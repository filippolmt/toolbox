package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

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
	for _, k := range config.KnownTools {
		if k == "gcloud" {
			continue
		}
		if !cfg.Tools[k] {
			t.Errorf("tool %q should default to true alongside explicit override", k)
		}
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

	for _, k := range config.KnownTools {
		if !viper.GetBool("tools." + k) {
			t.Errorf("default tools.%s should be true after initConfig, got false", k)
		}
	}
}
