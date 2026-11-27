package bridge

import (
	"errors"
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
	"time"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// fakeDaemon is a /healthz the test controls: version "" answers the
// pre-version body, as a daemon built before the field existed does.
type fakeDaemon struct {
	mu      sync.Mutex
	version string
	hits    int
}

func (d *fakeDaemon) probes() int { d.mu.Lock(); defer d.mu.Unlock(); return d.hits }

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
		d.hits++
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

func TestCheckAtSessionStartLeavesAMatchingDaemonAlone(t *testing.T) {
	host := testHost(t)
	self := writeExe(t, filepath.Join(t.TempDir(), "toolbox"))
	startFakeDaemon(t, host, "1.2.3")
	fa := &fakeAgent{status: AgentStatus{Installed: true}, execPath: self}

	var out strings.Builder
	CheckAtSessionStart(host, fa, self, "1.2.3", &out)

	if fa.restartCalls != 0 || out.Len() != 0 {
		t.Errorf("restart calls = %d, stderr = %q; want none", fa.restartCalls, out.String())
	}
}

// A unit naming a symlink (the Homebrew cask's /opt/homebrew/bin/toolbox) to
// the running binary is the same binary.
func TestCheckAtSessionStartTreatsASymlinkToSelfAsTheSameBinary(t *testing.T) {
	host := testHost(t)
	dir := t.TempDir()
	self := writeExe(t, filepath.Join(dir, "toolbox"))
	link := filepath.Join(dir, "toolbox-link")
	if err := os.Symlink(self, link); err != nil {
		t.Fatal(err)
	}
	d := startFakeDaemon(t, host, "1.2.2")
	fa := &fakeAgent{status: AgentStatus{Installed: true}, execPath: link}
	fa.onRestart = func() { d.set("1.2.3") }

	var out strings.Builder
	CheckAtSessionStart(host, fa, self, "1.2.3", &out)

	if fa.restartCalls != 1 || !strings.Contains(out.String(), "refreshed from 1.2.2 to 1.2.3") {
		t.Errorf("restart calls = %d, stderr = %q; want one restart, refreshed", fa.restartCalls, out.String())
	}
}

func TestCheckAtSessionStartNeverRestartsADaemonOfAnotherBinary(t *testing.T) {
	for _, tc := range []struct {
		name, daemon string
		wantLines    int
	}{
		{"versions differ", "0.9.0", 1},
		{"versions match", "1.2.3", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := testHost(t)
			dir := t.TempDir()
			self := writeExe(t, filepath.Join(dir, "toolbox"))
			other := writeExe(t, filepath.Join(dir, "checkout-toolbox"))
			startFakeDaemon(t, host, tc.daemon)
			fa := &fakeAgent{status: AgentStatus{Installed: true}, execPath: other}

			var out strings.Builder
			CheckAtSessionStart(host, fa, self, "1.2.3", &out)

			if fa.restartCalls != 0 || fa.installCalledWith != "" {
				t.Errorf("restart calls = %d, install = %q; want the unit untouched", fa.restartCalls, fa.installCalledWith)
			}
			got := strings.Count(out.String(), "\n")
			if got != tc.wantLines {
				t.Fatalf("stderr = %q, want %d line(s)", out.String(), tc.wantLines)
			}
			if got == 1 && !(strings.Contains(out.String(), "0.9.0") && strings.Contains(out.String(), "1.2.3") &&
				strings.Contains(out.String(), "toolbox bridge install")) {
				t.Errorf("stderr = %q, want both versions and 'toolbox bridge install'", out.String())
			}
		})
	}
}

func TestCheckAtSessionStartWithoutTheBridgeOnlyPrintsTheTip(t *testing.T) {
	host := testHost(t)
	self := writeExe(t, filepath.Join(t.TempDir(), "toolbox"))
	d := startFakeDaemon(t, host, "")
	fa := &fakeAgent{execPath: self}

	var out strings.Builder
	CheckAtSessionStart(host, fa, self, "1.2.3", &out)

	if out.String() != InstallTip+"\n" || d.probes() != 0 || fa.restartCalls != 0 {
		t.Errorf("stderr = %q, probes = %d, restarts = %d; want only the tip", out.String(), d.probes(), fa.restartCalls)
	}
}

