package bridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// fakeDaemon is a /healthz the test controls: version "" answers the
// pre-version body, as a daemon built before the field existed does.
type fakeDaemon struct {
	mu      sync.Mutex
	version string
}

func (d *fakeDaemon) set(v string) { d.mu.Lock(); d.version = v; d.mu.Unlock() }

// startFakeDaemon serves /healthz on loopback and records its port in host's
// bridge state, where the CLI finds a real daemon.
func startFakeDaemon(t *testing.T, host fsx.Host, version string) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{version: version}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RouteHealth {
			http.NotFound(w, r)
			return
		}
		d.mu.Lock()
		body := map[string]string{"status": "ok"}
		if d.version != "" {
			body[FieldVersion] = d.version
		}
		d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	s, err := ResolveHostState(host)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureHostDir(s); err != nil {
		t.Fatal(err)
	}
	if err := WritePort(s, port); err != nil {
		t.Fatal(err)
	}
	return d
}

// writeExe creates an executable file standing in for a toolbox binary.
func writeExe(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

func TestCheckAtSessionStartRestartsAnOlderDaemonOfTheSameBinary(t *testing.T) {
	host := testHost(t)
	self := writeExe(t, filepath.Join(t.TempDir(), "toolbox"))
	d := startFakeDaemon(t, host, "") // predates the version field
	fa := &fakeAgent{status: AgentStatus{Installed: true}, execPath: self}
	fa.onRestart = func() { d.set("1.2.3") }

	var out strings.Builder
	CheckAtSessionStart(host, fa, self, "1.2.3", &out)

	if fa.restartCalls != 1 {
		t.Errorf("restart calls = %d, want 1", fa.restartCalls)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "1.2.3") {
		t.Errorf("stderr = %q, want one line naming 1.2.3", out.String())
	}
}
