package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBridgeContract_ShimMatchesGo binds the daemon↔shim wire contract: the
// container-side paths/filenames the shell shim hardcodes must equal the Go
// constants the daemon writes. They live in two languages linked only by
// comments ("Must match BRIDGE_SOCK in bridge-lib.sh"); a rename on either
// side otherwise breaks the bridge silently. Mirrors the init.d + Tool Catalog
// bijection tests — drift is a red test, not a field report.
func TestBridgeContract_ShimMatchesGo(t *testing.T) {
	const shimPath = "../build/assets/bin/bridge-lib.sh"
	raw, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatalf("read shim %s: %v", shimPath, err)
	}
	shim := string(raw)

	// Each Go literal the shim must contain verbatim.
	for _, c := range []struct{ name, literal string }{
		{"ContainerDir", ContainerDir},
		{"LegacyContainerDir", LegacyContainerDir},
		{"ContainerSocket", ContainerSocket},
		{"tokenFile", tokenFile},
		{"portFile", portFile},
	} {
		if !strings.Contains(shim, c.literal) {
			t.Errorf("%s: shim %s does not contain %q — Go/shell bridge contract drifted", c.name, shimPath, c.literal)
		}
	}
}

func TestResolveHostState_UsesHomeDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home, _ := os.UserHomeDir()

	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}
	want := filepath.Join(home, HostDir)
	if s.Dir != want {
		t.Errorf("Dir = %q, want %q", s.Dir, want)
	}
	if s.Token != filepath.Join(want, "token") {
		t.Errorf("Token = %q, want %q", s.Token, filepath.Join(want, "token"))
	}
	if s.Port != filepath.Join(want, "port") {
		t.Errorf("Port = %q, want %q", s.Port, filepath.Join(want, "port"))
	}
	if s.Log != filepath.Join(want, "log") {
		t.Errorf("Log = %q, want %q", s.Log, filepath.Join(want, "log"))
	}
	if s.PID != filepath.Join(want, "pid") {
		t.Errorf("PID = %q, want %q", s.PID, filepath.Join(want, "pid"))
	}
	if s.RunDir != filepath.Join(want, "run") {
		t.Errorf("RunDir = %q, want %q", s.RunDir, filepath.Join(want, "run"))
	}
	if s.Socket != filepath.Join(want, "run", "bridge.sock") {
		t.Errorf("Socket = %q, want %q", s.Socket, filepath.Join(want, "run", "bridge.sock"))
	}
}

func TestEnsureHostDir_Idempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}
	for range 2 {
		if err := EnsureHostDir(s); err != nil {
			t.Fatalf("EnsureHostDir: %v", err)
		}
	}
	info, err := os.Stat(s.Dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", info.Mode().Perm())
	}
}
