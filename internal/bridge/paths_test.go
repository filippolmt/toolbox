package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/fsx"
)

func TestResolveHostState_UsesHomeDir(t *testing.T) {
	host := testHost(t)

	s, err := ResolveHostState(host)
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}
	want := filepath.Join(host.Home, HostDir)
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
	if s.Legacy != filepath.Join(host.Home, LegacyHostDir) {
		t.Errorf("Legacy = %q, want %q", s.Legacy, filepath.Join(host.Home, LegacyHostDir))
	}
}

// TestResolveHostState_RejectsAHostWithoutAHome: the strictness fsx.Home
// enforced on the ambient read has to hold at the seam that replaced it, or a
// zero-valued Host would resolve the state dir to /.toolbox/toolbox/bridge.
func TestResolveHostState_RejectsAHostWithoutAHome(t *testing.T) {
	if _, err := ResolveHostState(fsx.Host{}); err == nil {
		t.Fatal("a Host with no home must fail, not resolve onto the filesystem root")
	}
}

func TestEnsureHostDir_Idempotent(t *testing.T) {
	host := testHost(t)
	s, err := ResolveHostState(host)
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
