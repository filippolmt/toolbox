package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Config is the top-level toolbox configuration.
//
// Image selection is no longer controlled by explicit image.name/image.tag
// fields — it is derived from the Tools map by build.ResolveImage. If the
// user needs a custom registry image, the escape hatch is to point at a
// locally-built one via `toolbox build` + manual `docker tag`.
type Config struct {
	Mounts []Mount               `mapstructure:"mounts"`
	Tools  map[string]bool       `mapstructure:"tools"`
	Shells map[string]NamedShell `mapstructure:"shells"`
	Shell  string                `mapstructure:"shell"`
	// MountsRoot retargets every default mount whose Source lives under
	// ~/.toolbox/ to the given prefix. Useful when the user wants every
	// toolbox-managed credential / state dir to live somewhere other than
	// the host home (e.g. an encrypted volume). Empty = use ~/.toolbox/ as
	// before. Per-mount patches in mounts: still win — applied after the
	// root rewrite by mountplan.Merge, so a single override remains
	// possible.
	MountsRoot string `mapstructure:"mounts_root"`
	// SDD opts the workspace into one or more Spec-Driven-Development skill
	// packs (gsd, bmad, openspec, ...) installed repo-locally on every
	// `toolbox shell`. Each `sdd.<key>: true` flag toggles the matching
	// internal/sdd.Skill entry: sessionplan emits TOOLBOX_SDD_ENABLED plus
	// a per-skill spec env var; entrypoint.sh loops them and runs the
	// pinned installer in /workspace.
	//
	// Lives OUTSIDE the Tools map on purpose: catalog.WriteCanonical feeds
	// the local-image hash, so a top-level field is guaranteed hash-neutral
	// — flipping an SDD flag never forces a rebuild.
	SDD map[string]bool `mapstructure:"sdd"`
	// BrowserBridge toggles the host-side ~/.toolbox/browser RO mount in the
	// container and gates the `toolbox browser-bridge install` command. When
	// false, the mount is omitted and the install command refuses. Default
	// true. Lives outside Tools{} so flipping it stays hash-neutral (mirrors
	// the SDD rationale above).
	BrowserBridge *bool `mapstructure:"browser_bridge"`
}

// NamedShell is a shell workspace entry configured under shells:<name>.
type NamedShell struct {
	Path string `mapstructure:"path"`
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
	if filepath.IsAbs(s) {
		return nil
	}
	return fmt.Errorf("mounts_root %q must be absolute or start with ~/", s)
}

// Load reads the configuration via config.Plan, walking up from CWD and
// honouring the explicit --config flag when set elsewhere in cmd/.
//
// Deprecated: Load is a thin compatibility wrapper around Plan, retained for
// the duration of one release cycle. Subcommand callers (cmd/build.go,
// cmd/shell.go, cmd/stop.go) migrate to consuming the *Config produced by
// initConfig directly during Phase 09 (Session Plan); after the Phase 09 /
// Phase 10 sweep this wrapper is deleted.
//
// New code MUST call Plan(searchFrom, explicitOverride) directly. Tests for
// the byte-merge logic should target Merge — see internal/config/merge_test.go.
func Load() (*Config, error) {
	// os.Getwd() error is intentionally ignored: empty cwd resolves to "."
	// inside Plan via filepath.Clean(""), which still triggers the walk-up
	// search from the current process directory. Mirrors cmd/root.go::initConfig.
	cwd, _ := os.Getwd()
	return Plan(cwd, "")
}
