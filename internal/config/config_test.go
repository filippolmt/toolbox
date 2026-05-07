package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
)

// TestLoadSmoke proves the deprecated Load() wrapper still works end-to-end:
// in a clean filesystem (empty HOME, CWD with no .toolbox.yaml), Load
// returns a *Config with default Shell + every catalog tool default-true.
//
// Byte-merge scenarios used to live here (TestLoadWithoutConfig,
// TestLoadUserOverridePreservesOtherTools, etc.). They moved to
// internal/config/merge_test.go::TestMergeScenarios after Phase 08
// because Load() now delegates to Plan, which uses a fresh viper.New()
// per call and ignores the global viper singleton — so the old
// `viper.ReadConfig(bytes.NewBufferString(...)) + Load()` priming
// pattern stops reaching the merge logic.
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
	for _, k := range catalog.Keys() {
		if !cfg.Tools[k] {
			t.Errorf("tool %q should default to true after Load() in clean fs, got false", k)
		}
	}
	// Confirm a non-existent ~/.toolbox.yaml is non-fatal — the smoke test
	// explicitly does NOT plant one. If the path existed it would surface
	// as a non-default Shell or non-default Tools value above.
	_, statErr := os.Stat(filepath.Join(home, ".toolbox.yaml"))
	if !os.IsNotExist(statErr) {
		t.Fatalf("test fixture broken: ~/.toolbox.yaml should not exist; stat err=%v", statErr)
	}
}

func TestIsDefaultTools(t *testing.T) {
	if !IsDefaultTools(DefaultTools()) {
		t.Error("DefaultTools() should be recognised as default")
	}
	if !IsDefaultTools(nil) {
		t.Error("nil map (no user config) should be treated as default")
	}
	if !IsDefaultTools(map[string]bool{}) {
		t.Error("empty map should be treated as default (all keys default-true)")
	}
	custom := DefaultTools()
	custom["gcloud"] = false
	if IsDefaultTools(custom) {
		t.Error("one tool disabled should not be considered default")
	}
}

// TestToolBuildArgGo cross-checks that the Go toolchain key maps to the
// correct Dockerfile ARG. This is the in-code half of GO-04 cascade; the
// Dockerfile half is enforced end-to-end by the smoke test.
func TestToolBuildArgGo(t *testing.T) {
	if got := catalog.BuildArg("go"); got != "INSTALL_GO" {
		t.Errorf("catalog.BuildArg(\"go\") = %q, want %q", got, "INSTALL_GO")
	}
}
