package config

import (
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

func TestImageRef(t *testing.T) {
	cfg := Config{
		Image: ImageConfig{Name: "toolbox", Tag: "local"},
	}
	expected := "toolbox:local"
	if got := cfg.ImageRef(); got != expected {
		t.Errorf("ImageRef() = %q, want %q", got, expected)
	}
}

func TestLoadWithoutConfig(t *testing.T) {
	// Reset viper for an isolated test.
	viper.Reset()

	// Mirror the defaults set by cmd/root.go.
	viper.SetDefault("image.name", "toolbox")
	viper.SetDefault("image.tag", "local")
	viper.SetDefault("build.context", ".")
	viper.SetDefault("build.dockerfile", "docker/Dockerfile")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// image.name must be "toolbox".
	if cfg.Image.Name != "toolbox" {
		t.Errorf("Image.Name = %q, want %q", cfg.Image.Name, "toolbox")
	}

	// Must receive 8 default mounts.
	if len(cfg.Mounts) != 8 {
		t.Errorf("expected 8 default mounts, got %d", len(cfg.Mounts))
	}
}
