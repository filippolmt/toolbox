// Package sessionplan owns the pipeline that turns a toolbox Config, a
// workspace path, and --publish specs into the typed plan handed to
// internal/container.Shell: image reference, bind set, publish specs, env,
// working dir, container name, container Cmd, security opts. Plan is the
// single seam; it owns the filesystem side effects of the mount stage.
package sessionplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/docker/go-connections/nat"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/filippolmt/toolbox/internal/bridge"
	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/proximo"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sdd"
	"github.com/filippolmt/toolbox/internal/version"
)

// --- Public Seams ---

// Image identifies the container image to launch. Ref defaults to the
// canonical registry image (`toolbox build` overwrites its local cache) but
// can be relocated, opt-in, via config Image / RegistryMirror —
// build.ResolveImage owns the precedence. PullPolicy mirrors config.Pull
// ("auto" | "always" | "never") and drives imageplan.RefreshAtStart, plus
// imageplan.Refresh on the session-reload path.
type Image struct {
	Ref        string
	PullPolicy string
}

// SessionPlan is the resolved-fs plan returned by Plan. Binds are the
// typed mountplan.Bind entries with sources fully resolved (symlinks
// created, dirs materialised). Lifecycle stringifies them at the daemon
// edge.
type SessionPlan struct {
	Image         Image
	Binds         []mountplan.Bind
	Warnings      []string
	WorkingDir    string
	ExposedPorts  network.PortSet
	PortBindings  network.PortMap
	Env           []string
	ContainerName string
	Cmd           []string
	// ExecCmd overrides the command run in the attached interactive exec
	// session. When nil the exec reuses Cmd (the normal shell). Set by Plan
	// for a worktree session (PlanInput.Worktree), which keeps the container's
	// main process an idle shell while the agent runs in the user's attached
	// session — so the agent does not also run headless in the container's
	// main PID. Lifecycle.Shell reads it at the exec edge.
	ExecCmd     []string
	SecurityOpt []string
	// ExtraHosts is the docker --add-host list. Populated when the browser
	// bridge is enabled so host.docker.internal resolves on native Linux
	// Docker (Docker Desktop already provides the mapping; the duplicate
	// entry is harmless there).
	ExtraHosts []string
	// OverlayDockerfile is the resolved host path of the optional local image
	// overlay (~/.toolbox/Dockerfile, mounts_root-aware). Always set; the
	// container edge (localimage.Ensure) passes through unchanged when the
	// file is absent and only builds the derived `:local` image when present.
	OverlayDockerfile string
	// StateDir is the resolved host source of the toolbox state mount — the
	// directory the container sees as ~/.toolbox-state (mounts_root- and
	// profile-aware, same resolution as OverlayDockerfile). The container edge
	// hands it to the update prefetch, which writes the cache the in-container
	// prompt hook reads back through the bind. Empty when the mount was
	// removed from the plan, which turns the prefetch off.
	StateDir string
	// Proximo mirrors cfg.Proximo. When true, the container edge augments
	// ExtraHosts with the proximo-routed hostnames discovered from running
	// containers (pinned to host-gateway) just before ContainerCreate — that
	// discovery needs the Docker client, which only the container package
	// holds. The CA bind + NODE_EXTRA_CA_CERTS/TOOLBOX_PROXIMO_CA env are
	// already resolved into Binds/Env here (pure, host-side).
	Proximo bool
	// ReclaimImages mirrors `image_reclaim`, resolved from its tri-state:
	// absent means on, since Image Reclamation runs unless the developer
	// disabled it in so many words. The container edge starts the sweep with
	// it — the act needs the Docker client and the digest the created
	// container actually runs, neither of which exists here.
	ReclaimImages bool
	// PidMode is the docker --pid value. Empty for an ordinary session (the
	// container gets its own PID namespace); `container:<anchor>` when the
	// session opted into cross-container peer messaging, which is what makes
	// Claude Code's pid-keyed session registry resolvable across containers.
	// The container edge creates the anchor if it is missing.
	PidMode string
	// ReloadFrom is the handover from the process that re-exec'd into this one,
	// nil for an ordinary shell start. The container edge reads it to know
	// which container to destroy before it creates, and what the "before" half
	// of the reload summary is. It is deliberately not part of Env: the
	// variable carrying it never enters a container.
	ReloadFrom *reload.From
}

// LaunchesAgent reports whether the session auto-launches an agent in the
// attached exec rather than dropping the developer at a shell. Only a
// `toolbox worktree` session does, and that is exactly the launch mode a
// reload has to reproduce — so the reload asks this rather than inferring
// intent from what the developer happened to be running.
func (p *SessionPlan) LaunchesAgent() bool { return p.ExecCmd != nil }

// EffectiveCmd is what this session actually runs: the worktree agent launch
// when the plan carries one, the configured shell otherwise. Both halves are
// this plan's own fields, so the choice belongs here rather than being
// re-made at the attach.
func (p *SessionPlan) EffectiveCmd() []string {
	if p.ExecCmd != nil {
		return p.ExecCmd
	}
	return p.Cmd
}

// ReloadMarkerPath is the **host-side** path of this session's reload marker:
// the state mount's resolved host source plus the basename both sides agree
// on. Empty when the plan carries no state mount — which is also the only way
// the container could not see the marker either, since reloadMarkerEnv
// withholds the variable in exactly that case.
//
// A method rather than a helper in `container`, because both halves of it are
// this plan's own fields, and the container-side spelling of the same basename
// is already composed here (reloadMarkerEnv). The two roots differ on purpose:
// the container writes under the mount target, the host reads under the bind's
// source.
func (p *SessionPlan) ReloadMarkerPath() string {
	if p.StateDir == "" {
		return ""
	}
	return reload.MarkerPath(p.StateDir, p.ContainerName)
}