// Failures are warnings: CheckAtSessionStart has no error to return, so the
// session path cannot fail on them; each leaves one warning line.
func TestCheckAtSessionStartWarnsAndCarriesOn(t *testing.T) {
	old := refreshWait
	refreshWait = 200 * time.Millisecond
	t.Cleanup(func() { refreshWait = old })

	t.Run("restart fails", func(t *testing.T) {
		host := testHost(t)
		self := writeExe(t, filepath.Join(t.TempDir(), "toolbox"))
		startFakeDaemon(t, host, "1.2.2")
		fa := &fakeAgent{status: AgentStatus{Installed: true}, execPath: self, restartErr: errors.New("kickstart refused")}
		var out strings.Builder
		CheckAtSessionStart(host, fa, self, "1.2.3", &out)
		assertOneWarning(t, out.String(), "kickstart refused")
	})
	t.Run("restarted daemon keeps the old version", func(t *testing.T) {
		host := testHost(t)
		self := writeExe(t, filepath.Join(t.TempDir(), "toolbox"))
		startFakeDaemon(t, host, "1.2.2")
		fa := &fakeAgent{status: AgentStatus{Installed: true}, execPath: self}
		var out strings.Builder
		CheckAtSessionStart(host, fa, self, "1.2.3", &out)
		assertOneWarning(t, out.String(), "still reports 1.2.2")
	})
	t.Run("daemon unreachable", func(t *testing.T) {
		host := testHost(t)
		self := writeExe(t, filepath.Join(t.TempDir(), "toolbox"))
		fa := &fakeAgent{status: AgentStatus{Installed: true}, execPath: self}
		var out strings.Builder
		CheckAtSessionStart(host, fa, self, "1.2.3", &out)
		assertOneWarning(t, out.String(), "not reachable")
		if fa.restartCalls != 0 {
			t.Errorf("restart calls = %d, want 0", fa.restartCalls)
		}
	})
	t.Run("unit unreadable", func(t *testing.T) {
		host := testHost(t)
		startFakeDaemon(t, host, "1.2.2")
		fa := &fakeAgent{status: AgentStatus{Installed: true}, execPathErr: errors.New("permission denied")}
		var out strings.Builder
		CheckAtSessionStart(host, fa, "/self", "1.2.3", &out)
		assertOneWarning(t, out.String(), "permission denied")
	})
}

func assertOneWarning(t *testing.T, out, want string) {
	t.Helper()
	if strings.Count(out, "\n") != 1 || !strings.HasPrefix(out, "toolbox: warning: ") || !strings.Contains(out, want) {
		t.Errorf("stderr = %q, want one warning containing %q", out, want)
	}
}

func TestServiceFileExecParsers(t *testing.T) {
	plist := `<dict>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/homebrew/bin/toolbox</string>
    <string>bridge</string>
    <string>daemon</string>
  </array>
</dict>`
	if got, err := plistExec(plist); err != nil || got != "/opt/homebrew/bin/toolbox" {
		t.Errorf("plistExec = %q, %v", got, err)
	}
	for body, want := range map[string]string{
		"[Service]\nExecStart=/home/u/my tools/toolbox bridge daemon\n": "/home/u/my tools/toolbox",
		"[Service]\nExecStart=/usr/bin/toolbox browser-bridge daemon\n": "/usr/bin/toolbox",
	} {
		if got, err := unitExec(body); err != nil || got != want {
			t.Errorf("unitExec(%q) = %q, %v; want %q", body, got, err, want)
		}
	}
	if _, err := unitExec("[Service]\n"); err == nil {
		t.Error("unitExec without ExecStart: want an error")
	}
}
