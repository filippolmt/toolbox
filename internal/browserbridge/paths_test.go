package browserbridge

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
		t.Errorf("Token = %q", s.Token)
	}
	if s.Port != filepath.Join(want, "port") {
		t.Errorf("Port = %q", s.Port)
	}
	if s.Log != filepath.Join(want, "log") {
		t.Errorf("Log = %q", s.Log)
	}
	if s.PID != filepath.Join(want, "pid") {
		t.Errorf("PID = %q", s.PID)
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