// PlanInput is the full set of inputs to Plan. Bundling the inputs keeps the
// container-name decision — the one field that varies between a workspace
// session and a named shell — a single Name input rather than a post-planning
// override in the caller.
type PlanInput struct {
	Cfg            *config.Config
	Workspace      string
	Ports          []string
	BridgeLoopback bool

	// ImageDigest is the running image's resolved repo digest (`sha256:...`),
	// supplied host-side by the caller (it needs the Docker client, which the
	// pure planner does not hold). Empty when unresolvable — e.g. a locally
	// built untagged image; the identity injection then omits the digest entry
	// so a reader skips the image comparison rather than treating an empty
	// value as a stale digest. See session-reload.
	ImageDigest string

	// Name is the named shell exactly as the user typed it, empty for workspace
	// sessions (no-arg / absolute-path). Both derivations live behind this seam:
	// SanitizeShellName yields the container suffix (`toolbox-named-<sanitized>`;
	// empty falls back to the workspace path hash), and config.NormalizeShellKey
	// yields the cfg.Shells key whose per-shell env: overlays the top-level one.
	// Keeping the raw name here means cmd sets Name by intent and decides
	// neither the container-name format nor the env overlay.
	Name string

	// Profile is the active `toolbox shell --profile` selection, or nil for a
	// default (non-profile) session. It drives the mount plan (its own root +
	// share skip-set, owned by mountplan.Profile) and is folded into the
	// container name so a profile shell never collides with the default shell
	// for the same workspace, each keeping its own mount set fixed at
	// ContainerCreate.
	Profile *mountplan.Profile

	// Worktree, when non-nil, plans a `toolbox worktree` session: the main
	// repo's .git enters the mount plan and ExecCmd launches the agent in the
	// user's attached session. Nil for every other session.
	Worktree *WorktreeSession

	// Peer opts the session into cross-container Claude Code peer messaging:
	// the shared inbox-socket bind (mountplan) plus the shared PID namespace
	// (PidMode), and the opt-in folded into the container name. Resolved by
	// cmd from `peer_messaging:` and `--peer`. Default on — declining it
	// takes an explicit `peer_messaging: false` or `--peer=false`.
	// See docs/adr/0003-cross-container-peer-messaging.md.
	Peer bool

	// ReloadFrom is the payload handed over by the process this one re-exec'd
	// from, or nil for an ordinary shell start. The reload carries nothing and
	// re-derives everything, so the only thing it changes here is the working
	// directory — and only when that directory is still under the workspace.
	ReloadFrom *reload.From
}

// WorktreeSession carries the inputs a `toolbox worktree` session adds to a
// plain one. Both derivations from it stay behind this seam: RepoRoot becomes
// the .git bind, and Agent + Prompt become the ExecCmd wrapper.
type WorktreeSession struct {
	// RepoRoot is the main repository root (not the worktree path, which is
	// the session's Workspace). A linked worktree's .git points into it.
	RepoRoot string
	// Agent is the resolved AI agent binary to auto-launch (claude | codex).
	Agent string
	// Prompt is the initial task handed to the agent, empty for a bare launch.
	Prompt string
	// Resume relaunches the agent on its most recent conversation instead of
	// starting one. The third launch mode beside prompt and bare, and the only
	// one a developer never asks for directly: it is set by a reload, whose
	// whole promise is conversational continuity across the recreate.
	Resume bool
}

// gitDir returns the main repo's .git directory for a worktree session, or ""
// when this is not one.
func (in PlanInput) gitDir() string {
	if in.Worktree == nil {
		return ""
	}
	return filepath.Join(in.Worktree.RepoRoot, ".git")
}

// containerName resolves the container name from the workspace path and the
// raw named-shell name — the single place the workspace-hash vs named-shell
// choice lives. A name that sanitizes to nothing (empty, blanks-only) →
// workspace-derived; otherwise the named form. bridgeLoopback-free and fs-free.
func containerName(workspace, name string, profile *mountplan.Profile, peer bool) string {
	// SanitizeShellName trims before it lowercases and folds the charset, so
	// `toolbox shell " Infra"` lands on the same container as `infra`.
	sanitized := SanitizeShellName(name)
	if sanitized == "" {
		// Workspace sessions fold the full profile discriminator (name + share
		// set) into the hash, so switching profile OR --share yields a distinct
		// container — mounts are fixed at ContainerCreate.
		return ContainerNameFor(workspace, peerDiscriminator(mountplan.ContainerDiscriminator(profile), peer))
	}
	// Named shells fold the profile name into the sanitized name so a profile
	// named-shell keeps a distinct container from the default one. The suffix
	// carries no -<8hex>, so it stays disjoint from the workspace-hash format.
	// (A --share change alone reuses the container — refresh with `toolbox stop`,
	// same as any mount/port flag; see docs/commands.md#profiles.)
	if pn := mountplan.ProfileName(profile); pn != "" {
		sanitized = sanitized + "-" + SanitizeShellName(pn)
	}
	if peer {
		sanitized = sanitized + peerNameSuffix
	}
	return namedContainerNameFromSanitized(sanitized)
}

