package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
)

// TestInitConfigExplicitFileIsRead is the regression guard for the bug where
// `--config <path>` only called viper.SetConfigFile and never ReadInConfig, so
// the user's yaml was silently ignored and defaults applied. Writes a yaml
// with a non-default value and asserts Load() surfaces it.
func TestInitConfigExplicitFileIsRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(path, []byte("tools:\n  gcloud: false\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Save/restore package-level state touched by initConfig.
	origCfgFile := cfgFile
	cfgFile = path
	t.Cleanup(func() {
		cfgFile = origCfgFile
		viper.Reset()
	})
	viper.Reset()

	initConfig()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Tools["gcloud"] {
		t.Error("gcloud should be false after --config read — initConfig must call ReadInConfig")
	}
	// Every other tool must stay at its default value (true).
	for _, k := range catalog.Keys() {
		if k == "gcloud" {
			continue
		}
		if !cfg.Tools[k] {
			t.Errorf("tool %q should default to true alongside explicit override", k)
		}
	}
}

// TestInitConfigProjectFileFromCWD reproduces the bug where launching
// `toolbox shell` from a workspace that contains a `.toolbox.yaml` did not
// pick up the project file: viper's MergeInConfig path caches the first
// configFile resolved in initConfig, so when ~/.toolbox.yaml is absent the
// project file in CWD must still be discovered and merged.
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

	// Point HOME at an empty dir so no ~/.toolbox.yaml exists to mask the
	// project-level lookup. This mirrors the user-reported scenario.
	emptyHome := t.TempDir()
	t.Setenv("HOME", emptyHome)

	origCfgFile := cfgFile
	cfgFile = ""
	t.Cleanup(func() {
		cfgFile = origCfgFile
		_ = os.Chdir(origWD)
		viper.Reset()
	})
	viper.Reset()

	initConfig()

	if got := viper.GetString("mounts_root"); got != mountsRoot {
		t.Errorf("mounts_root from CWD .toolbox.yaml not loaded: got %q, want %q", got, mountsRoot)
	}
}

// TestInitConfigProjectFileWalksUpFromSubdir guards the user-reported bug:
// launching `toolbox shell` from a subdirectory of the workspace must still
// pick up the workspace's `.toolbox.yaml`. Mirrors how git resolves
// `.git` / `.gitignore` from any nested CWD.
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
		cfgFile = origCfgFile
		_ = os.Chdir(origWD)
		viper.Reset()
	})
	viper.Reset()

	initConfig()

	if got := viper.GetString("mounts_root"); got != mountsRoot {
		t.Errorf("walk-up did not find workspace .toolbox.yaml: got %q, want %q", got, mountsRoot)
	}
}

// TestInitConfigProjectFileStopsAtHome guards against the walk-up search
// re-reading ~/.toolbox.yaml as if it were a project file. The global is
// handled by the dedicated branch above; the walk must terminate at HOME so
// it can't double-load (and so a directory shape like ~/work/proj doesn't
// silently treat ~ as the project root).
func TestInitConfigProjectFileStopsAtHome(t *testing.T) {
	home := t.TempDir()
	// Put a file directly at HOME — this represents the global config and
	// must NOT be picked up as a project config by the walk-up path.
	homeMounts := filepath.Join(home, "global-mounts")
	yaml := "mounts_root: " + homeMounts + "\n"
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write home config: %v", err)
	}
	// CWD = a sibling directory inside HOME (no project config of its own).
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
		cfgFile = origCfgFile
		_ = os.Chdir(origWD)
		viper.Reset()
	})
	viper.Reset()

	initConfig()

	// Global path *does* read this file (initConfig still loads ~/.toolbox.yaml
	// via AddConfigPath(home)), so mounts_root will equal homeMounts. The
	// regression guard is that the walk-up did not also try to merge the
	// HOME file as a project config (which would be a no-op here, but proves
	// pathological behavior in edge cases). We assert findProjectConfig
	// directly returns "" for a CWD inside HOME with no nested config.
	if got := findProjectConfig(sub); got != "" {
		t.Errorf("findProjectConfig should stop at HOME, got %q", got)
	}
	// Sanity: global was read.
	if got := viper.GetString("mounts_root"); got != homeMounts {
		t.Errorf("global config not loaded: got %q, want %q", got, homeMounts)
	}
}

// TestInitConfigAppliesDefaults verifies setDefaults() now runs on every
// initConfig path (both the --config and the default-search branches). Before
// the fix, defaults were only set in the else branch, so a `--config` user
// that omitted a tool key missed the viper-side default.
func TestInitConfigAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("# empty\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	origCfgFile := cfgFile
	cfgFile = path
	t.Cleanup(func() {
		cfgFile = origCfgFile
		viper.Reset()
	})
	viper.Reset()

	initConfig()

	for _, k := range catalog.Keys() {
		if !viper.GetBool("tools." + k) {
			t.Errorf("default tools.%s should be true after initConfig, got false", k)
		}
	}
}
