package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestDefaultMounts(t *testing.T) {
	mounts := DefaultMounts()

	// Must return 5 mounts.
	if len(mounts) != 5 {
		t.Fatalf("expected 5 default mounts, got %d", len(mounts))
	}

	// ~/.secrets must NOT be present (D-08).
	for _, m := range mounts {
		if m.Source == "~/.secrets" {
			t.Error("~/.secrets should NOT be in default mounts (D-08)")
		}
	}

	// ~/.claude must be ReadOnly=false.
	found := false
	for _, m := range mounts {
		if m.Source == "~/.claude" {
			found = true
			if m.ReadOnly {
				t.Error("~/.claude should be ReadOnly=false")
			}
		}
	}
	if !found {
		t.Error("~/.claude not found in default mounts")
	}

	// ~/.ssh must be ReadOnly=true.
	for _, m := range mounts {
		if m.Source == "~/.ssh" {
			if !m.ReadOnly {
				t.Error("~/.ssh should be ReadOnly=true")
			}
		}
	}
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

	// Must receive 5 default mounts.
	if len(cfg.Mounts) != 5 {
		t.Errorf("expected 5 default mounts, got %d", len(cfg.Mounts))
	}
}