// Plan walks the full session pipeline for in.Cfg + in.Workspace + in.Ports
// and returns the resolved plan handed to container.Shell. Hard fails when
// mount-stage validation rejects the user list, when port specs cannot
// be parsed, or when the home directory cannot be resolved. Per-mount
// soft skips surface via SessionPlan.Warnings.
//
// in.BridgeLoopback toggles the in-container loopback bridge (init.d/70):
// when true and at least one publish spec is present, the resulting env
// carries TOOLBOX_LOOPBACK_BRIDGE_PORTS so the bridge listener spawns one
// socat per published container port — see docs/commands.md#loopback-bridge.
func Plan(in PlanInput) (*SessionPlan, error) {
	workspace := normalizeWorkspace(in.Workspace)

	exposed, bindings, uniqContainerPorts, err := parsePublishSpecs(in.Ports)
	if err != nil {
		return nil, err
	}

	ref := build.ResolveImage(in.Cfg.Image, in.Cfg.RegistryMirror)

	// Resolve the container Cmd up front so an incoherent shell+tools
	// combination fails before any fs side effects (mountplan.Plan creates
	// dirs/symlinks under ~/.toolbox; we don't want them on a config error).
	cmd, err := ResolveShellCmd(in.Cfg)
	if err != nil {
		return nil, err
	}

	// mountplan.Plan owns the fs side effects (mkdir, symlinks); per-mount
	// soft skips ride out on Warnings.
	mp, err := mountplan.Plan(mountplan.PlanInput{
		Cfg:       in.Cfg,
		Workspace: workspace,
		Profile:   in.Profile,
		GitDir:    in.gitDir(),
		Peer:      in.Peer,
	})
	if err != nil {
		return nil, err
	}

	// Resolve the local overlay Dockerfile path (mounts_root-aware). Runs
	// after mountplan.Plan so the same home-resolution failure surfaces once.
	overlayDockerfile, err := mountplan.OverlayDockerfilePath(in.Cfg, in.Profile)
	if err != nil {
		return nil, err
	}

	// Host path of the state mount, for the host-side update prefetch.
	stateDir, err := mountplan.StateDirPath(in.Cfg, in.Profile)
	if err != nil {
		return nil, err
	}

	// Resolved before the env is composed: the reload marker is named after the
	// container, and its path is what declares the capability to the image.
	name := containerName(workspace, in.Name, in.Profile, in.Peer)
	workingDir := reloadWorkingDir(mp.WorkingDir, in.ReloadFrom)

	return &SessionPlan{
		Image:             Image{Ref: ref, PullPolicy: in.Cfg.Pull},
		Binds:             mp.Binds,
		Warnings:          mp.Warnings,
		WorkingDir:        workingDir,
		ExposedPorts:      exposed,
		PortBindings:      bindings,
		Env:               composeEnv(in, workspace, workingDir, uniqContainerPorts, slices.Concat(proximo.Env(in.Cfg), agentHomeEnv(mp.Binds), reloadMarkerEnv(stateDir, name))),
		ContainerName:     name,
		Cmd:               cmd,
		ExecCmd:           worktreeExecCmd(cmd, resolveWorktreeLaunch(in.Worktree, in.ReloadFrom, workingDir)),
		SecurityOpt:       NestedSandboxSecurityOpt(in.Cfg),
		ExtraHosts:        browserBridgeExtraHosts(in.Cfg),
		OverlayDockerfile: overlayDockerfile,
		StateDir:          stateDir,
		Proximo:           proximo.Enabled(in.Cfg),
		ReclaimImages:     in.Cfg.ImageReclaim == nil || *in.Cfg.ImageReclaim,
		PidMode:           peerPidMode(in.Peer),
		ReloadFrom:        in.ReloadFrom,
	}, nil
}

// reloadMarkerEnv declares the reload capability to the image and hands it the
// one path it could not build for itself. Presence of the variable is the
// capability: an image whose `toolbox-reload` finds it absent refuses at the
// prompt, which is the guard against the silent direction of version skew —
// a new image under an old CLI, where the marker would be written, ignored,
// and the session torn down for nothing.
//
// Container-side path, because that is the side that writes it. The host edge
// composes the same basename against the bind's host source.
//
// **Emitted only when that bind exists.** A session whose `mounts:` dropped the
// state mount has nowhere for the two sides to meet, and the container would
// not notice: the entrypoint creates ~/.toolbox-state either way, so the write
// succeeds into a container-local directory, the shell exits, and the host
// reads nothing — the session spent in silence that the capability marker is
// here to prevent. Withholding the variable turns that into the refusal at the
// prompt it should have been.
func reloadMarkerEnv(stateDir, containerName string) []string {
	if stateDir == "" {
		return nil
	}
	return []string{reload.MarkerEnv + "=" + path.Join(mountplan.StateMountTarget, reload.MarkerName(containerName))}
}

// resolveWorktreeLaunch reproduces a worktree session's launch mode across a
// reload, which is deliberately not the same as replaying its intent.
//
// Two rules, both from the fact that the original invocation has already run.
// The prompt is **dropped**: the task that opened this worktree was sent once
// and completed, and re-sending it would start the work again. The agent
// **resumes** instead — but only when the carried working directory survived
// validation, because `claude --continue` is keyed on the cwd and the
// workspace is mounted twice. On the fallback the agent launches bare:
// resuming the wrong lineage in silence is worse than not resuming.
//
// Returns a copy; the caller's WorktreeSession is an input, not a scratchpad.
func resolveWorktreeLaunch(wt *WorktreeSession, from *reload.From, workingDir string) *WorktreeSession {
	if wt == nil || from == nil {
		return wt
	}
	out := *wt
	out.Prompt = ""
	out.Resume = from.Resume && from.Cwd != "" && workingDir == from.Cwd
	return &out
}

