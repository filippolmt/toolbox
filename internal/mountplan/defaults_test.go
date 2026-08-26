package mountplan

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/bridge"
	"github.com/filippolmt/toolbox/internal/config"
)

func TestDefaults(t *testing.T) {
	mounts := Defaults()

	if len(mounts) != 35 {
		t.Fatalf("expected 35 default mounts, got %d", len(mounts))
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
	// ~/.toolbox/toolbox/state (toolbox-own namespace) must be rw and auto-created.
	assertMount(t, mounts, "~/.toolbox/toolbox/state", false, true)
	// Every cloud / forge CLI must have a rw, auto-created state dir.
	assertMount(t, mounts, "~/.toolbox/gh", false, true)
	assertMount(t, mounts, "~/.toolbox/glab", false, true)
	assertMount(t, mounts, "~/.toolbox/gcloud", false, true)
	assertMount(t, mounts, "~/.toolbox/gws", false, true)
	assertMount(t, mounts, "~/.toolbox/atuin", false, true)
	assertMount(t, mounts, "~/.toolbox/azure", false, true)
	assertMount(t, mounts, "~/.toolbox/oci", false, true)
	assertMount(t, mounts, "~/.toolbox/sonar", false, true)
	assertMount(t, mounts, "~/.toolbox/docker", false, true)
	assertMount(t, mounts, "~/.toolbox/cf/auth", false, true)
	assertMount(t, mounts, "~/.toolbox/cf/config", false, true)
	// Regression guard: cf keeps its credential store under
	// ~/.config/cloudflare/ (config/default.json + profiles/), not the retired
	// ~/.config/.cf/auth.jsonc or ~/.cf/config.toml. The cf-auth mount must
	// target the current path or `cf auth login` wipes on `toolbox stop` and
	// never reaches the other toolboxes.
	assertMountTarget(t, mounts, "~/.toolbox/cf/auth", "/home/toolbox/.config/cloudflare")
	assertMountTarget(t, mounts, "~/.toolbox/cf/config", "/home/toolbox/.config/.cf")
	assertMount(t, mounts, "~/.toolbox/wrangler", false, true)
	assertMount(t, mounts, "~/.toolbox/rtk/config", false, true)
	assertMount(t, mounts, "~/.toolbox/rtk/data", false, true)
	assertMount(t, mounts, "~/.toolbox/kube", false, true)
	assertMount(t, mounts, "~/.toolbox/playwright-cache", false, true)
	// Playwright-cli workspace config: read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/playwright-config", false, true)
	// User-defined hooks dir: read-only, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/startup.d", true, true)
	// Host-provided CA certs dir: read-only (container must not rewrite user
	// certs), create-if-missing (empty folder auto-discoverable).
	assertMount(t, mounts, "~/.toolbox/certs", true, true)
	assertMountTarget(t, mounts, "~/.toolbox/certs", "/etc/toolbox/certs")
	// Per-user npm prefix: read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/npm-global", false, true)
	// bun state (install cache + global packages): read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/bun", false, true)
	// pnpm user-global root (global bin + store): read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/pnpm", false, true)
	// uv tool root (tool venvs + launchers): read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/uv", false, true)
	// Per-user Go workspace (GOPATH): read-write, create-if-missing (GO-05).
	assertMount(t, mounts, "~/.toolbox/go", false, true)
	// herdr config + state (sessions, plugins): read-write, create-if-missing.
	assertMount(t, mounts, "~/.toolbox/herdr/config", false, true)
	assertMount(t, mounts, "~/.toolbox/herdr/state", false, true)

	// ssh + git config follow the host via symlinks, not copies. ssh stays
	// read-only (host private keys); gitconfig is read-write so `git config`
	// inside the container edits the real host file.
	assertSymlink(t, mounts, "~/.toolbox/ssh", "~/.ssh", true)
	assertSymlink(t, mounts, "~/.toolbox/gitconfig", "~/.gitconfig", false)
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

func assertMountTarget(t *testing.T, mounts []config.Mount, src, wantTarget string) {
	t.Helper()
	for _, m := range mounts {
		if m.Source != src {
			continue
		}
		if m.Target != wantTarget {
			t.Errorf("%s: Target = %q, want %q", src, m.Target, wantTarget)
		}
		return
	}
	t.Errorf("mount %s not found in Defaults()", src)
}

func assertSymlink(t *testing.T, mounts []config.Mount, src, wantLink string, wantRO bool) {
	t.Helper()
	for _, m := range mounts {
		if m.Source != src {
			continue
		}
		if m.SymlinkFrom != wantLink {
			t.Errorf("%s: SymlinkFrom = %q, want %q", src, m.SymlinkFrom, wantLink)
		}
		if m.ReadOnly != wantRO {
			t.Errorf("%s: ReadOnly = %v, want %v", src, m.ReadOnly, wantRO)
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

func TestDefaults_IncludesBridge(t *testing.T) {
	// Both the current target and the pre-rename legacy target must be bound
	// from the same host state dir: a pre-rename image hardcodes the legacy
	// path in its shims and must keep working against a renamed host CLI.
	// run/ is the lone RW mount — the daemon's unix socket lives there and
	// connect() on a socket inside a RO mount fails with EROFS.
	want := map[string]struct {
		target   string
		source   string
		readOnly bool
	}{
		"bridge":        {bridge.ContainerDir, "~/" + bridge.HostDir, true},
		"bridge-legacy": {bridge.LegacyContainerDir, "~/" + bridge.HostDir, true},
		"bridge-run":    {bridge.ContainerRunDir, "~/" + bridge.HostRunDir, false},
	}
	for _, m := range defaults() {
		w, ok := want[m.Name]
		if !ok {
			continue
		}
		if m.ReadOnly != w.readOnly {
			t.Errorf("%s ReadOnly = %v, want %v", m.Name, m.ReadOnly, w.readOnly)
		}
		if m.Target != w.target {
			t.Errorf("%s Target = %q, want %q", m.Name, m.Target, w.target)
		}
		if m.Source != w.source {
			t.Errorf("%s Source = %q, want %q", m.Name, m.Source, w.source)
		}
		if !m.CreateIfMissing {
			t.Errorf("%s CreateIfMissing must be true so the mount is resolvable pre-install", m.Name)
		}
		delete(want, m.Name)
	}
	for name := range want {
		t.Errorf("%s mount missing from defaults()", name)
	}
}
