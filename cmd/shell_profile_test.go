package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// profileCmd builds a throwaway cobra command carrying the --profile flag so
// resolveShellProfile can distinguish an explicit empty value from an unset
// one. When set is true the flag is marked Changed (as cobra would for an
// explicit `--profile ""`).
func profileCmd(t *testing.T, set bool) *cobra.Command {
	t.Helper()
	c := &cobra.Command{}
	var v string
	c.Flags().StringVar(&v, "profile", "", "")
	if set {
		if err := c.Flags().Set("profile", ""); err != nil {
			t.Fatalf("Set profile: %v", err)
		}
	}
	return c
}

func TestResolveShellProfile(t *testing.T) {
	t.Run("no profile is a noop", func(t *testing.T) {
		p, err := resolveShellProfile(profileCmd(t, false), "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p != nil {
			t.Errorf("profile = %+v, want nil", p)
		}
	})

	t.Run("explicit empty profile errors", func(t *testing.T) {
		if _, err := resolveShellProfile(profileCmd(t, true), "", nil); err == nil {
			t.Fatal("want error for explicit --profile \"\", got nil")
		}
	})

	t.Run("share without profile errors", func(t *testing.T) {
		if _, err := resolveShellProfile(profileCmd(t, false), "", []string{"gh"}); err == nil {
			t.Fatal("want error for --share without --profile, got nil")
		}
	})

	t.Run("path traversal rejected", func(t *testing.T) {
		for _, bad := range []string{"..", ".", "../escape", "a/b"} {
			if _, err := resolveShellProfile(profileCmd(t, true), bad, nil); err == nil {
				t.Errorf("profile %q: want error, got nil", bad)
			}
		}
	})

	t.Run("active profile carries name and share", func(t *testing.T) {
		p, err := resolveShellProfile(profileCmd(t, true), "work", []string{"gh"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p == nil || p.Name != "work" {
			t.Fatalf("profile = %+v, want Name=work", p)
		}
		if p.Root() != "~/.toolbox/profiles/work" {
			t.Errorf("Root() = %q, want %q", p.Root(), "~/.toolbox/profiles/work")
		}
		// EffectiveShare keeps the user's tokens and always adds bridge (infra).
		got := p.EffectiveShare()
		if !contains(got, "gh") || !contains(got, "bridge") {
			t.Errorf("EffectiveShare() = %v, want gh and bridge", got)
		}
	})
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