// reloadWorkingDir applies the one piece of in-container process state a
// reload carries. Anything outside the workspace — `cd /home/toolbox`, a path
// inside a mount the new session may not have, a directory since deleted —
// falls back silently to the canonical working directory, because a reload
// that lands in a directory the new container does not have is worse than one
// that lands at the top of the workspace.
//
// Both spellings of the workspace are accepted: the canonical /workspace and
// the host-path mirror, which is what mountplan hands back as the working
// directory whenever the mirror bind exists. They are the same content, and
// rejecting the one the developer happened to be standing in would make the
// fallback fire on the common case.
func reloadWorkingDir(canonical string, from *reload.From) string {
	if from == nil || from.Cwd == "" {
		return canonical
	}
	for _, root := range []string{canonical, mountplan.WorkspaceTarget} {
		if root != "" && (from.Cwd == root || strings.HasPrefix(from.Cwd, root+"/")) {
			return from.Cwd
		}
	}
	return canonical
}

// Container paths of the two agent homes whose HOST source the bridge daemon
// needs. They are the mount targets in mountplan.Defaults, kept here as
// literals and pinned to that set by TestAgentHomeTargetsAreMounted — the
// daemon's copy of these paths would otherwise drift silently.
const (
	claudeHomeTarget = "/home/toolbox/.claude"
	codexHomeTarget  = "/home/toolbox/.codex"
)

// agentHomeEnv exports the HOST paths behind the container's two agent homes.
// `proximo skill install` runs on the host through the bridge but writes files
// an *in-container* agent must read, and mounts_root, --profile and
// inherit_host_auth each move the source backing those targets — so the daemon
// cannot derive them and the session plan has to say. TOOLBOX_HOST_AGENT_HOME
// is the parent of the claude source (upstream resolves the Claude dir as
// $HOME/.claude); TOOLBOX_HOST_CODEX_HOME is the codex source itself
// ($CODEX_HOME is read directly). The two are separate values because
// inherit_host_auth can move one without the other. A target absent from the
// plan yields no entry, and the daemon falls back to its own default.
//
// Emitted as composeEnv's "extra", so these land with the curated entries and
// ahead of the user's own env — which must stay last to win collisions.
// → "Proximo Execution Modes" in CONTEXT.md.
func agentHomeEnv(binds []mountplan.Bind) []string {
	var agentHome, codexHome string
	for _, b := range binds {
		switch b.Target {
		case claudeHomeTarget:
			agentHome = filepath.Dir(b.Source)
		case codexHomeTarget:
			codexHome = b.Source
		}
	}
	var out []string
	if agentHome != "" {
		out = append(out, bridge.HostAgentHomeEnv+"="+agentHome)
	}
	if codexHome != "" {
		out = append(out, bridge.HostCodexHomeEnv+"="+codexHome)
	}
	return out
}

// browserBridgeExtraHosts returns the docker --add-host entries needed for
// the in-container wrapper to reach the host daemon. Empty when the bridge
// is disabled.
func browserBridgeExtraHosts(cfg *config.Config) []string {
	if cfg.Bridge != nil && !*cfg.Bridge {
		return nil
	}
	return []string{"host.docker.internal:host-gateway"}
}

// loopbackBridgeEnv returns the `TOOLBOX_LOOPBACK_BRIDGE_*` env entries the
// container needs to enable the runtime bridge for the requested publish
// specs. Returns nil when bridgeLoopback is false. When bridgeLoopback is
// true with no published container ports, only the no-publish marker env
// is emitted so the in-container init.d/70 script can warn the user about
// the no-op. When ports are present, the deduplicated, insertion-ordered
// list is comma-joined into TOOLBOX_LOOPBACK_BRIDGE_PORTS.
func loopbackBridgeEnv(bridgeLoopback bool, uniqContainerPorts []string) []string {
	if !bridgeLoopback {
		return nil
	}
	if len(uniqContainerPorts) == 0 {
		// init.d/70 reads this marker to emit the "enabled but no -p ports
		// published — skipping" warning. The marker is intentionally absent
		// in the happy path; a non-empty TOOLBOX_LOOPBACK_BRIDGE_PORTS is
		// itself the "enabled" signal.
		return []string{"TOOLBOX_LOOPBACK_BRIDGE_NO_PUBLISH=1"}
	}
	return []string{"TOOLBOX_LOOPBACK_BRIDGE_PORTS=" + strings.Join(uniqContainerPorts, ",")}
}

