package cmd

import (
	"slices"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

func TestApplyShellProfile(t *testing.T) {
	t.Run("no profile is a noop", func(t *testing.T) {
		cfg = &config.Config{}
		t.Cleanup(func() { cfg = nil })

		share, err := applyShellProfile("", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if share != nil {
			t.Errorf("share = %v, want nil", share)
		}
		if cfg.MountsRoot != "" {
			t.Errorf("MountsRoot mutated without profile: %q", cfg.MountsRoot)
		}
	})

	t.Run("share without profile errors", func(t *testing.T) {
		cfg = &config.Config{}
		t.Cleanup(func() { cfg = nil })

		if _, err := applyShellProfile("", []string{"gh"}); err == nil {
			t.Fatal("want error for --share without --profile, got nil")
		}
	})

	t.Run("path traversal rejected without touching cfg", func(t *testing.T) {
		cfg = &config.Config{}
		t.Cleanup(func() { cfg = nil })

		for _, bad := range []string{"..", ".", "../escape", "a/b"} {
			if _, err := applyShellProfile(bad, nil); err == nil {
				t.Errorf("profile %q: want error, got nil", bad)
			}
			if cfg.MountsRoot != "" {
				t.Errorf("profile %q: MountsRoot mutated on error: %q", bad, cfg.MountsRoot)
			}
		}
	})

	t.Run("active profile sets root and auto-shares bridge", func(t *testing.T) {
		cfg = &config.Config{}
		t.Cleanup(func() { cfg = nil })

		share, err := applyShellProfile("work", []string{"gh"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MountsRoot != "~/.toolbox/profiles/work" {
			t.Errorf("MountsRoot = %q, want %q", cfg.MountsRoot, "~/.toolbox/profiles/work")
		}
		if !slices.Contains(share, "gh") || !slices.Contains(share, "bridge") {
			t.Errorf("share = %v, want to contain gh and bridge", share)
		}
	})

	t.Run("profile overrides a configured mounts_root", func(t *testing.T) {
		cfg = &config.Config{MountsRoot: "~/other-toolbox"}
		t.Cleanup(func() { cfg = nil })

		if _, err := applyShellProfile("work", nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MountsRoot != "~/.toolbox/profiles/work" {
			t.Errorf("MountsRoot = %q, want profile root to win over config", cfg.MountsRoot)
		}
	})
}
