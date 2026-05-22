package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadSmoke proves the deprecated Load() wrapper still works end-to-end:
// in a clean filesystem (empty HOME, CWD with no .toolbox.yaml), Load
// returns a *Config with default Shell + nil/empty InheritHostAuth.
func TestLoadSmoke(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.Shell != "zsh" {
		t.Errorf("Shell = %q, want zsh (default)", cfg.Shell)
	}
	if len(cfg.InheritHostAuth) != 0 {
		t.Errorf("InheritHostAuth = %v, want empty (isolated default)", cfg.InheritHostAuth)
	}
	_, statErr := os.Stat(filepath.Join(home, ".toolbox.yaml"))
	if !os.IsNotExist(statErr) {
		t.Fatalf("test fixture broken: ~/.toolbox.yaml should not exist; stat err=%v", statErr)
	}
}