// composeEnv assembles the full ordered env slice for a session: the curated
// workspace + SDD entries first, then the loopback-bridge markers, then the
// self-identity entries (CLI version + image digest) the in-container update
// poller compares against published releases, then the host platform
// (GOOS/GOARCH, for anything cross-compiling for the host from inside a
// shell), then the managed-statusline opt-out marker, then any caller-supplied
// curated extras (Plan passes proximo.Env, which stats the host CA), then
// the user-supplied env: map.
//
// The user layer is EffectiveEnv(in.Name), not cfg.Env: the active named
// shell's `shells.<name>.env` overlays the top-level `env:` here, at the seam,
// so no caller has to pre-mix the two into the config it hands over. An empty
// Name resolves to a copy of the top-level map — one path, no branch.
// Reserved-key collisions are already rejected by config.ValidateEnv, so
// userEnv can append unconditionally.
func composeEnv(in PlanInput, workspace, workingDir string, uniqContainerPorts, extra []string) []string {
	env := append(shellEnv(workspace, workingDir, in.Cfg.SDD), loopbackBridgeEnv(in.BridgeLoopback, uniqContainerPorts)...)
	env = append(env, identityEnv(in.ImageDigest)...)
	env = append(env, hostPlatformEnv()...)
	env = append(env, managedStatuslineEnv(in.Cfg.ManagedStatusline)...)
	env = append(env, extra...)
	return append(env, userEnv(in.Cfg.EffectiveEnv(in.Name))...)
}

// ImageDigestEnv names the container's record of the image it was created
// from. Exported because the container edge reads it back off a running
// container to answer "is this session behind the local store?" — the pair of
// writer and reader is the seam, and a literal on each side would drift.
const ImageDigestEnv = "TOOLBOX_IMAGE_DIGEST"

// CLIVersionEnv records which host CLI created the container. It lost its
// only consumer when the in-container update poller was retired in favour of
// a host-side detector — the host *is* the CLI, so it needs no injection — and
// then gained two: the reload summary reads it back off the plan to say which
// CLI was left behind, and `toolbox-reload` quotes it in the refusal it prints
// when the CLI is too old to support a reload at all.
const CLIVersionEnv = "TOOLBOX_CLI_VERSION"

// NoUpdateCheckEnv opts a session out of the update banner. One name, read on
// both sides of the bind mount since detection moved host-side: the container
// edge skips the probe when it arrives through the `env:` passthrough, and
// zshrc skips the render. Exported so neither side spells it as a literal —
// a rename on one side alone would quietly half-disable the feature.
const NoUpdateCheckEnv = "TOOLBOX_NO_UPDATE_CHECK"

// EnvValue reads one variable out of a docker-style KEY=VALUE list, the shape
// both a composed plan and a ContainerInspect carry. The last occurrence wins,
// matching how the daemon resolves a duplicated key — which is the reason this
// is not a one-line loop at the call site: getting that rule wrong reads the
// value the container is *not* running under.
func EnvValue(env []string, key string) string {
	out := ""
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			out = v
		}
	}
	return out
}

// WithImageDigest returns env with the container's image-digest record set to
// digest — replacing an existing entry, dropping it when digest is empty, and
// otherwise inserting it in identityEnv's position (immediately after the CLI
// version) so the documented emission order holds either way.
//
// It exists because the host resolves that digest *before* the shell-start
// registry refresh runs: a refresh that pulls a newer image would otherwise
// stamp the container with the digest the local store held a moment earlier,
// and the container's record of what it was created from would be a lie —
// one that reads, to the update prefetch, as "this session is behind".
func WithImageDigest(env []string, digest string) []string {
	out := make([]string, 0, len(env)+1)
	placed := false
	for _, e := range env {
		// An existing entry is the position: replace it, or drop it when the
		// digest became unresolvable.
		if strings.HasPrefix(e, ImageDigestEnv+"=") {
			if digest != "" && !placed {
				out = append(out, ImageDigestEnv+"="+digest)
				placed = true
			}
			continue
		}
		out = append(out, e)
		// No entry to replace: identityEnv emits the digest right after the
		// CLI version, so that is where a re-stamp puts it back.
		if !placed && digest != "" && strings.HasPrefix(e, CLIVersionEnv+"=") {
			out = append(out, ImageDigestEnv+"="+digest)
			placed = true
		}
	}
	return out
}

// identityEnv emits the container's self-identification: CLIVersionEnv
// (always, from the host CLI build) and ImageDigestEnv (only when the host
// resolved a repo digest). The digest entry is omitted — not emitted empty —
// when unresolvable, so a reader distinguishes "created from a local build"
// from "created from a digest that is now stale" instead of reporting a bogus
// update. See session-reload.
func identityEnv(imageDigest string) []string {
	out := []string{CLIVersionEnv + "=" + version.Version}
	if imageDigest != "" {
		out = append(out, ImageDigestEnv+"="+imageDigest)
	}
	return out
}

// hostPlatformEnv emits the host's OS and architecture in GOOS/GOARCH spelling.
// The CLI knows both firsthand — it is the host process — which is the whole
// point: `uname` run inside the container reports the container, so anything
// cross-compiling for the host from a toolbox shell (the Makefile's go-build)
// has no way to ask. Always emitted, so a consumer can treat "absent" as
// "container predates this" and fall back rather than guess wrong.
func hostPlatformEnv() []string {
	return []string{
		"TOOLBOX_HOST_OS=" + runtime.GOOS,
		"TOOLBOX_HOST_ARCH=" + runtime.GOARCH,
	}
}

// managedStatuslineEnv emits TOOLBOX_MANAGED_STATUSLINE=0 only when the user
// opted out (managed_statusline: false); nil/true emit nothing so the boot
// hook's default-on path runs. Emitting only on opt-out keeps the env minimal
// and the value unambiguous: present-and-0 = opt out, absent = managed.
func managedStatuslineEnv(managed *bool) []string {
	if managed != nil && !*managed {
		return []string{"TOOLBOX_MANAGED_STATUSLINE=0"}
	}
	return nil
}

