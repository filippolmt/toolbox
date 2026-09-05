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
	"path/filepath"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/proximo"
)

// Bind is the typed representation of a Docker bind spec. lifecycle.Shell
// stringifies these via Bind.String at the daemon edge so internal callers
// and tests deal with structured fields instead of "src:target:mode" strings.
type Bind struct {
	// Source is a host path for every mount the pipeline resolves, and a
	// Docker named volume for the session inputs appended after resolveAll
	// (peerSocketBind). Docker reads the two off the same field — a spec whose
	// source is not absolute is taken as a volume name — so nothing downstream
	// branches on which it is; the difference only matters to code that would
	// stat, create or retarget the source, and no such code may touch a volume.
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
	// StateDir is the host path of the toolbox state mount for this plan, or
	// "" when nothing mounts there any more — StateDirPath's answer, read off
	// the merge this plan already performed. It is here rather than re-derived
	// by the caller because re-deriving means merging a second time, and the
	// merge resolves the proximo gate: a session that asked twice paid two
	// `proximo config ca-path` spawns to describe one set of mounts.
	StateDir string
}

// WorktreeGitDirMountName is the Name carried by the bind that PlanInput.GitDir
// produces. Named like every other mount so warnings and `toolbox mounts`
// output can tell the reader where the entry came from.
const WorktreeGitDirMountName = "worktree-gitdir"

// PlanInput is the full set of inputs to Plan. A struct rather than a
// positional list: the session-shaped inputs (which workspace, whose profile,
// which extra host path) only grow, and at four arguments the call site stops
// saying which is which.
type PlanInput struct {
	Cfg       *config.Config
	Workspace string
	Profile   *Profile
	// GitDir is the main repository's .git directory for a `toolbox worktree`
	// session, empty for every other session. A linked worktree's .git is a
	// pointer file into the main repo, so git only resolves in-container when
	// that directory is bound at its host path. It joins the merged mount list
	// and goes through resolveAll like every other mount, so a missing source
	// is a soft skip with a warning instead of a bind ContainerCreate rejects.
	GitDir string
	// Peer opts the session into cross-container Claude Code peer messaging.
	// The only mount-side consequence is the shared inbox-socket volume
	// (see peerSocketBind); the PID namespace half, and the volume's
	// one-time ownership init, live in sessionplan + internal/container.
	Peer bool
	// Host is the resolved host this plan is for. Every ~/.toolbox default
	// hangs off Host.Home, so the pipeline reads it here instead of the
	// process — the plan says which home it planned against, and a test names
	// one rather than rewriting $HOME for the whole binary.
	Host fsx.Host
	// Proximo is the resolved Proximo Availability Gate for this invocation:
	// the decision plus the host CA path it was decided against, derived once
	// by the caller (proximo.Resolve) and read here rather than re-derived —
	// the derivation pays a subprocess spawn, and the same gate also answers
	// the session's env and its create-edge discovery flag. The zero value is
	// a session with proximo off, so a plan that declares nothing binds no CA.
	Proximo proximo.Gate
}

// Plan walks the full mount pipeline for in.Cfg and returns the bind set + the
// shell WorkingDir for ContainerCreate.
//
// Hard fails when the user's home directory cannot be resolved (every
// ~/.toolbox/<x> default would silently disappear, leaving the container
// without credential mounts) or when Merge rejects the user list (typo on a
// name-only patch, anonymous mount with empty source, …). Per-mount issues
// (missing source without a create rule, missing symlink target, …) stay
// soft skips surfaced via Warnings.
func Plan(in PlanInput) (Result, error) {
	// merge, not Merge: a planned session brings its own resolved proximo
	// gate, so the pipeline reads the decision instead of paying for it again.
	merged, err := merge(in.Host, in.Cfg, in.Profile, in.Proximo)
	if err != nil {
		return Result{}, err
	}

	// After Merge, which is where the home resolution used to sit: a config
	// with both a rejected mount list and an unresolvable home reports the
	// merge error, the way it always did.
	if err := in.Host.Validate(); err != nil {
		return Result{}, err
	}

	// Appended post-Merge: session inputs, not configurable mounts — no
	// `mounts:` patch should be able to retarget or disable them.
	if in.GitDir != "" {
		merged = append(merged, config.Mount{
			Name:   WorktreeGitDirMountName,
			Source: in.GitDir,
			Target: in.GitDir,
		})
	}

	binds, warnings := resolveAll(merged, in.Host.Home)
	warnings = append(profileHostSharedWarnings(merged, in.Profile), warnings...)

	// The peer socket mount joins the set after resolveAll: its source is a
	// named volume, so there is no host path to expand, create or stat, and
	// nothing that could turn it into a soft skip.
	if in.Peer {
		binds = append(binds, peerSocketBind())
	}

	binds = append(binds, Bind{Source: in.Workspace, Target: WorkspaceTarget, Mode: "rw"})

	workingDir := WorkspaceTarget
	if mirror, ok := WorkspaceMirrorPath(in.Workspace); ok {
		binds = append(binds, Bind{Source: in.Workspace, Target: mirror, Mode: "rw"})
		workingDir = mirror
	}

	return Result{Binds: binds, Warnings: warnings, WorkingDir: workingDir, StateDir: stateDirIn(in.Host, merged)}, nil
}

