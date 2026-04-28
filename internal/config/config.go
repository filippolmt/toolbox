package config

import (
	"fmt"
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
	Shell  string          `mapstructure:"shell"`
	// MountsRoot retargets every default mount whose Source lives under
	// ~/.toolbox/ to the given prefix. Useful when the user wants every
	// toolbox-managed credential / state dir to live somewhere other than
	// the host home (e.g. an encrypted volume). Empty = use ~/.toolbox/ as
	// before. Per-mount patches in mounts: still win — applied after the
	// root rewrite, so a single override remains possible.
	MountsRoot string `mapstructure:"mounts_root"`
}

// Mount represents a host -> container volume bind.
//
// Inside the user's mounts: list, an entry is interpreted as:
//   - a *patch* of a default when Name matches a default and Target is empty
//     (only non-zero fields override the default; useful for retargeting a
//     single Source);
//   - a *replace* of a default when Name matches a default and Target is set
//     (the entire default entry is swapped for the user's);
//   - an *addition* otherwise (appended after the defaults).
//
// See MergeMounts for the full contract.
type Mount struct {
	// Name is a stable alias used by patch/replace targeting. Default mounts
	// populate it; user-declared mounts set it to override a default by name.
	Name     string `mapstructure:"name"`
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
	// Disabled removes the mount from the resolved set. Used in patches to
	// opt out of a default (e.g. drop the Docker socket) without forcing a
	// full mounts: redeclaration.
	Disabled bool `mapstructure:"disabled"`
}

// DefaultMounts returns the default mount set (D-07).
// ~/.secrets is intentionally NOT included (D-08).
//
// Every auth/state path is addressed through ~/.toolbox/ on the host:
//   - Claude / state / gh / glab live there as real dirs (isolated from
//     the host's own ~/.claude, ~/.config/gh, etc.).
//   - ssh / gitconfig are symlinks to the host's versions, so `ssh-keygen`
//     and `git config` stay in sync with the container.
//
// If a symlink target is missing on the host, that mount is skipped with
// a warning; the user can add it later without re-running any command.
func DefaultMounts() []Mount {
	return []Mount{
		// Claude Code config + credentials.
		{Name: "claude", Source: "~/.toolbox/.claude", Target: "/home/toolbox/.claude", ReadOnly: false, CreateIfMissing: true},
		// OpenAI Codex CLI auth + config — populated by `codex login` inside the container.
		{Name: "codex", Source: "~/.toolbox/.codex", Target: "/home/toolbox/.codex", ReadOnly: false, CreateIfMissing: true},
		// Bash history and other shell state, shared across every toolbox shell.
		{Name: "state", Source: "~/.toolbox/state", Target: "/home/toolbox/.toolbox-state", ReadOnly: false, CreateIfMissing: true},
		// SSH keys and git config follow the host via symlinks under ~/.toolbox/,
		// so changes made with `ssh-keygen` / `git config` on the host are
		// immediately visible inside the container (and vice versa).
		{Name: "ssh", Source: "~/.toolbox/ssh", Target: "/home/toolbox/.ssh", ReadOnly: true, SymlinkFrom: "~/.ssh"},
		{Name: "gitconfig", Source: "~/.toolbox/gitconfig", Target: "/home/toolbox/.gitconfig", ReadOnly: true, SymlinkFrom: "~/.gitconfig"},
		// GitHub CLI auth — populated by `gh auth login` inside the container.
		{Name: "gh", Source: "~/.toolbox/gh", Target: "/home/toolbox/.config/gh", ReadOnly: false, CreateIfMissing: true},
		// GitLab CLI auth — populated by `glab auth login` inside the container.
		{Name: "glab", Source: "~/.toolbox/glab", Target: "/home/toolbox/.config/glab-cli", ReadOnly: false, CreateIfMissing: true},
		// gcloud auth + config — populated by `gcloud auth login` inside the container.
		{Name: "gcloud", Source: "~/.toolbox/gcloud", Target: "/home/toolbox/.config/gcloud", ReadOnly: false, CreateIfMissing: true},
		// Google Workspace CLI auth + config — populated by `gws auth login` inside the container.
		// Default config dir is ~/.config/gws (overridable via GOOGLE_WORKSPACE_CLI_CONFIG_DIR).
		// The image sets GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file so the encryption key lands
		// in this bind-mount instead of an OS keyring (unavailable inside the container).
		{Name: "gws", Source: "~/.toolbox/gws", Target: "/home/toolbox/.config/gws", ReadOnly: false, CreateIfMissing: true},
		// Azure CLI auth + config — populated by `az login` inside the container.
		{Name: "azure", Source: "~/.toolbox/azure", Target: "/home/toolbox/.azure", ReadOnly: false, CreateIfMissing: true},
		// Oracle OCI CLI auth + config — populated by `oci setup config` inside the container.
		{Name: "oci", Source: "~/.toolbox/oci", Target: "/home/toolbox/.oci", ReadOnly: false, CreateIfMissing: true},
		// rtk token-savings history — populated by `rtk` proxy invocations and read
		// by `rtk gain`. Default state dir is ~/.config/rtk; bind-mounting it keeps
		// the analytics database across container recreations.
		{Name: "rtk", Source: "~/.toolbox/rtk", Target: "/home/toolbox/.config/rtk", ReadOnly: false, CreateIfMissing: true},
		// kubeconfig — populated by `gcloud container clusters get-credentials`,
		// `aws eks update-kubeconfig`, manual edits, etc. Persists across the
		// auto-remove-on-exit container lifecycle so cluster context survives
		// a reopened shell.
		{Name: "kube", Source: "~/.toolbox/kube", Target: "/home/toolbox/.kube", ReadOnly: false, CreateIfMissing: true},
		// Playwright browser cache — populated by `playwright install`; keeps the
		// ~500MB of Chromium/Firefox/Webkit binaries across container restarts.
		{Name: "playwright-cache", Source: "~/.toolbox/playwright-cache", Target: "/home/toolbox/.cache/ms-playwright", ReadOnly: false, CreateIfMissing: true},
		// User-defined startup hooks. Any *.sh file here is executed by the
		// entrypoint before handing control to the shell — read-only to prevent
		// in-container tampering; edits happen on the host.
		{Name: "startup.d", Source: "~/.toolbox/startup.d", Target: "/home/toolbox/.toolbox-startup.d", ReadOnly: true, CreateIfMissing: true},
		// Per-user npm global prefix. Keeps runtime `npm install -g` writable
		// without root and persistent across container recreations. The prefix
		// itself is wired via NPM_CONFIG_PREFIX + PATH in the Dockerfile.
		{Name: "npm-global", Source: "~/.toolbox/npm-global", Target: "/home/toolbox/.npm-global", ReadOnly: false, CreateIfMissing: true},
		// Per-user Go workspace (GOPATH). Go's default `$HOME/go` resolves
		// to /home/toolbox/go inside the container; this bind-mount persists
		// the module cache (`pkg/mod`) and `go install` binaries (`bin/`)
		// across container recreations. Unconditional — matches the
		// playwright-cache / npm-global pattern (D-11). No GOROOT/GOPATH
		// ENV required (D-08 / D-09): Go auto-detects GOROOT from the
		// `/usr/local/go/bin/go` exec path and defaults GOPATH to $HOME/go.
		{Name: "go", Source: "~/.toolbox/go", Target: "/home/toolbox/go", ReadOnly: false, CreateIfMissing: true},
		// Docker socket for DinD-free container access.
		{Name: "docker-sock", Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: false},
	}
}