// userEnv emits the cfg.Env map as deterministically ordered K=V strings.
// Sorted by key so the plan is stable across runs (map iteration order is
// nondeterministic) — load-bearing for the sessionplan tests.
func userEnv(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// MissingPublishPorts returns the wanted publish ports that the existing
// container was not created with. PortBindings are fixed at create time,
// so "--publish" on a reused container is a silent no-op for any port
// not in this list. nil-safe against InspectResponse.HostConfig.
func MissingPublishPorts(wanted network.PortMap, inspect container.InspectResponse) []string {
	if inspect.HostConfig == nil {
		return nil
	}
	current := inspect.HostConfig.PortBindings
	var missing []string
	for port := range wanted {
		if _, ok := current[port]; !ok {
			missing = append(missing, port.String())
		}
	}
	return missing
}

// PortConflict is one wanted publish port whose host side is already bound
// by another container. Port is the host port plus protocol ("8877/tcp");
// Holder is the container name reported by the daemon.
type PortConflict struct {
	Port   string
	Holder string
}

// ConflictingPublishPorts returns the wanted publish ports whose host side is
// already taken, sorted by port so messages are stable. occupied maps
// "<hostPort>/<proto>" to the name of the container holding it; anything
// absent counts as free.
//
// The host side is what matters: Docker binds wanted[containerPort].HostPort
// on the host, so a shifted mapping (host 9877 -> container 8877) does not
// clash with a holder of 8877. The host IP is deliberately ignored — the
// kernel refuses 127.0.0.1:p over a wildcard bind of p and vice versa, so
// comparing IPs would miss the common 0.0.0.0 holder.
//
// Best-effort by construction: a caller that can only see Docker containers
// passes a Docker-only occupied set, and a non-Docker host process holding
// the port still surfaces as the daemon's own create-time error.
func ConflictingPublishPorts(wanted network.PortMap, occupied map[string]string) []PortConflict {
	if len(occupied) == 0 {
		return nil
	}
	seen := make(map[string]string, len(wanted))
	for port, bindings := range wanted {
		// Protocol comes from the wanted port, the number from the host side
		// of its binding: the key must describe what gets bound on the host.
		proto := string(port.Proto())
		for _, b := range bindings {
			key := b.HostPort + "/" + proto
			if holder, taken := occupied[key]; taken {
				seen[key] = holder
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	conflicts := make([]PortConflict, 0, len(seen))
	for key, holder := range seen {
		conflicts = append(conflicts, PortConflict{Port: key, Holder: holder})
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Port < conflicts[j].Port })
	return conflicts
}

// ContainerNamePrefix is the prefix that identifies toolbox-managed
// containers. Exported so internal/container.StopAll can filter the
// host's full container list without taking a SessionPlan input.
const ContainerNamePrefix = "toolbox-"

// IsToolboxContainerName reports whether a container name belongs to a
// toolbox-managed container: the "toolbox-" prefix (workspace-hash and
// named-shell formats) or the legacy singleton name "toolbox". Shared by
// internal/container StopAll (bulk teardown) and List (host inventory) so the
// definition of "a toolbox container" lives in one place.
func IsToolboxContainerName(name string) bool {
	return name == "toolbox" || strings.HasPrefix(name, ContainerNamePrefix)
}

// MaxContainerNameLen is the documented Docker convention cap for
// container names. Exposed so cmd/ validators can budget the user-supplied
// portion (workspace basename or named-shell name) without rederiving it.
const MaxContainerNameLen = 63

// WorkspaceHashLen is the length of the trailing hash suffix in the
// workspace container format (`toolbox-<base>-<8hex>`). cmd/ uses this
// value to reject named-shell names whose sanitized form would be
// indistinguishable from the hash suffix.
const WorkspaceHashLen = 8

var sanitizeRe = regexp.MustCompile(`[^a-z0-9]+`)

// ContainerNameFor builds the container name for a given workspace path.
// Exported so internal/container.Stop (which has no SessionPlan in scope)
// can reuse the format. Plan and Merge call this internally and stash the
// result on plan.ContainerName.
//
// Format: toolbox-<basename>-<hash8>. The hash is over the absolute path
// so two directories sharing a basename do not collide. Output capped at
// 63 chars (Docker convention): basename is truncated first so the prefix
// and hash suffix survive.
//
// A non-empty discriminator (the profile identity, from
// mountplan.ContainerDiscriminator) is folded into the hash seed (not the
// visible basename), so a profile shell gets a distinct name from the default
// shell for the same workspace while the format and length budget are
// unchanged. The plain workspace hash (sddEnv sentinel) stays discriminator-free
// — only the container identity varies by profile.
func ContainerNameFor(workspace, discriminator string) string {
	abs := normalizeWorkspace(workspace)
	seed := abs
	if discriminator != "" {
		seed = abs + "\x00profile=" + discriminator
	}
	hash := hashWorkspace(seed)

	base := workspaceSlug(abs)

	// MaxContainerNameLen - len("toolbox-") - len("-") - WorkspaceHashLen.
	maxBasename := MaxContainerNameLen - len(ContainerNamePrefix) - 1 - WorkspaceHashLen
	if len(base) > maxBasename {
		base = strings.TrimRight(base[:maxBasename], "-")
		if base == "" {
			base = "root"
		}
	}

	return ContainerNamePrefix + base + "-" + hash
}

// MaxNamedShellNameLen is the cap for the sanitized form of a named shell
// name, derived from MaxContainerNameLen minus the named-shell prefix +
// infix. cmd/ validators consume this so a prefix rename keeps the cap in
// lockstep.
const MaxNamedShellNameLen = MaxContainerNameLen - len(ContainerNamePrefix) - len(NamedContainerNameInfix)

// NamedContainerNameInfix separates the ContainerNamePrefix from the
// user-supplied shell name. The dedicated infix guarantees the named-shell
// container format is disjoint from the workspace-hash format produced by
// ContainerNameFor (`toolbox-<base>-<8hex>`): a workspace whose basename is
// literally "named" still gets a trailing -<8hex> suffix, while named-shell
// containers never carry one (callers reject hash-shaped names upstream).
const NamedContainerNameInfix = "named-"

// SanitizeShellName lowercases and slug-collapses the user-supplied shell
// name using the same character class as the workspace basename portion of
// ContainerNameFor. Exposed so cmd/* can validate the sanitized form (e.g.
// reject names that would collide with the workspace-hash suffix) without
// re-implementing the regex.
func SanitizeShellName(name string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	base = sanitizeRe.ReplaceAllString(base, "-")
	return strings.Trim(base, "-")
}

// NamedContainerName returns the container name used by `toolbox shell <name>`
// and `toolbox stop <name>`.
//
// Format: toolbox-named-<sanitized>. The named- infix keeps this disjoint
// from the workspace-hash format (toolbox-<base>-<8hex>); cmd/ validation
// additionally refuses sanitized names that match the 8-hex hash pattern so
// a named shell cannot impersonate a workspace container.
func NamedContainerName(name string) string {
	return namedContainerNameFromSanitized(SanitizeShellName(name))
}

// namedContainerNameFromSanitized is the post-sanitization sibling of
// NamedContainerName, for the callers inside this package that already hold
// the sanitized form (containerName folds the profile name into it before
// composing the final name).
func namedContainerNameFromSanitized(sanitized string) string {
	if sanitized == "" {
		sanitized = "shell"
	}
	return ContainerNamePrefix + NamedContainerNameInfix + sanitized
}

// workspaceSlug lowercases and slug-collapses the basename of a (normalized)
// workspace path, falling back to "root" when nothing survives — e.g. the
// filesystem root, where filepath.Base returns "/". Shared by ContainerNameFor
// and shellEnv so the container name and the Remote Control session prefix
// derive from a single sanitization rule.
func workspaceSlug(abs string) string {
	base := strings.ToLower(filepath.Base(abs))
	base = sanitizeRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		return "root"
	}
	return base
}

// normalizeWorkspace returns the workspace path as an absolute, cleaned
// path. Falls back to the input on Abs failure (the empty string and other
// pathological inputs propagate untouched and surface downstream).
func normalizeWorkspace(workspace string) string {
	if abs, err := filepath.Abs(workspace); err == nil {
		return filepath.Clean(abs)
	}
	return workspace
}

// --- Port Parsing ---

// defaultHostIP keeps OAuth callbacks loopback-only: publish specs with no
// explicit host IP bind 127.0.0.1, not 0.0.0.0, so a callback port is never
// exposed to the LAN.
var defaultHostIP = netip.MustParseAddr("127.0.0.1")

// natMappingToBinding converts a go-connections publish mapping (kept solely
// because nat.ParsePortSpec has no moby equivalent) into the moby port +
// binding pair, applying the loopback host-IP default. This is the only
// nat→moby seam in the codebase.
func natMappingToBinding(m nat.PortMapping) (network.Port, network.PortBinding, error) {
	p, err := network.ParsePort(string(m.Port))
	if err != nil {
		return network.Port{}, network.PortBinding{}, err
	}
	hostIP := defaultHostIP
	if m.Binding.HostIP != "" {
		hostIP, err = netip.ParseAddr(m.Binding.HostIP)
		if err != nil {
			return network.Port{}, network.PortBinding{}, err
		}
	}
	return p, network.PortBinding{HostIP: hostIP, HostPort: m.Binding.HostPort}, nil
}

// parsePublishSpecs parses "docker run -p"-style publish specs into
// Docker's ExposedPorts + PortBindings, and the ordered slice of unique
// container ports (insertion order; deduplicated) used by callers that
// need to iterate ports deterministically — currently loopbackBridgeEnv,
// which forwards each to its socat listener. Defaults the host IP to
// 127.0.0.1 (not 0.0.0.0) so OAuth callbacks stay loopback-only instead
// of being exposed to the LAN.
func parsePublishSpecs(specs []string) (network.PortSet, network.PortMap, []string, error) {
	if len(specs) == 0 {
		return nil, nil, nil, nil
	}
	exposed := network.PortSet{}
	bindings := network.PortMap{}
	seenPorts := make(map[string]struct{}, len(specs))
	orderedPorts := make([]string, 0, len(specs))
	for _, spec := range specs {
		mappings, err := nat.ParsePortSpec(spec)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid --publish %q: %w", spec, err)
		}
		for _, m := range mappings {
			p, binding, perr := natMappingToBinding(m)
			if perr != nil {
				return nil, nil, nil, fmt.Errorf("invalid --publish %q: %w", spec, perr)
			}
			exposed[p] = struct{}{}
			bindings[p] = append(bindings[p], binding)

			port := p.Port() // drops "/tcp" suffix
			if _, dup := seenPorts[port]; !dup {
				seenPorts[port] = struct{}{}
				orderedPorts = append(orderedPorts, port)
			}
		}
	}
	return exposed, bindings, orderedPorts, nil
}

// --- Workspace Env ---

// shellEnv returns the env vars injected into every shell spawned by the
// container. TOOLBOX_HOST_WORKSPACE holds the absolute host path mounted
// at the canonical workspace target so that Makefiles and compose files
// can pass a host-resolvable path to `docker run -v` under the
// bind-mounted socket (DooD): a literal "/workspace/foo" is meaningless
// to the host daemon. PWD is set explicitly to workingDir so that scripts
// reading $PWD directly (without a getcwd fallback) see the same path
// bash exposes after starting in WorkingDir.
// CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX defaults to the machine
// hostname, which inside the container is the Docker container ID — on
// claude.ai (web/mobile) every Remote Control session would show up as
// hex gibberish. The workspace slug (same rule as the container name)
// gives readable session names that match `docker ps` output.
//
// HERDR_SESSION names herdr's persistent session per workspace. ~/.config/herdr
// is one host-global bind (mountplan defaults, "herdr") and herdr persists its
// workspace list there with absolute cwds, then ignores the startup cwd whenever
// the restored session already has workspaces. Unnamed, every container would
// therefore reopen whatever another project saved last — a path this container
// does not mount, which herdr answers by silently falling back to $HOME, so the
// shell lands on /home/toolbox. ContainerNameFor with an empty discriminator is
// the plain workspace identity (slug + path hash, the sddEnv sentinel form):
// derived from the workspace alone, so a --peer or --profile change, which forks
// the container name over the same mounted workspace, keeps the session.
//
// The workspace target itself and the host-path mirror logic live in
// internal/mountplan; sessionplan.Plan consults mountplan.Plan to learn
// workingDir and forwards it here.
func shellEnv(workspace, workingDir string, sddCfg map[string]config.SDDSkill) []string {
	env := []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + workingDir,
		"CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=" + workspaceSlug(workspace),
		"HERDR_SESSION=" + ContainerNameFor(workspace, ""),
	}
	env = append(env, sddEnv(workspace, sddCfg)...)
	return env
}

