package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetCmdState restores the package-level vars touched by initConfig so
// tests don't bleed state into each other.
func resetCmdState(t *testing.T, origCfgFile string) {
	t.Helper()
	cfgFile = origCfgFile
	cfg = nil
}

// TestInitConfigExplicitFileIsRead asserts the --config flag wiring reads
// the supplied yaml end-to-end.
func TestInitConfigExplicitFileIsRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(path, []byte("inherit_host_auth: [gh]\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	origCfgFile := cfgFile
	cfgFile = path
	t.Cleanup(func() { resetCmdState(t, origCfgFile) })

	initConfig()

	if cfg == nil {
		t.Fatal("cfg should be populated after initConfig")
	}
	if len(cfg.InheritHostAuth) != 1 || cfg.InheritHostAuth[0] != "gh" {
		t.Errorf("InheritHostAuth = %v, want [gh] after --config read", cfg.InheritHostAuth)
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

// TestInitConfigProjectFileStopsAtHome: the walk-up must terminate at HOME.
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
	if cfg.MountsRoot != homeMounts {
		t.Errorf("global config not loaded: got %q, want %q", cfg.MountsRoot, homeMounts)
	}
}

// TestInitConfigAppliesDefaults: a config with no fields set must still
// produce a sensible default cfg.
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
	if cfg.Shell != "zsh" {
		t.Errorf("Shell = %q, want zsh (default)", cfg.Shell)
	}
	if len(cfg.InheritHostAuth) != 0 {
		t.Errorf("InheritHostAuth = %v, want empty default", cfg.InheritHostAuth)
	}
}

// TestConfigEditReportsFindingsAndFails: `config edit` hands the file to
// $EDITOR, which can write anything — the one write path no seam can gate. So
// it checks afterwards: findings are reported and the exit is non-zero, but the
// file is left exactly as the user saved it. Reverting hand-written work would
// be hostile, which is why this is a report and not the ApplyChecked gate.
func TestConfigEditReportsFindingsAndFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdirTemp(t)
	t.Setenv("EDITOR", "true") // stand-in for the user saving the file below
	cfgPath := filepath.Join(home, ".toolbox.yaml")
	saved := "shells:\n  broken:\n    env:\n      A: \"1\"\n"
	if err := os.WriteFile(cfgPath, []byte(saved), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	out := &bytes.Buffer{}
	configEditCmd.SetOut(out)
	configEditCmd.SetErr(out)

	err := runConfigEdit(configEditCmd, nil)
	if err == nil {
		t.Fatal("an invalid file left by the editor must exit non-zero")
	}
	if !strings.Contains(out.String(), "shells.broken.path is empty") {
		t.Errorf("findings must be reported, got: %s", out.String())
	}
	if got, readErr := os.ReadFile(cfgPath); readErr != nil || string(got) != saved {
		t.Errorf("the user's own edit must survive verbatim, got %q (err=%v)", got, readErr)
	}
}