// HomeMountParents is the fixed in-image HOME under which runtime-user-writable
// subdirs live. Mount targets outside this prefix are not the subject of the
// "Docker auto-creates parents as root:root" bug that MountParentDirs guards
// against.
const HomeMountParents = "/home/toolbox/"

// SupportedShells is the canonical list of values accepted by the `shell`
// key in ~/.toolbox.yaml. Exposed so tests and error messages can consume a
// single source of truth (D-14).
var SupportedShells = []string{"zsh", "bash"}

// ValidateShell returns nil when s is a supported shell, or an error listing
// the accepted values (D-15). Used by Load() and (defensively) by the
// container shell resolver in a later plan.
func ValidateShell(s string) error {
	for _, sh := range SupportedShells {
		if s == sh {
			return nil
		}
	}
	return fmt.Errorf("unsupported shell %q: must be one of %s",
		s, strings.Join(SupportedShells, ", "))
}

// mountsRootPrefix is the source-path prefix that ApplyMountsRoot rewrites.
// Every default mount whose Source begins with this prefix is retargeted
// when the user sets mounts_root in their config.
const mountsRootPrefix = "~/.toolbox/"

// ValidateMountsRoot rejects mounts_root values that would silently bind
// the wrong path. Empty is allowed (no override). The value must be either
// absolute (/path) or strictly home-relative with a sub-path (~/sub) so
// the resolver can expand it deterministically. Bare "~" is refused on
// purpose: it would rewrite ~/.toolbox/<x> to ~/<x>, dropping the
// isolation namespace and writing toolbox state straight onto the host
// home (~/.claude, ~/.gitconfig, …) — the exact leak the default mount
// set is designed to prevent. Relative paths are refused too: they would
// resolve against the CWD at toolbox-shell invocation, which is almost
// never what the user wants for a global override.
func ValidateMountsRoot(s string) error {
	if s == "" {
		return nil
	}
	if s == "~" {
		return fmt.Errorf("mounts_root %q is too broad: it would write toolbox state directly under the host home, defeating credential isolation; use a sub-path (e.g. ~/toolbox-state) or an absolute path", s)
	}
	if strings.HasPrefix(s, "~/") {
		return nil
	}
	if path.IsAbs(s) {
		return nil
	}
	return fmt.Errorf("mounts_root %q must be absolute or start with ~/", s)
}