// sddEnv translates the cfg.SDD opt-in map into the env contract consumed
// by internal/build/assets/entrypoint.sh. Unknown keys are silently dropped
// so a typo in .toolbox.yaml (e.g. `sdd.gds: true`) never aborts the shell
// — the bash bootstrap runs before any user-facing diagnostic surface.
// (Entries with an explicit steps: override are the exception: config's
// ValidateSDD rejects unknown keys there at load time, so they never reach
// this point.)
//
// Field-per-env-var encoding (vs a single pipe-packed value) keeps the
// host/container boundary typed: bash decodes by reading one variable per
// field with no fragile splitting.
func sddEnv(workspace string, sddCfg map[string]config.SDDSkill) []string {
	if len(sddCfg) == 0 {
		return nil
	}
	enabled := resolveEnabledSkills(sddCfg)
	if len(enabled) == 0 {
		return nil
	}

	keys := make([]string, 0, len(enabled))
	for k := range enabled {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	emitted := make([]string, 0, len(keys))
	body := make([]string, 0, len(keys)*5)
	for _, k := range keys {
		s := enabled[k]
		stepArgs := s.InstallSteps
		steps := make([]string, len(stepArgs))
		for i, args := range stepArgs {
			steps[i] = strings.Join(args, " ")
		}
		emitted = append(emitted, k)
		body = append(body,
			sdd.SkillEnvKey(k, sdd.EnvFieldPkg)+"="+s.NpmPackage,
			sdd.SkillEnvKey(k, sdd.EnvFieldVersion)+"="+s.Version,
			sdd.SkillEnvKey(k, sdd.EnvFieldBin)+"="+s.BinName,
			sdd.SkillEnvKey(k, sdd.EnvFieldSteps)+"="+strings.Join(steps, sdd.StepSeparator),
			sdd.SkillEnvKey(k, sdd.EnvFieldMarker)+"="+s.RequiresMarker,
		)
	}
	if len(emitted) == 0 {
		return nil
	}

	out := make([]string, 0, 2+len(body))
	out = append(out,
		sdd.EnvEnabled+"="+strings.Join(emitted, ","),
		sdd.EnvWorkspaceHash+"="+hashWorkspace(workspace),
	)
	out = append(out, body...)
	return out
}

// resolveEnabledSkills returns the map of known skill keys to their
// registry entries, after filtering sddCfg through the registry to drop
// unknown keys and disabled entries. A per-skill steps: override from
// .toolbox.yaml replaces the registry's InstallSteps wholesale (#317):
// partial merge semantics would leave the user unable to drop a default
// step, which is exactly the gsd --claude --local case the override
// exists for.
func resolveEnabledSkills(sddCfg map[string]config.SDDSkill) map[string]sdd.Skill {
	out := make(map[string]sdd.Skill, len(sddCfg))
	for k, v := range sddCfg {
		if !v.Enabled {
			continue
		}
		s, ok := sdd.Lookup(k)
		if !ok {
			continue
		}
		if v.Steps != nil {
			s.InstallSteps = v.Steps
		}
		out[k] = s
	}
	return out
}

// hashWorkspace is the single source of the WorkspaceHashLen-byte hash
// derived from a workspace path. Shared by ContainerNameFor (container
// name suffix) and sddEnv (sentinel filename suffix the entrypoint reads
// to decide whether a skill is up to date). Computing it twice would
// drift if the slicing rule changed.
func hashWorkspace(workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return hex.EncodeToString(sum[:])[:WorkspaceHashLen]
}
