package config

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestDefaultMounts(t *testing.T) {
	mounts := DefaultMounts()

	// Must return 8 mounts.
	if len(mounts) != 8 {
		t.Fatalf("expected 8 default mounts, got %d", len(mounts))
	}

	// ~/.secrets must NOT be present (D-08).
	for _, m := range mounts {
		if m.Source == "~/.secrets" {
			t.Error("~/.secrets should NOT be in default mounts (D-08)")
		}
	}

	// Every ~-based source must live under ~/.toolbox/ so host creds are
	// not leaked into the container.
	for _, m := range mounts {
		if strings.HasPrefix(m.Source, "~/") && !strings.HasPrefix(m.Source, "~/.toolbox/") {
			t.Errorf("mount source %q must be under ~/.toolbox/", m.Source)
		}
	}

	// ~/.toolbox/.claude must be rw and auto-created.
	assertMount(t, mounts, "~/.toolbox/.claude", false, true)
	// ~/.toolbox/state must be rw and auto-created.
	assertMount(t, mounts, "~/.toolbox/state", false, true)
	// ~/.toolbox/gh / ~/.toolbox/glab must be rw and auto-created.
	assertMount(t, mounts, "~/.toolbox/gh", false, true)
	assertMount(t, mounts, "~/.toolbox/glab", false, true)

	// ssh + git config follow the host via symlinks, not copies.
	assertSymlink(t, mounts, "~/.toolbox/ssh", "~/.ssh")
	assertSymlink(t, mounts, "~/.toolbox/gitconfig", "~/.gitconfig")
	assertSymlink(t, mounts, "~/.toolbox/gitconfig-dbm", "~/.gitconfig-dbm")
}

func assertMount(t *testing.T, mounts []Mount, src string, wantRO, wantCreate bool) {
	t.Helper()
	for _, m := range mounts {
		if m.Source != src {
			continue
		}
		if m.ReadOnly != wantRO {
			t.Errorf("%s: ReadOnly = %v, want %v", src, m.ReadOnly, wantRO)
		}
		if m.CreateIfMissing != wantCreate {
			t.Errorf("%s: CreateIfMissing = %v, want %v", src, m.CreateIfMissing, wantCreate)
		}
		return
	}
	t.Errorf("mount %s not found in DefaultMounts()", src)
}

func assertSymlink(t *testing.T, mounts []Mount, src, wantLink string) {
	t.Helper()
	for _, m := range mounts {
		if m.Source != src {
			continue
		}
		if m.SymlinkFrom != wantLink {
			t.Errorf("%s: SymlinkFrom = %q, want %q", src, m.SymlinkFrom, wantLink)
		}
		return
	}
	t.Errorf("mount %s not found in DefaultMounts()", src)
}

// setToolsDefaults mirrors the per-leaf defaults from cmd/root.go.
func setToolsDefaults() {
	for _, k := range KnownTools {
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

	if len(cfg.Mounts) != 8 {
		t.Errorf("expected 8 default mounts, got %d", len(cfg.Mounts))
	}

	if !IsDefaultTools(cfg.Tools) {
		t.Errorf("Load() with no user config should yield default tools, got %v", cfg.Tools)
	}
	for _, k := range KnownTools {
		if !cfg.Tools[k] {
			t.Errorf("tool %q should default to true", k)
		}
	}
}

// TestLoadUserOverridePreservesOtherTools reproduces the merge semantics:
// a .toolbox.yaml that flips a single tool must leave every other default
// untouched. This is the main contract the user asked for — "override solo
// le chiavi modificate, il resto eredita dalla globale".
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
	for _, k := range KnownTools {
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
