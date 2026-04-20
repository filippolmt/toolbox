package config

import (
	"path"
	"sort"
	"strings"

	"github.com/spf13/viper"
)

// Config is the top-level toolbox configuration.
//
// Image selection is no longer controlled by explicit image.name/image.tag
// fields — it is derived from the Tools map by build.ResolveImage. If the
// user needs a custom registry image, the escape hatch is to point at a
// locally-built one via `toolbox build` + manual `docker tag`.
type Config struct {
	Mounts []Mount         `mapstructure:"mounts"`
	Tools  map[string]bool `mapstructure:"tools"`
}

// Mount represents a host -> container volume bind.
type Mount struct {
	Source   string `mapstructure:"source"`
	Target   string `mapstructure:"target"`
	ReadOnly bool   `mapstructure:"readonly"`
	// CreateIfMissing creates the source directory (mode 0700) when absent,
	// instead of skipping the mount. Used for toolbox-managed state dirs.
	CreateIfMissing bool `mapstructure:"create_if_missing"`
	// SymlinkFrom is a host path the Source is symlinked to when the Source
	// does not exist yet. Used to keep toolbox state in sync with host files
	// (e.g. ~/.toolbox/ssh -> ~/.ssh). If SymlinkFrom itself is missing, the
	// mount is skipped with a warning.
	SymlinkFrom string `mapstructure:"symlink_from"`
}

// DefaultMounts returns the default mount set (D-07).
// ~/.secrets is intentionally NOT included (D-08).
//
// Every auth/state path is addressed through ~/.toolbox/ on the host:
//   - Claude / state / gh / glab live there as real dirs (isolated from
//     the host's own ~/.claude, ~/.config/gh, etc.).
//   - ssh / gitconfig / gitconfig-dbm are symlinks to the host's versions,
//     so `ssh-keygen` and `git config` stay in sync with the container.
//
// If a symlink target is missing on the host, that mount is skipped with
// a warning; the user can add it later without re-running any command.
func DefaultMounts() []Mount {
	return []Mount{
		// Claude Code config + credentials.
		{Source: "~/.toolbox/.claude", Target: "/home/toolbox/.claude", ReadOnly: false, CreateIfMissing: true},
		// Bash history and other shell state, shared across every toolbox shell.
		{Source: "~/.toolbox/state", Target: "/home/toolbox/.toolbox-state", ReadOnly: false, CreateIfMissing: true},
		// SSH keys and git config follow the host via symlinks under ~/.toolbox/,
		// so changes made with `ssh-keygen` / `git config` on the host are
		// immediately visible inside the container (and vice versa).
		{Source: "~/.toolbox/ssh", Target: "/home/toolbox/.ssh", ReadOnly: true, SymlinkFrom: "~/.ssh"},
		{Source: "~/.toolbox/gitconfig", Target: "/home/toolbox/.gitconfig", ReadOnly: true, SymlinkFrom: "~/.gitconfig"},
		{Source: "~/.toolbox/gitconfig-dbm", Target: "/home/toolbox/.gitconfig-dbm", ReadOnly: true, SymlinkFrom: "~/.gitconfig-dbm"},
		// GitHub CLI auth — populated by `gh auth login` inside the container.
		{Source: "~/.toolbox/gh", Target: "/home/toolbox/.config/gh", ReadOnly: false, CreateIfMissing: true},
		// GitLab CLI auth — populated by `glab auth login` inside the container.
		{Source: "~/.toolbox/glab", Target: "/home/toolbox/.config/glab-cli", ReadOnly: false, CreateIfMissing: true},
		// gcloud auth + config — populated by `gcloud auth login` inside the container.
		{Source: "~/.toolbox/gcloud", Target: "/home/toolbox/.config/gcloud", ReadOnly: false, CreateIfMissing: true},
		// Azure CLI auth + config — populated by `az login` inside the container.
		{Source: "~/.toolbox/azure", Target: "/home/toolbox/.azure", ReadOnly: false, CreateIfMissing: true},
		// Oracle OCI CLI auth + config — populated by `oci setup config` inside the container.
		{Source: "~/.toolbox/oci", Target: "/home/toolbox/.oci", ReadOnly: false, CreateIfMissing: true},
		// Playwright browser cache — populated by `playwright install`; keeps the
		// ~500MB of Chromium/Firefox/Webkit binaries across container restarts.
		{Source: "~/.toolbox/playwright-cache", Target: "/home/toolbox/.cache/ms-playwright", ReadOnly: false, CreateIfMissing: true},
		// User-defined startup hooks. Any *.sh file here is executed by the
		// entrypoint before handing control to the shell — read-only to prevent
		// in-container tampering; edits happen on the host.
		{Source: "~/.toolbox/startup.d", Target: "/home/toolbox/.toolbox-startup.d", ReadOnly: true, CreateIfMissing: true},
		// Per-user npm global prefix. Keeps runtime `npm install -g` writable
		// without root and persistent across container recreations. The prefix
		// itself is wired via NPM_CONFIG_PREFIX + PATH in the Dockerfile.
		{Source: "~/.toolbox/npm-global", Target: "/home/toolbox/.npm-global", ReadOnly: false, CreateIfMissing: true},
		// Docker socket for DinD-free container access.
		{Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: false},
	}
}

// HomeMountParents is the fixed in-image HOME under which runtime-user-writable
// subdirs live. Mount targets outside this prefix are not the subject of the
// "Docker auto-creates parents as root:root" bug that MountParentDirs guards
// against.
const HomeMountParents = "/home/toolbox/"

// MountParentDirs returns the distinct parent directories of mount targets
// under /home/toolbox/, excluding /home/toolbox itself. These are the dirs
// Docker would otherwise auto-create as root:root 0755 at runtime (as the
// parent of a bind mount), blocking the non-root runtime user from writing
// sibling subdirs — e.g. helm under ~/.config, starship under ~/.cache. The
// image must pre-create them (Dockerfile Layer 21). A Go test cross-checks
// the Dockerfile against this function so a new default mount can't
// silently regress the fix.
func MountParentDirs(mounts []Mount) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range mounts {
		if !strings.HasPrefix(m.Target, HomeMountParents) {
			continue
		}
		parent := path.Dir(m.Target)
		if parent == strings.TrimSuffix(HomeMountParents, "/") {
			continue
		}
		if _, ok := seen[parent]; ok {
			continue
		}
		seen[parent] = struct{}{}
		out = append(out, parent)
	}
	sort.Strings(out)
	return out
}

// Load reads the configuration from Viper and applies defaults.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Fall back to default mounts if none configured (D-07).
	if len(cfg.Mounts) == 0 {
		cfg.Mounts = DefaultMounts()
	}

	// Fill in defaults for every known tool so downstream callers (hashing,
	// build-arg translation) don't need to branch on missing keys.
	if cfg.Tools == nil {
		cfg.Tools = map[string]bool{}
	}
	for _, k := range KnownTools {
		if _, ok := cfg.Tools[k]; !ok {
			cfg.Tools[k] = true
		}
	}

	return cfg, nil
}
