package config

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/filippolmt/toolbox/internal/catalog"
)

// setToolsDefaults mirrors the per-leaf defaults from cmd/root.go.
func setToolsDefaults() {
	for _, k := range catalog.Keys() {
		viper.SetDefault("tools."+k, true)
	}
}

func TestLoadWithoutConfig(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Load no longer merges Mounts — the user list stays as parsed (empty
	// when no yaml). The full pipeline lives behind mountplan.Plan; this
	// test guards Load's tool defaults contract.
	if len(cfg.Mounts) != 0 {
		t.Errorf("Load() with no user config should leave cfg.Mounts empty, got %d", len(cfg.Mounts))
	}

	if !IsDefaultTools(cfg.Tools) {
		t.Errorf("Load() with no user config should yield default tools, got %v", cfg.Tools)
	}
	for _, k := range catalog.Keys() {
		if !cfg.Tools[k] {
			t.Errorf("tool %q should default to true", k)
		}
	}
}

// TestLoadUserOverridePreservesOtherTools reproduces the merge semantics:
// a .toolbox.yaml that flips a single tool must leave every other default
// untouched.
func TestLoadUserOverridePreservesOtherTools(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("tools:\n  gcloud: false\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Tools["gcloud"] {
		t.Error("gcloud should be disabled after override")
	}
	for _, k := range catalog.Keys() {
		if k == "gcloud" {
			continue
		}
		if !cfg.Tools[k] {
			t.Errorf("tool %q should remain true — one-key override must not reset others", k)
		}
	}
	if IsDefaultTools(cfg.Tools) {
		t.Error("IsDefaultTools should be false once any tool is opted out")
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
// Dockerfile half is enforced end-to-end by the smoke test in Plan 03.
func TestToolBuildArgGo(t *testing.T) {
	if got := catalog.BuildArg("go"); got != "INSTALL_GO" {
		t.Errorf("catalog.BuildArg(\"go\") = %q, want %q", got, "INSTALL_GO")
	}
}

// TestLoadMountsRootBareTildeRejected: bare "~" would rewrite
// ~/.toolbox/<x> to ~/<x> — dropping the isolation namespace and writing
// toolbox state directly under the host home (the exact leak the default
// mount set guards against). Refuse it loudly with a message that points
// to the fix. ValidateMountsRoot is the validator; Load consumes it.
func TestLoadMountsRootBareTildeRejected(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("mounts_root: \"~\"\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should reject bare ~ as mounts_root")
	}
	if !strings.Contains(err.Error(), "mounts_root") || !strings.Contains(err.Error(), "isolation") {
		t.Errorf("error should explain the isolation footgun, got: %v", err)
	}
}

// TestLoadMountsRootRelativeRejected: relative mounts_root is a likely
// mistake — refuse it loudly instead of silently resolving against CWD.
func TestLoadMountsRootRelativeRejected(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("mounts_root: ./relative\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load() should reject relative mounts_root")
	} else if !strings.Contains(err.Error(), "mounts_root") {
		t.Errorf("error should mention mounts_root, got: %v", err)
	}
}
