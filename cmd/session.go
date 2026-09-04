package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// newDockerClient is the client-construction seam. A var for the same reason
// as shellFn: startSession owns the ordering between the client, the mount
// stage's filesystem side effects and the attach, and that ordering is what a
// test needs to observe without a daemon.
var newDockerClient = container.NewClient

// sessionIntent describes the session a command wants opened, resolved from
// whatever that command's flags happen to be called. It is what the two
// interactive entry points — `shell` and `worktree create|open` — differ by:
// everything they *do* with it lives in startSession, so a third entry point
// adds a value, not a third copy of the assembly.
//
// Cobra keeps the flag globals. The intent is the seam: `shell` resolves its
// flag globals into one of these, and a test builds one directly.
type sessionIntent struct {
	// Plan is the caller's half of the session plan — workspace, named shell,
	// ports, loopback bridge, profile, peer opt-in, the optional worktree
	// branch — carried as sessionplan.PlanInput itself rather than copied
	// field by field into a parallel struct.
	//
	// Host, ImageDigest and ReloadFrom are the assembly's half: startSession
	// overwrites them unconditionally, so setting them here has no effect.
	// A `worktree` intent leaves Profile nil and takes Peer from config,
	// because that command exposes neither --profile/--share nor --peer;
	// declared here rather than left to a call site that omitted a field.
	Plan sessionplan.PlanInput
	// Reentry is the normalised form that gets the developer back after a
	// reload — `shell` renders the flags as typed, `worktree` normalises
	// create into open. Rendered by the caller because only the caller knows
	// its own flag set.
	Reentry []string
}

// startSession opens the session an intent describes: it consumes the reload
// handover, migrates legacy toolbox state, resolves the running image's repo
// digest, plans, seeds a worktree checkout, and hands the plan to the attach.
//
// This is the single composition root for "open a session". Both interactive
// entry points route through it, so the ordering invariants below are stated
// once instead of being re-honoured — and quietly diverging — per call site:
//
//   - the handover is consumed before the assembly does anything else, and so
//     before anything builds a container env: the host-to-host variable must
//     never reach a container;
//   - the plan is built after the Docker client, so a failed client init
//     (env parse / socket misconfig) leaves behind no mountplan.Plan
//     filesystem side effects under ~/.toolbox and the workspace;
//   - the signal context is installed last, immediately before the attach, so
//     a Ctrl+C during the mount stage still kills the process outright rather
//     than cancelling a context that stage does not read.
func startSession(in sessionIntent) error {
	if in.Plan.Cfg == nil {
		return errConfigNotLoaded
	}

	// Consumed and unset before anything builds a container env, so the
	// host-to-host handover never reaches a container. Nil on an ordinary
	// session start; unreadable is a hard error, never a silent degrade.
	reloadFrom, err := takeReloadHandover()
	if err != nil {
		return err
	}
	in.Plan.ReloadFrom = reloadFrom

	// The ambient host, read once and threaded from here on: the legacy-state
	// migration and the whole session plan address the same home instead of
	// each re-reading $HOME. Strict — the plan cannot be built without a home.
	host, err := fsx.CurrentHost()
	if err != nil {
		return err
	}
	in.Plan.Host = host

	// One-time relocation of toolbox-own state into the ~/.toolbox/toolbox
	// namespace. Best-effort: on failure CreateIfMissing rebuilds an empty
	// state dir and the pull cache regenerates, so warn instead of failing the
	// session. Every session migrates — the state root is shared, and a
	// worktree session that skipped this left the relocation to whichever
	// `toolbox shell` came next.
	if err := mountplan.MigrateLegacyToolboxState(host.Home); err != nil {
		fmt.Fprintf(os.Stderr, "toolbox: warning: %v\n", err)
	}

	if in.Plan.Cfg.Bridge != nil && *in.Plan.Cfg.Bridge {
		printBridgeTipIfNeeded(host)
	}

	cli, err := newDockerClient()
	if err != nil {
		return dockerClientErr(err)
	}
	defer cli.Close()

	// Resolve the running image's repo digest host-side and thread it to the
	// planner, which stamps it into the container as its record of what it was
	// created from — the baseline the update prefetch compares the local image
	// store against. Best-effort: an unresolvable digest (locally built image,
	// inspect failure, image not yet pulled) yields "" and the planner omits
	// the env entry. See session-reload.
	in.Plan.ImageDigest, _ = build.LocalRepoDigest(context.Background(), cli,
		build.ResolveImage(in.Plan.Cfg.Image, in.Plan.Cfg.RegistryMirror))

	plan, err := sessionplan.Plan(in.Plan)
	if err != nil {
		return err
	}

	// A worktree session carries gitignored per-repo state into the fresh
	// checkout. Both create and open re-seed, and the seeding is what makes
	// the agent start with the local specs and permission allowlist a
	// tracked-files-only checkout would miss.
	if wt := in.Plan.Worktree; wt != nil {
		seedWorktreeFiles(wt.RepoRoot, in.Plan.Workspace, in.Plan.Cfg.Worktree.Seed)
	}

	// Post-attach Ctrl+C reaches the container as a raw-mode byte; this signal
	// context only fires during pull/build or on external kill.
	ctx, stop := signalCtx()
	defer stop()

	return runSession(ctx, cli, plan, in.Reentry)
}