// ApplyMountsRoot returns a copy of base with every Source under
// ~/.toolbox/ rewritten to live under root instead. Mounts whose Source
// is outside that prefix (e.g. /var/run/docker.sock) are left untouched,
// as is SymlinkFrom (which references the real host path, not the
// toolbox-managed mirror). Empty root returns base unchanged.
func ApplyMountsRoot(base []Mount, root string) []Mount {
	if root == "" {
		return base
	}
	// Strip a trailing slash so joining with the rest gives a clean path.
	trimmed := strings.TrimSuffix(root, "/")
	out := make([]Mount, len(base))
	copy(out, base)
	for i := range out {
		if !strings.HasPrefix(out[i].Source, mountsRootPrefix) {
			continue
		}
		rest := strings.TrimPrefix(out[i].Source, mountsRootPrefix)
		out[i].Source = trimmed + "/" + rest
	}
	return out
}

// MergeMounts combines a base mount set (typically DefaultMounts()) with a
// user-declared list, applying these rules per user entry:
//
//   - Name set, Target empty → patch the matching base entry. Only non-zero
//     user fields override the base; bool fields can flip false→true via the
//     patch but cannot flip true→false (mapstructure can't distinguish "not
//     set" from false). Use the replace form if you need that.
//   - Name set, Target set → if Name matches a base entry, replace it
//     entirely; otherwise append.
//   - Name empty → append (anonymous mount).
//
// After merging, any entry with Disabled=true is removed from the result so
// users can opt out of a default (e.g. docker-sock) without redeclaring the
// rest of the list. Patches referencing an unknown Name fail loudly.
func MergeMounts(base, user []Mount) ([]Mount, error) {
	out := make([]Mount, len(base))
	copy(out, base)
	nameIdx := map[string]int{}
	for i, m := range out {
		if m.Name != "" {
			nameIdx[m.Name] = i
		}
	}

	var unknown []string
	for _, u := range user {
		switch {
		case u.Name != "" && u.Target == "":
			idx, ok := nameIdx[u.Name]
			if !ok {
				unknown = append(unknown, u.Name)
				continue
			}
			if u.Source != "" {
				out[idx].Source = u.Source
			}
			if u.SymlinkFrom != "" {
				out[idx].SymlinkFrom = u.SymlinkFrom
			}
			if u.ReadOnly {
				out[idx].ReadOnly = true
			}
			if u.CreateIfMissing {
				out[idx].CreateIfMissing = true
			}
			if u.Disabled {
				out[idx].Disabled = true
			}
		case u.Name != "":
			if u.Source == "" {
				return nil, fmt.Errorf("mounts[%q]: source must not be empty when target is set", u.Name)
			}
			if idx, ok := nameIdx[u.Name]; ok {
				out[idx] = u
			} else {
				nameIdx[u.Name] = len(out)
				out = append(out, u)
			}
		default:
			if u.Source == "" {
				return nil, fmt.Errorf("mounts: anonymous mount (target %q) must declare a non-empty source", u.Target)
			}
			out = append(out, u)
		}
	}

	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("mounts: patch references unknown mount name(s): %s", strings.Join(unknown, ", "))
	}

	final := make([]Mount, 0, len(out))
	for _, m := range out {
		if m.Disabled {
			continue
		}
		final = append(final, m)
	}
	return final, nil
}

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

	// Validate and apply the global mounts_root override (if any) before
	// merging user patches: this lets a user redirect every ~/.toolbox/<x>
	// default to <mounts_root>/<x> in a single line, and still override
	// individual entries via mounts: patches afterwards.
	if err := ValidateMountsRoot(cfg.MountsRoot); err != nil {
		return nil, err
	}
	defaults := ApplyMountsRoot(DefaultMounts(), cfg.MountsRoot)

	// Merge user-declared mounts on top of the defaults: by-Name patches /
	// replacements / disables, plus appended additions. See MergeMounts.
	merged, err := MergeMounts(defaults, cfg.Mounts)
	if err != nil {
		return nil, err
	}
	cfg.Mounts = merged

	// Shell default + validation (D-16). Missing or empty => "zsh". Any other
	// non-supported value fails Load() before any downstream consumer runs.
	if cfg.Shell == "" {
		cfg.Shell = "zsh"
	}
	if err := ValidateShell(cfg.Shell); err != nil {
		return nil, err
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
