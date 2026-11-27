package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// hcCall is one argv the /hc handler handed the host's hc.
type hcCall []string

// newHCTestHandler builds a handler whose hc executor records every argv and
// answers `get` with getJSON.
func newHCTestHandler(t *testing.T, getJSON string) (http.Handler, *[]hcCall) {
	t.Helper()
	var calls []hcCall
	h := buildTestHandler(t, handlerFns{hc: func(_ context.Context, args []string) ([]byte, int, error) {
		calls = append(calls, hcCall(args))
		if args[0] == "get" {
			return []byte(getJSON), 0, nil
		}
		return []byte("ok\n"), 0, nil
	}})
	return h, &calls
}

func hcBody(t *testing.T, command string, args ...string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{FieldCommand: command, FieldArgs: args})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestHCRelay_ArgvGate pins the Host Terminal Relay's argv gate: the verbs
// that only look (ls, get, read) and a plain send reach the host's hc, rebuilt
// with `--` before the positionals so no container-supplied word becomes an
// option; anything else — another verb, an hc flag, a terminal on another
// machine — is refused before hc runs.
func TestHCRelay_ArgvGate(t *testing.T) {
	for _, tc := range []struct {
		label   string
		command string
		args    []string
		want    hcCall
	}{
		{"ls", "ls", nil, hcCall{"ls", "--json"}},
		{"ls --all", "ls", []string{"--all"}, hcCall{"ls", "--json", "--all"}},
		{"get", "get", []string{"t2"}, hcCall{"get", "--json", "--", "t2"}},
		{"get by name", "get", []string{"reviewer_1"}, hcCall{"get", "--json", "--", "reviewer_1"}},
		{"read", "read", []string{"t2"}, hcCall{"read", "--", "t2"}},
		{"read --lines", "read", []string{"t2", "--lines", "40"}, hcCall{"read", "--lines", "40", "--", "t2"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			h, calls := newHCTestHandler(t, "")
			rr := doPostTo(t, h, RouteHC, "tok", hcBody(t, tc.command, tc.args...))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", rr.Code, rr.Body)
			}
			if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0], tc.want) {
				t.Errorf("hc argv = %q, want %q", *calls, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		label   string
		command string
		args    []string
	}{
		{"run", "run", []string{"claude"}},
		{"exec", "exec", []string{"id"}},
		{"keys", "keys", []string{"t2", "enter"}},
		{"machine", "machine", []string{"ls"}},
		{"wait", "wait", []string{"t2"}},
		{"ls with another flag", "ls", []string{"--label", "a=b"}},
		{"get on another machine", "get", []string{"spike/t1"}},
		{"get with a flag for a terminal", "get", []string{"--help"}},
		{"get with no terminal", "get", nil},
		{"read with a bad line count", "read", []string{"t2", "--lines", "-1"}},
		{"read with a stray word", "read", []string{"t2", "t3"}},
		{"send --force", "send", []string{"--force", "t2", "hi"}},
		{"send --wait", "send", []string{"t2", "hi", "--wait"}},
		{"send to another machine", "send", []string{"toolbox/t2", "hi"}},
		{"send empty text", "send", []string{"t2", ""}},
	} {
		t.Run("refuses "+tc.label, func(t *testing.T) {
			h, calls := newHCTestHandler(t, "")
			rr := doPostTo(t, h, RouteHC, "tok", hcBody(t, tc.command, tc.args...))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d (%s), want 400", rr.Code, rr.Body)
			}
			if len(*calls) != 0 {
				t.Errorf("hc ran %q, want nothing run", *calls)
			}
		})
	}
}

// TestHCRelay_SendOnlyToAReadyAgent: a send types into a host terminal, so it
// reaches only one whose foreground program herdr-core recognises as an agent
// waiting for input. A shell (no status) or a working agent is refused with
// nothing typed; an idle one gets `--if idle`, which closes the window between
// the check and the keystrokes.
func TestHCRelay_SendOnlyToAReadyAgent(t *testing.T) {
	for _, tc := range []struct {
		label  string
		get    string
		status int
		send   hcCall
	}{
		{"idle agent", `{"status":{"app":"claude-code","state":"idle"}}`, http.StatusOK, hcCall{"send", "--if", "idle", "--", "t2", "hi"}},
		{"done agent", `{"status":{"app":"codex","state":"done"}}`, http.StatusOK, hcCall{"send", "--", "t2", "hi"}},
		{"shell", `{"status":null}`, http.StatusConflict, nil},
		{"working agent", `{"status":{"app":"claude-code","state":"working"}}`, http.StatusConflict, nil},
		{"blocked agent", `{"status":{"app":"claude-code","state":"blocked"}}`, http.StatusConflict, nil},
		{"unreadable get", `not json`, http.StatusConflict, nil},
	} {
		t.Run(tc.label, func(t *testing.T) {
			h, calls := newHCTestHandler(t, tc.get)
			rr := doPostTo(t, h, RouteHC, "tok", hcBody(t, "send", "t2", "hi"))
			if rr.Code != tc.status {
				t.Fatalf("status = %d (%s), want %d", rr.Code, rr.Body, tc.status)
			}
			want := []hcCall{{"get", "--json", "--", "t2"}}
			if tc.send != nil {
				want = append(want, tc.send)
			}
			if !reflect.DeepEqual(*calls, want) {
				t.Errorf("hc argv = %q, want %q", *calls, want)
			}
		})
	}
}

// TestLaunchHC_FindsTheUserLocalHC: the bridge runs as a LaunchAgent whose
// PATH is /usr/bin:/bin:/usr/sbin:/sbin, while herdr-core installs hc in
// ~/.local/bin. The child gets HOME, which is how hc finds the user's server.
func TestLaunchHC_FindsTheUserLocalHC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh script fake not runnable on windows")
	}
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "hc"), []byte("#!/bin/sh\necho \"$HOME $*\"\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "/elsewhere")
	out, exit, err := launchHC(context.Background(), fsx.Host{Home: home}, []string{"get", "--json", "--", "t2"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if exit != 3 {
		t.Errorf("exit = %d, want hc's own 3", exit)
	}
	if got, want := strings.TrimSpace(string(out)), home+" get --json -- t2"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestLaunchHC_NoHC(t *testing.T) {
	_, _, err := launchHC(context.Background(), fsx.Host{Home: t.TempDir()}, []string{"ls", "--json"})
	if err != ErrHCNotInstalled {
		t.Errorf("err = %v, want ErrHCNotInstalled", err)
	}
}
