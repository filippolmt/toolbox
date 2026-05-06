package config

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/spf13/viper"

	"github.com/filippolmt/toolbox/internal/catalog"
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
	// root rewrite by mountplan.Merge, so a single override remains
	// possible.
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
// See mountplan.Merge for the full contract.
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

// HomeMountParents is the fixed in-image HOME under which runtime-user-writable
// subdirs live. Mount targets outside this prefix are not the subject of the
// "Docker auto-creates parents as root:root" bug that mountplan.ParentDirs
// guards against.
const HomeMountParents = "/home/toolbox/"

// SupportedShells is the canonical list of values accepted by the `shell`
// key in ~/.toolbox.yaml. Exposed so tests and error messages can consume a
// single source of truth (D-14).
var SupportedShells = []string{"zsh", "bash"}

// ValidateShell returns nil when s is a supported shell, or an error listing
// the accepted values (D-15). Used by Load() and (defensively) by the
// container shell resolver in a later plan.
func ValidateShell(s string) error {
	if slices.Contains(SupportedShells, s) {
		return nil
	}
	return fmt.Errorf("unsupported shell %q: must be one of %s",
		s, strings.Join(SupportedShells, ", "))
}

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

// Load reads the configuration from Viper and applies defaults.
//
// cfg.Mounts is left as the user declared it (or empty). The full mount
// pipeline — defaults, mounts_root retarget, user-merge, filesystem
// resolve, workspace bind — lives behind mountplan.Plan, which is the
// single seam runtime callers and tests cross.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Validate the global mounts_root override at the edge: mountplan.Merge
	// re-validates defensively, but failing here keeps bad config visible at
	// CLI startup instead of at first shell.
	if err := ValidateMountsRoot(cfg.MountsRoot); err != nil {
		return nil, err
	}

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
	for _, k := range catalog.Keys() {
		if _, ok := cfg.Tools[k]; !ok {
			cfg.Tools[k] = true
		}
	}

	return cfg, nil
}