// Merge returns the post-merge mount list (defaults retargeted by
// MountsRoot, then patched/replaced/appended/disabled by cfg.Mounts) for the
// given host.
// It materialises no source and binds no workspace, so callers can inspect
// the resolved set without touching the filesystem the plan describes —
// but it is not side-effect-free: resolving the gate below queries the
// proximo binary and stats the CA it names.
//
// It resolves the proximo gate itself because its callers are read-only
// surfaces answering one question each (`mounts list`, `config doctor`),
// not sessions with a gate already in hand. A session goes through Plan,
// which carries the one PlanInput.Proximo resolved for the invocation.
func Merge(host fsx.Host, cfg *config.Config, profile *Profile) ([]config.Mount, error) {
	return merge(host, cfg, profile, proximo.Resolve(host, cfg))
}

// merge is Merge with the proximo gate declared rather than derived — the
// form Plan uses so a session pays the gate's subprocess query once, at the
// composition root, instead of once per pipeline that asks about it.
func merge(host fsx.Host, cfg *config.Config, profile *Profile, gate proximo.Gate) ([]config.Mount, error) {
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
	// Two things behind this function read host.Home: the inherit_host_auth
	// pre-stat below, and the ~/.proximo fallback the gate was resolved from.
	// A caller with no home to declare leaves it empty, and both degrade
	// rather than fail — applyInheritHostAuth treats ~/ paths as-is so os.Stat
	// reports them missing, and proximo.Resolve returns a gate with no CA path
	// so the bind drops out of the set. That is why Merge takes a Host
	// without validating it where Plan does: the merge
	// contract itself (patch, replace, disable, mounts_root) is answerable
	// without a home, and the two probes that are not degrade the way the
	// discarded os.UserHomeDir error used to make them degrade.
	base := applyMountsRoot(defaults(), root, shared)
	base, err := applyInheritHostAuth(base, cfg.InheritHostAuth, host.Home)
	if err != nil {
		return nil, err
	}
	if cfg.Bridge != nil && !*cfg.Bridge {
		base = dropMountByName(base, "bridge")
		base = dropMountByName(base, "bridge-legacy")
		base = dropMountByName(base, "bridge-run")
	}
	// proximo CA bind is injected here (not in defaults()) because its source
	// is host-specific and only relevant when the gate is on. resolveAll
	// soft-skips it with a warning when proximo is not installed.
	if m, ok := gate.CAMount(); ok {
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

// OverlayDockerfilePath returns the host path of the optional local overlay
// Dockerfile (default ~/.toolbox/Dockerfile). It honours the same root
// resolution the mount pipeline uses — a profile root wins over a
// config-level mounts_root, else the ~/.toolbox default — so the overlay
// file lives beside the retargeted credential dirs. The file itself is not a
// mount; the path is resolved here so the root-resolution rule lives in one
// place.
func OverlayDockerfilePath(host fsx.Host, cfg *config.Config, profile *Profile) (string, error) {
	if err := host.Validate(); err != nil {
		return "", err
	}
	root := cfg.MountsRoot
	if r := profile.Root(); r != "" {
		root = r
	}
	return filepath.Clean(host.Expand(mountsRootJoin(root, "Dockerfile"))), nil
}

// StateDirPath returns the resolved host path of the toolbox state mount —
// the directory the container sees as ~/.toolbox-state. Resolved through
// Merge rather than re-derived, so a mounts_root retarget, a profile root, a
// `--share state` carve-out and a user `mounts:` patch all land here the same
// way they land on the bind itself. The lookup keys on the container *target*,
// not the mount name: the name is the user's to change, the target is what
// makes host and container agree on one directory. Returns "" when nothing
// mounts there any more, which callers read as "no shared state to write" —
// better than handing them a path the container cannot see.
//
// The mount source is needed as a *path* (not a bind) by the host-side update
// prefetch, which writes the cache the in-container prompt hook reads: the
// two ends must address the same directory or the banner never fires.
func StateDirPath(host fsx.Host, cfg *config.Config, profile *Profile) (string, error) {
	if err := host.Validate(); err != nil {
		return "", err
	}
	merged, err := Merge(host, cfg, profile)
	if err != nil {
		return "", err
	}
	return stateDirIn(host, merged), nil
}

// stateDirIn is the lookup itself, over an already-merged set: the sole
// spelling of "which host directory is the state mount", shared by Plan (which
// merged for its own reasons and publishes the answer on Result) and
// StateDirPath (which merges to answer this question alone).
func stateDirIn(host fsx.Host, merged []config.Mount) string {
	for _, m := range merged {
		if m.Target == StateMountTarget {
			return filepath.Clean(host.Expand(m.Source))
		}
	}
	return ""
}
