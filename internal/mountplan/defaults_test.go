package mountplan

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

func TestDefaults(t *testing.T) {
	mounts := Defaults()

	if len(mounts) != 26 {
		t.Fatalf("expected 26 default mounts, got %d", len(mounts))
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
	// ~/.toolbox/.codex must be rw and auto-created.
	assertMount(t, mounts, "~/.toolbox/.codex", false, true)
	// ~/.toolbox/state must be rw and auto-created.
	assertMount(t, mounts, "~/.toolbox/state", false, true)
	// Every cloud / forge CLI must have a rw, auto-created state dir.
	assertMount(t, mounts, "~/.toolbox/gh", false, true)
	assertMount(t, mounts, "~/.toolbox/glab", false, true)
	assertMount(t, mounts, "~/.toolbox/gcloud", false, true)
	assertMount(t, mounts, "~/.toolbox/gws", false, true)
	assertMount(t, mounts, "~/.toolbox/atuin", false, true)
	assertMount(t, mounts, "~/.toolbox/azure", false, true)
	assertMount(t, mounts, "~/.toolbox/oci", false, true)
	assertMount(t, mounts, "~/.toolbox/docker", false, true)
	assertMount(t, mounts, "~/.toolbox/cf/auth", false, true)
	assertMount(t, mounts, "~/.toolbox/cf/config", false, true)
	assertMount(t, mounts, "~/.toolbox/wrangler", false, true)
	assertMount(t, mounts, "~/.toolbox/rtk/config", false, true)
	assertMount(t, mounts, "~/.toolbox/rtk/data", false, true)
	assertMount(t, mounts, "~/.toolbox/kube", false, true)
	assertMount(t, mounts, "~/.toolbox/playwright-cache", false, true)
	// Playwright-cli workspace config: read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/playwright-config", false, true)
	// User-defined hooks dir: read-only, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/startup.d", true, true)
	// Per-user npm prefix: read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/npm-global", false, true)
	// bun state (install cache + global packages): read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/bun", false, true)
	// Per-user Go workspace (GOPATH): read-write, create-if-missing (GO-05).
	assertMount(t, mounts, "~/.toolbox/go", false, true)

	// ssh + git config follow the host via symlinks, not copies.
	assertSymlink(t, mounts, "~/.toolbox/ssh", "~/.ssh")
	assertSymlink(t, mounts, "~/.toolbox/gitconfig", "~/.gitconfig")
}

// TestDefaultsHaveNames guards the Name-based merge contract: every
// default mount must carry a non-empty, unique Name so mounts: patches and
// replacements can target it.
func TestDefaultsHaveNames(t *testing.T) {
	mounts := Defaults()
	seen := map[string]struct{}{}
	for _, m := range mounts {
		if m.Name == "" {
			t.Errorf("default mount with target %q has empty Name", m.Target)
			continue
		}
		if _, dup := seen[m.Name]; dup {
			t.Errorf("default mount Name %q is not unique", m.Name)
		}
		seen[m.Name] = struct{}{}
	}
}

func assertMount(t *testing.T, mounts []config.Mount, src string, wantRO, wantCreate bool) {
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
	t.Errorf("mount %s not found in Defaults()", src)
}

func assertSymlink(t *testing.T, mounts []config.Mount, src, wantLink string) {
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
	t.Errorf("mount %s not found in Defaults()", src)
}

func findMount(mounts []config.Mount, name string) *config.Mount {
	for i := range mounts {
		if mounts[i].Name == name {
			return &mounts[i]
		}
	}
	return nil
}
