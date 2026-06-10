package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

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
