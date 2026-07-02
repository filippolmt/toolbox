// Package mountplan owns the full pipeline that turns a toolbox Config into
// the slice of Docker bind specs handed to ContainerCreate.
//
// The pipeline is intentionally a single concept exposed at one external
// seam — Plan(cfg, workspace) — even though internally it walks four
// distinct stages (defaults, mounts_root retarget, user-merge, filesystem
// resolve) plus the workspace + DooD-mirror append. Callers and tests both
// cross the same seam.
//
// The legacy split — config.{Default,Merge,ApplyMountsRoot}Mounts +
// internal/mount.ResolveMounts + lifecycle.Shell appending workspace binds
// — fragmented one concept across two packages and three call sites. This
// package consolidates them and keeps internal/config free of filesystem
// side-effects.
package mountplan

import (
	"os"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/proximo"
)

// Bind is the typed representation of a Docker bind spec. lifecycle.Shell
// stringifies these via Bind.String at the daemon edge so internal callers
// and tests deal with structured fields instead of "src:target:mode" strings.
type Bind struct {
	Source string
	Target string
	Mode   string // "rw" | "ro"
}

// String returns the Docker SDK bind spec format ("src:target:mode") used by
// container.HostConfig.Binds.
func (b Bind) String() string { return b.Source + ":" + b.Target + ":" + b.Mode }

// Result is the output of Plan: the full bind set to hand to ContainerCreate
// plus the WorkingDir (set to the host-path mirror when safe, falling back
// to WorkspaceTarget) and any per-mount soft skips for the caller to surface.
type Result struct {
	Binds      []Bind
	Warnings   []string
	WorkingDir string
}

// Plan walks the full mount pipeline for cfg and returns the bind set + the
// shell WorkingDir for ContainerCreate.
//
// Hard fails when the user's home directory cannot be resolved (every
// ~/.toolbox/<x> default would silently disappear, leaving the container
// without credential mounts) or when Merge rejects the user list (typo on a
// name-only patch, anonymous mount with empty source, …). Per-mount issues
// (missing source without a create rule, missing symlink target, …) stay
// soft skips surfaced via Warnings.
func Plan(cfg *config.Config, workspace string, profile *Profile) (Result, error) {
	merged, err := Merge(cfg, profile)
	if err != nil {
		return Result{}, err
	}

	home, err := fsx.Home()
	if err != nil {
		return Result{}, err
	}

	binds, warnings := resolveAll(merged, home)

	binds = append(binds, Bind{Source: workspace, Target: WorkspaceTarget, Mode: "rw"})

	workingDir := WorkspaceTarget
	if mirror, ok := WorkspaceMirrorPath(workspace); ok {
		binds = append(binds, Bind{Source: workspace, Target: mirror, Mode: "rw"})
		workingDir = mirror
	}

	return Result{Binds: binds, Warnings: warnings, WorkingDir: workingDir}, nil
}

// Merge returns the post-merge mount list (defaults retargeted by
// MountsRoot, then patched/replaced/appended/disabled by cfg.Mounts).
// Pure: no filesystem side-effects, no workspace bind. Used by tests
// asserting merge contracts and by callers that want to inspect the
// resolved set without materialising sources.
func Merge(cfg *config.Config, profile *Profile) ([]config.Mount, error) {
	// A profile retargets to its own root and wins over a config-level
	// mounts_root for this invocation; without one, the config value applies.
	root := cfg.MountsRoot
	var shared []string
	if profile != nil {
		root = profile.Root()
		shared = profile.EffectiveShare()
	}
	if err := config.ValidateMountsRoot(root); err != nil {
		return nil, err
	}
	if err := validateShare(defaults(), shared); err != nil {
		return nil, err
	}
	// HOME for inherit_host_auth's pre-stat. UserHomeDir failure leaves home
	// empty, in which case applyInheritHostAuth treats ~/ paths as-is and
	// os.Stat reports them missing — surfaces the misconfiguration loudly.
	home, _ := os.UserHomeDir()
	base := applyMountsRoot(defaults(), root, shared)
	base, err := applyInheritHostAuth(base, cfg.InheritHostAuth, home)
	if err != nil {
		return nil, err
	}
	if cfg.Bridge != nil && !*cfg.Bridge {
		base = dropMountByName(base, "bridge")
		base = dropMountByName(base, "bridge-legacy")
		base = dropMountByName(base, "bridge-run")
	}
	// proximo CA bind is injected here (not in defaults()) because its source
	// is host-specific and only relevant when `proximo: true`. resolveAll
	// soft-skips it with a warning when proximo is not installed.
	if m, ok := proximo.CAMount(cfg); ok {
		base = append(base, m)
	}
	return mergeMounts(base, cfg.Mounts)
}

// dropMountByName returns a copy of base with the entry whose Name matches
// removed. Used to honour top-level feature toggles (e.g. bridge:
// false) at the mount-resolution seam — feature flags driven by code do not
// need to round-trip through the user `mounts:` list.
func dropMountByName(base []config.Mount, name string) []config.Mount {
	out := make([]config.Mount, 0, len(base))
	for _, m := range base {
		if m.Name == name {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Defaults returns the canonical default mount set. Exported so build tests
// can cross-check the Dockerfile against the same source of truth used at
// runtime; runtime callers should go through Plan instead.
func Defaults() []config.Mount { return defaults() }
