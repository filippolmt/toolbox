package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
)

// resetCmdState restores the package-level vars touched by initConfig so
// tests don't bleed state into each other. Replaces the previous viper.Reset()
// pattern (D-09 — no global viper churn).
func resetCmdState(t *testing.T, origCfgFile string) {
	t.Helper()
	cfgFile = origCfgFile
	cfg = nil
}

// TestInitConfigExplicitFileIsRead is the regression guard for the bug where
// `--config <path>` only called viper.SetConfigFile and never ReadInConfig, so
// the user's yaml was silently ignored and defaults applied. Writes a yaml
// with a non-default value and asserts the resolved *Config surfaces it.
func TestInitConfigExplicitFileIsRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(path, []byte("tools:\n  gcloud: false\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	origCfgFile := cfgFile
	cfgFile = path
	t.Cleanup(func() { resetCmdState(t, origCfgFile) })

	initConfig()

	if cfg == nil {
		t.Fatal("cfg should be populated after initConfig")
	}
	if cfg.Tools["gcloud"] {
		t.Error("gcloud should be false after --config read")
	}
	for _, k := range catalog.Keys() {
		if k == "gcloud" {
			continue
		}
		if !cfg.Tools[k] {
			t.Errorf("tool %q should default to true alongside explicit override", k)
		}
	}
}

// TestInitConfigProjectFileFromCWD: launching `toolbox shell` from a workspace
// with a `.toolbox.yaml` must pick up the project file via Plan's walk-up.
func TestInitConfigProjectFileFromCWD(t *testing.T) {
	dir := t.TempDir()
	mountsRoot := filepath.Join(dir, "mounts")
	yaml := "mounts_root: " + mountsRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".toolbox.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	emptyHome := t.TempDir()
	t.Setenv("HOME", emptyHome)

	origCfgFile := cfgFile
	cfgFile = ""
	t.Cleanup(func() {
		resetCmdState(t, origCfgFile)
		_ = os.Chdir(origWD)
	})

	initConfig()

	if cfg == nil {
		t.Fatal("cfg should be populated")
	}
	if cfg.MountsRoot != mountsRoot {
		t.Errorf("mounts_root from CWD .toolbox.yaml not loaded: got %q, want %q", cfg.MountsRoot, mountsRoot)
	}
}

// TestInitConfigProjectFileWalksUpFromSubdir: launching from a subdirectory
// of the workspace must still find the workspace's .toolbox.yaml.
func TestInitConfigProjectFileWalksUpFromSubdir(t *testing.T) {
	workspace := t.TempDir()
	mountsRoot := filepath.Join(workspace, "mounts")
	yaml := "mounts_root: " + mountsRoot + "\n"
	if err := os.WriteFile(filepath.Join(workspace, ".toolbox.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	nested := filepath.Join(workspace, "deep", "nested", "subdir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatalf("chdir nested: %v", err)
	}

	emptyHome := t.TempDir()
	t.Setenv("HOME", emptyHome)

	origCfgFile := cfgFile
	cfgFile = ""
	t.Cleanup(func() {
		resetCmdState(t, origCfgFile)
		_ = os.Chdir(origWD)
	})

	initConfig()

	if cfg == nil {
		t.Fatal("cfg should be populated")
	}
	if cfg.MountsRoot != mountsRoot {
		t.Errorf("walk-up did not find workspace .toolbox.yaml: got %q, want %q", cfg.MountsRoot, mountsRoot)
	}
}

// TestInitConfigProjectFileStopsAtHome: the walk-up must terminate at HOME so
// the global ~/.toolbox.yaml is not re-read as a project file. The global
// branch DOES read the file, so the resolved cfg.MountsRoot equals the global
// value — the regression guard is that running from inside HOME doesn't
// double-load the file or treat it as a project root.
func TestInitConfigProjectFileStopsAtHome(t *testing.T) {
	home := t.TempDir()
	homeMounts := filepath.Join(home, "global-mounts")
	yaml := "mounts_root: " + homeMounts + "\n"
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write home config: %v", err)
	}
	sub := filepath.Join(home, "work")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatalf("chdir sub: %v", err)
	}
	t.Setenv("HOME", home)

	origCfgFile := cfgFile
	cfgFile = ""
	t.Cleanup(func() {
		resetCmdState(t, origCfgFile)
		_ = os.Chdir(origWD)
	})

	initConfig()

	if cfg == nil {
		t.Fatal("cfg should be populated")
	}
	// Walk-up must NOT have picked up ~/.toolbox.yaml as a project file.
	// The global branch read the same file once — so the resolved value
	// equals homeMounts. The deeper invariant (walk-up stops at HOME) is
	// pinned by internal/config/plan_test.go::TestWalkUpStopsAtHome (Plan 01).
	if cfg.MountsRoot != homeMounts {
		t.Errorf("global config not loaded: got %q, want %q", cfg.MountsRoot, homeMounts)
	}
}

// TestInitConfigAppliesDefaults: catalog tool defaults must apply on the
// --config branch too (regression guard from before defaults were unified
// across both branches).
func TestInitConfigAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("# empty\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	origCfgFile := cfgFile
	cfgFile = path
	t.Cleanup(func() { resetCmdState(t, origCfgFile) })

	initConfig()

	if cfg == nil {
		t.Fatal("cfg should be populated")
	}
	for _, k := range catalog.Keys() {
		if !cfg.Tools[k] {
			t.Errorf("default tools.%s should be true after initConfig, got false", k)
		}
	}
}
