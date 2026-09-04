// Package container owns the toolbox-container lifecycle: ensuring the
// image, creating/starting/stopping the per-workspace container, and
// attaching an interactive shell. Public seams: Shell, Stop, StopAll,
// NewClient. The Shell signature is (ctx, cli, *sessionplan.SessionPlan)
// — every input that used to be parsed inline (publish specs, image
// resolution, mount plan, container name, env, security opts) now rides
// in the typed plan composed by internal/sessionplan.Plan, and the
// runtime branch over the ContainerInspect result is the typed Op
// returned by internal/runplan.Compute and dispatched by dispatchOp.
//
// The orchestration Module lives in lifecycle.go (this file). The
// stop/remove + shell-exit policy is owned by internal/teardown; image
// readiness by internal/imageplan; host-process identity by
// internal/dockeridentity. The TTY/signal Adapter lives in attach.go
// and is kept as a separate file because its concern (raw mode, signal
// forwarding) is independent from Docker SDK orchestration.
package container

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/dockeridentity"
	"github.com/filippolmt/toolbox/internal/imageplan"
	"github.com/filippolmt/toolbox/internal/imageprefetch"
	"github.com/filippolmt/toolbox/internal/imagereclaim"
	"github.com/filippolmt/toolbox/internal/localimage"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/proximo"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/teardown"
	"github.com/filippolmt/toolbox/internal/ui"
	"github.com/filippolmt/toolbox/internal/version"
)

// execShellFn attaches an interactive shell to a container.
// Exposed as a package-level var so tests can substitute it.
var execShellFn = execShell

// The three seams into the image family are wrapped rather than assigned. Each
// leaf declares its own Docker surface, unexported in its own package, so a
// bare `var x = leaf.F` would take the type of that unnameable interface and
// no test in *this* package could write a stub for it. The wrapper restates
// the parameter as what this package holds anyway — the whole client, since
// internal/container is the Docker edge and does not narrow.
// → CONTEXT.md, Declared Docker Surface.

// startPrefetch launches the host-side update probe + prefetch for the
// lifetime of the attached session. A package-level var for the same reason
// as execShellFn: every lifecycle test would otherwise start a goroutine that
// talks to a registry.
var startPrefetch = func(ctx context.Context, cli client.APIClient, in imageprefetch.Input) {
	imageprefetch.Start(ctx, cli, in)
}

// reclaimImages is the Image Reclamation sweep for the lifetime of the
// attached session. A package-level var for the same reason as startPrefetch:
// every lifecycle test would otherwise have a second goroutine deleting images
// out of its own mock.
var reclaimImages = func(ctx context.Context, cli client.APIClient, in imagereclaim.Input) {
	imagereclaim.Start(ctx, cli, in)
}

// refreshAtStart is the shell-start image refresh, prompt and all. A
// package-level var for the same reason as startPrefetch: the tree behind it
// asks a question on a terminal, and what Shell owns is only what it does
// with the answer.
var refreshAtStart = func(ctx context.Context, cli client.APIClient, image sessionplan.Image, stateDir string, stake imageplan.Stake) imageplan.Outcome {
	return imageplan.RefreshAtStart(ctx, cli, image, stateDir, stake)
}

// refreshAnswer is what the start-up refresh settled: the outcome, and what a
// yes to it was staked on. offerRefresh establishes the two together and every
// consumer needs both, so they travel as one rather than as a pair of
// arguments. Outcome is embedded because the fields are read where they are
// produced-for — answer.Interrupted, answer.Synced — and a wrapper that made
// those a level deeper would buy nothing.
type refreshAnswer struct {
	imageplan.Outcome
	stake imageplan.Stake
}

// offerRefresh runs the shell-start image refresh — prompt and all — on the
// paths that can honour its answer, and records a "no" as the postponement it
// is.
//
// Two paths skip the act whole. A reload has already refreshed and proved the
// image in replaceForReload, and its premise is that the move onto the newer
// image was asked for — so there is nothing left to ask, and the same path is
// what an unattended trigger walks. A *running* container is the case where
// the answer could not be honoured: Docker cannot swap the image under it, and
// replacing it would end whatever else is attached to it — panes, agents, a
// sibling shell — none of which volunteered. The Idle Reload is the accepted
// answer there (ADR 0006), and the prefetch fetches behind either way.
//
// What is left is create and start, and the two differ in what a yes costs:
// on create it buys the image, on start it also spends the stopped container
// the developer was about to reuse. That is the Stake handed to the tree,
// which words the question and — the part no clock may decide — points the
// unanswered window. It is carried in the answer alongside the outcome, and it
// is the only place this branch is classified: honouring a yes reads the stake
// rather than re-deriving what a yes meant from the op.
//
// The stamp a decline leaves is the moment, and it is what arms the Idle
// Reload for this session alone — even where that is otherwise off, because
// *not now* is a request to postpone rather than to refuse. See CONTEXT.md's
// Idle Reload and Session Quiescence entries for what reads it. Best-effort:
// an unwritable state mount costs the postponement, not the shell, and a
// stamp older than the container it names is inert by construction, so
// nothing has to clear it.
func offerRefresh(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, op runplan.Op) refreshAnswer {
	stake := imageplan.StakeDownload
	if op.Action == runplan.ActionStart {
		stake = imageplan.StakeRecreate
	}
	if plan.ReloadFrom != nil || op.Action == runplan.ActionConnect {
		return refreshAnswer{stake: stake}
	}
	refresh := refreshAtStart(ctx, cli, plan.Image, plan.StateDir, stake)
	if refresh.Declined && plan.StateDir != "" {
		if err := reload.TouchDeclined(plan.StateDir, plan.ContainerName); err != nil {
			ui.Warning("start-up refresh: cannot record the postponement: " + err.Error())
		}
	}
	return refreshAnswer{Outcome: refresh, stake: stake}
}

// replaceForRefresh honours a yes given on the start branch, by destroying the
// stopped container the question was about so the create that follows can take
// its name and its place. Nothing new is needed to finish the job: the create
// it returns already pulls, creates and starts.
//
// Returns the op to act on, which is the one the second read below settles —
// never the one the question was asked about. That op carries an ID, and every
// call the caller still has to make takes it: a container the question was
// about may have been replaced while the question stood, and its ID then names
// nothing.
//
// Everything that can still fail runs before anything is destroyed, which is
// the property the whole act rests on: the pull is already behind us
// (`Outcome.Accepted` is a pull that landed), the overlay is built by the
// caller before it gets here, and preflightCreate is the last of the three —
// a host port another container holds can never be bound by the create that
// follows, and learning that after the removal would cost the developer the
// container they were asked about and leave them with the daemon's opaque
// refusal in its place.
//
// Then the container is read a second time, because the question held the
// terminal for seconds and the answer describes what was true when it was
// asked. What the second read finds decides:
//
//   - still stopped: the case the developer answered about — removed.
//   - already gone (a `toolbox stop`, a `docker rm`): the name is free, which
//     is all the removal was for.
//   - running again: a sibling shell started it while the question stood, and
//     force-removing it now would end a session whose owner never volunteered
//     — the collateral a running container is never asked about in the first
//     place. This session attaches to it instead.
//   - unreadable: nothing is destroyed on an answer the daemon would not give.
//
// asked is the op the question was put about, returned unchanged on the one
// branch that learns nothing: a daemon that would not answer leaves the start
// exactly as it was.
func replaceForRefresh(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, asked runplan.Op) (runplan.Op, error) {
	if preflightErr := preflightCreate(ctx, cli, plan); preflightErr != nil {
		// Zero rather than the asked op: the caller aborts on an error, and
		// must not be handed an op to act on instead.
		return runplan.Op{}, preflightErr
	}

	res, inspectErr := cli.ContainerInspect(ctx, plan.ContainerName, client.ContainerInspectOptions{})
	fresh, computeErr := runplan.Compute(res.Container, inspectErr)
	switch {
	case computeErr != nil:
		ui.Warning("start-up refresh: cannot re-read " + plan.ContainerName + " before replacing it (" +
			computeErr.Error() + ") — starting it as it is")
		return asked, nil
	case fresh.Action == runplan.ActionConnect:
		ui.Warning("start-up refresh: another shell started " + plan.ContainerName +
			" while the question stood — keeping it, and this session joins it")
		// fresh, not asked: that shell may have recreated the container rather
		// than started it, and the ID the question was about then names
		// nothing the daemon still has.
		return fresh, nil
	case fresh.Action == runplan.ActionStart:
		ui.Info("Recreating container " + plan.ContainerName + " on the newer image...")
		if removeErr := removeAndWait(ctx, cli, plan.ContainerName, "start-up refresh"); removeErr != nil {
			return runplan.Op{}, removeErr
		}
	}
	// Reached by the removal above and by a container someone else had already
	// removed: either way the container the banner's cache was published about
	// is gone, and left in place that cache would announce an update this
	// session has adopted. Same call, same reason, as the reload's own.
	imageprefetch.ClearResult(plan.StateDir)
	return runplan.Op{Action: runplan.ActionCreate}, nil
}

// formatPublishMismatch builds the warning string emitted when a reused
// container does not have every port the user asked for. Returns "" when
// every wanted port is already bound on the existing container, signalling
// the caller to stay quiet. sessionplan.MissingPublishPorts produces the
// typed missing-list; lifecycle composes the human-readable message.
func formatPublishMismatch(plan *sessionplan.SessionPlan, inspect container.InspectResponse, missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)

	wantedPorts := make([]string, 0, len(plan.PortBindings))
	for port := range plan.PortBindings {
		wantedPorts = append(wantedPorts, port.String())
	}
	sort.Strings(wantedPorts)

	actual := []string{}
	if inspect.HostConfig != nil {
		for port := range inspect.HostConfig.PortBindings {
			actual = append(actual, port.String())
		}
		sort.Strings(actual)
	}
	actualMsg := "none"
	if len(actual) > 0 {
		actualMsg = strings.Join(actual, ", ")
	}

	return fmt.Sprintf(
		"publish mismatch on existing container: wanted [%s], container has [%s], missing [%s] — run 'toolbox stop' then retry to apply",
		strings.Join(wantedPorts, ", "), actualMsg, strings.Join(missing, ", "),
	)
}

// preflightPortConflicts fails the create path when a wanted host port is
// already published by another container. Port bindings are fixed at
// ContainerCreate, so the alternative is the daemon's own opaque "Bind for
// 127.0.0.1:8877 failed: port is already allocated" — which names neither the
// port set nor the holder. Reading the occupied set is the Docker-edge half of
// the split; sessionplan.ConflictingPublishPorts stays pure.
//
// Best-effort by design: a listing failure, or a port held by a non-Docker
// host process, waves the create through to the daemon's error rather than
// blocking a shell on a check that cannot see everything.
func preflightPortConflicts(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) error {
	list, err := cli.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil
	}

	occupied := map[string]string{}
	workspaces := map[string]string{}
	for _, c := range list.Items {
		if len(c.Names) == 0 {
			continue
		}
		name := containerName(c)
		workspaces[name] = workspaceOf(c)
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			occupied[fmt.Sprintf("%d/%s", p.PublicPort, p.Type)] = name
		}
	}

	conflicts := sessionplan.ConflictingPublishPorts(plan.PortBindings, occupied)
	if len(conflicts) == 0 {
		return nil
	}
	return errors.New(formatPortConflict(conflicts, workspaces))
}

// formatPortConflict renders the pre-flight failure, grouped by holder so a
// published range collapses to one line per container. A toolbox holder earns
// the extra suggestion: credentials live on shared ~/.toolbox bind mounts, so
// the login can simply be finished inside that session — deliberately not
// offering `toolbox stop` there, which would kill someone else's shell.
// workspaces maps holder name to workspaceOf's output ("-" when unknown).
func formatPortConflict(conflicts []sessionplan.PortConflict, workspaces map[string]string) string {
	byHolder := map[string][]string{}
	holders := []string{}
	for _, c := range conflicts {
		if _, seen := byHolder[c.Holder]; !seen {
			holders = append(holders, c.Holder)
		}
		byHolder[c.Holder] = append(byHolder[c.Holder], c.Port)
	}
	sort.Strings(holders)

	var b strings.Builder
	b.WriteString("cannot publish ports already bound on this host (bindings are fixed at container creation):")
	for _, holder := range holders {
		fmt.Fprintf(&b, "\n  %s held by container %q", strings.Join(byHolder[holder], ", "), holder)
		if ws := workspaces[holder]; ws != "-" {
			fmt.Fprintf(&b, " (workspace %s)", ws)
		}
		if sessionplan.IsToolboxContainerName(holder) {
			b.WriteString("\n    that is another toolbox: run the login inside it — credentials are shared through the ~/.toolbox mounts")
		}
	}
	return b.String()
}

// --- Lifecycle ---

// NewClient returns a Docker client configured from the environment.
func NewClient() (client.APIClient, error) {
	return client.New(client.FromEnv)
}

// Shell manages the container lifecycle and attaches a zsh session.
// The workspace host path is always mounted at /workspace and used as the
// WorkingDir. When the attached shell exits the container is stopped and
// removed — all persistent state lives on bind-mounted volumes under
// ~/.toolbox/ (creds, shell history, caches), so nothing is lost by trashing
// the container itself.
//
// State machine — dispatched by runplan.Compute on the ContainerInspect result:
//   - ActionConnect -> exec into the running container
//   - ActionStart   -> start the stopped container, then exec
//   - ActionCreate  -> ensure image, create + start + exec
//
// Image ensure: the image ref defaults to the canonical GHCR tag but can be
// relocated opt-in (config Image / RegistryMirror). Refresh attempts a
// best-effort registry sync steered by the pull policy (auto/always/never),
// Ensure (called from createAndStart) hard-requires the image be present
// locally — `toolbox build` is the explicit path to a local rebuild.
//
// Multi-session caveat: if two terminals open a shell into the same
// workspace, both attach to the same container. When either exits the
// container is removed and the other session dies with it.
//
// Reload: a non-nil first return means the attached shell asked to move onto a
// newer image (docs/session-reload.md). Shell does not perform the re-exec —
// internal/container must not replace the host process, and it has no business
// constructing argv — so it hands the typed request back and cmd tail-calls.
// The cycle lives in the sequence of processes, not inside any one of them.
func Shell(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) (rl *reload.From, err error) {
	for _, w := range plan.Warnings {
		ui.Warning("mount skipped: " + w)
	}

	// A reload arrives owning a container it must replace, and it must do so
	// before the inspect below — which would otherwise compute a connect to the
	// very container being retired. A no-op for every other session.
	if reloadErr := replaceForReload(ctx, cli, plan); reloadErr != nil {
		return nil, reloadErr
	}

	// What this session does to the container, decided before any image work.
	inspect, op, resolveErr := resolveOp(ctx, cli, plan)
	if resolveErr != nil {
		return nil, resolveErr
	}

	// Best-effort registry sync of the base image, which on the one case that
	// is not already settled *asks* — see the Image Plan's own tree, and
	// offerRefresh for the two paths that skip the act whole. Hard guarantee
	// runs in imageplan.Ensure inside createAndStart. Whether the store was
	// established current is threaded to the prefetch below: a synchronous
	// probe is a probe, and the background poller must not re-ask the question
	// this just answered.
	answer := offerRefresh(ctx, cli, plan, op)
	if answer.Interrupted {
		// A ctrl+c at the start-up prompt. The prompt has already re-raised
		// the signal raw mode swallowed, but the answer is reported rather
		// than left to the signal alone: whether cmd's signal context has
		// cancelled yet is a matter of scheduling, and a session must not be
		// built in the window where it has not.
		return nil, errors.New("interrupted")
	}

	// Local overlay: when ~/.toolbox/Dockerfile exists, build a derived
	// `:local` image on top of the freshened base and run the shell from it.
	// Passthrough (base unchanged) when the file is absent; fail loud on a
	// build error so the shell never silently starts from the wrong image.
	// The returned `:local` carries pull policy "never", so the later
	// Ensure/Refresh for the create path never touch a registry for it.
	baseImage := plan.Image
	image, overlayErr := localimage.Ensure(ctx, cli, plan.Image, plan.OverlayDockerfile)
	if overlayErr != nil {
		return nil, overlayErr
	}
	plan.Image = image

	// After the overlay, not before — see replaceIfRefreshAccepted for what that
	// ordering protects and for why a yes at the recreate stake spends the
	// container.
	op, replaceErr := replaceIfRefreshAccepted(ctx, cli, plan, op, answer)
	if replaceErr != nil {
		return nil, replaceErr
	}

	// Now that the branch has settled: what this session asked for and cannot
	// have on the container it is joining. Silent on a create, which is
	// getting exactly what it asked for — including a create this refresh has
	// just turned a start into.
	warnReattachMismatch(ctx, cli, plan, inspect, op)

	// Re-stamp the container's image-digest record from the store as it is
	// *now*: cmd resolved it before planning, which is before Refresh had the
	// chance to pull. Only the create path can be wrong — a connect or start
	// reads the digest off a container that already exists.
	restampImageDigest(ctx, cli, plan, baseImage, op)

	containerID, dispatchErr := dispatchOp(ctx, cli, plan, op)
	if dispatchErr != nil {
		return nil, dispatchErr
	}

	// Auto-remove on exit, suppressed on the reload path — see shellTeardown.
	// Deferred from here, so it covers every return below this point and none
	// of the failures above it, which have no container to remove.
	defer func() { err = shellTeardown(cli, plan.ContainerName, rl != nil, err) }()

	// The digest this session actually runs, read off the container on the
	// connect path and off the re-stamped plan on the create path. Both
	// background acts are anchored to it: one asks whether the store has moved
	// past it, the other must never reclaim it.
	sessionDigest := createdImageDigest(plan, inspect, op)

	stopPrefetch := beginPrefetch(ctx, cli, plan, baseImage, sessionDigest, answer.Synced)
	defer stopPrefetch()

	stopReclaim := beginReclaim(ctx, cli, plan, baseImage, sessionDigest)
	defer stopReclaim()

	execErr := execShellFn(ctx, cli, containerID, plan.EffectiveCmd())

	// The shell writes a marker on its way out and exits; this is the moment
	// the host already decides between teardown and something else, so it is
	// the moment the marker is read. Read-and-delete, and unconditionally:
	// a session that asked for a reload and then died leaves the marker on a
	// mount every later session shares, where it would fire a reload nobody
	// asked for at the next ordinary exit.
	requested := takeReloadRequest(plan)
	if execErr != nil {
		return nil, execErr
	}
	return requested, nil
}

// resolveOp settles what this session is going to do to the container: read
// what is actually there, compute the action from it, and run the half of the
// host-config check that can fail. Destroys nothing — a reload's own container
// is retired by the caller, before this reads anything.
//
// The host-config check is create-only and runs here so that a known-fatal
// port conflict costs no pull. The half of that check which only warns runs
// once the branch has settled — see warnReattachMismatch.
func resolveOp(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) (container.InspectResponse, runplan.Op, error) {
	inspectResult, inspectErr := cli.ContainerInspect(ctx, plan.ContainerName, client.ContainerInspectOptions{})
	inspect := inspectResult.Container
	op, opErr := runplan.Compute(inspect, inspectErr)
	if opErr != nil {
		return container.InspectResponse{}, runplan.Op{}, fmt.Errorf("failed to inspect container: %w", opErr)
	}

	if op.Action == runplan.ActionCreate {
		if preflightErr := preflightCreate(ctx, cli, plan); preflightErr != nil {
			return container.InspectResponse{}, runplan.Op{}, preflightErr
		}
	}
	return inspect, op, nil
}

// replaceIfRefreshAccepted destroys the stopped container when the developer
// said yes to a refresh that staked it, and reports the op that follows.
//
// A yes at the recreate stake was a yes to this: the stopped container goes,
// and the branch becomes the create that knows how to build its replacement.
// The stake is what is read, not the op — it is what the question was put at,
// and it is where that branch was already decided. Only an answer counts:
// Accepted is the developer's own and a pull that landed, never a policy's and
// never an elapsed window's, because no container may be spent by anything
// else.
//
// Called after the overlay, not before: a `:local` build that will not build is
// the other way this start can still fail, and failing it once the container is
// gone would leave the developer with neither a session nor the container they
// were asked about.
func replaceIfRefreshAccepted(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, op runplan.Op, answer refreshAnswer) (runplan.Op, error) {
	if !answer.Accepted || answer.stake != imageplan.StakeRecreate {
		return op, nil
	}
	return replaceForRefresh(ctx, cli, plan, op)
}

// shellTeardown is the exit decision Shell defers: auto-remove the container
// unless another shell is still attached to it, with the shell's own exit error
// winning over any cleanup error. Policy and fresh-context handling are owned
// by teardown.
//
// handingOff suppresses the removal, which is what the reload path passes: the
// reload's teardown belongs to the next host process, after its verify, and
// destroying the container here would void that gate and leave a failed
// re-exec with nothing to go back to. Taken as the condition rather than as
// the reload request itself, so the exit policy needs to know nothing about
// what a reload is.
func shellTeardown(cli client.APIClient, name string, handingOff bool, err error) error {
	if handingOff {
		return err
	}
	if cleanupErr := teardown.OnShellExit(cli, name); cleanupErr != nil && err == nil {
		return cleanupErr
	}
	return err
}

// beginPrefetch starts the host-side update probe for as long as the shell is
// attached, and returns the func that stops it. One detector: the probe that
// decides whether to pull is the same act that knows whether the bytes landed,
// which is the fact the prompt banner states. The returned stop is always
// live, refusal included, so the caller defers one thing unconditionally —
// cancelling it with the session leaves no blob behind, an interrupted pull
// expiring on its own.
func beginPrefetch(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, base sessionplan.Image, containerDigest string, startSynced bool) func() {
	prefetchCtx, stop := context.WithCancel(ctx)
	if in, ok := prefetchInput(base, plan, containerDigest, startSynced); ok {
		startPrefetch(prefetchCtx, cli, in)
	}
	return stop
}

// beginReclaim starts the Image Reclamation sweep for as long as the shell is
// attached, and returns the func that stops it. Called from Shell *after*
// dispatchOp and never before: the ordering is the design, because only once
// this workspace's container exists and references the new image is every
// surviving reference to the old one somebody else's real reference. Run
// earlier and the removal is guaranteed to be refused — the session doing the
// reclaiming would itself be the last holder.
//
// The ref tracked is the *base*, never the `:local` overlay tag: the overlay is
// built rather than pulled, so it carries no repo digest at all, while the base
// underneath it is what gains a generation per merge. Gated by the plan's
// resolved `image_reclaim`; the returned stop is always live, so the caller
// defers one thing unconditionally.
func beginReclaim(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, base sessionplan.Image, sessionDigest string) func() {
	reclaimCtx, stop := context.WithCancel(ctx)
	if plan.ReclaimImages {
		reclaimImages(reclaimCtx, cli, imagereclaim.Input{Ref: base.Ref, KeepDigest: sessionDigest})
	}
	return stop
}

// prefetchInput assembles the update prefetch's input and reports whether the
// act runs at all. Two refusals, both settled on the map:
//
//   - pull: never means "do not talk to the registry", and a probe talks to
//     the registry — so it silences probe, prefetch and banner as one act.
//   - TOOLBOX_NO_UPDATE_CHECK silences the host half here and the render half
//     in zshrc. Honoured only in its `env:` passthrough form, which is what
//     reaches the composed plan env; an export typed inside a live shell still
//     stops the rendering, which is what that variable is now for.
//
// The image tracked is the *base* ref, never the `:local` overlay tag: the
// overlay is built, not pulled, and it is the base moving underneath it that
// a reload would adopt.
func prefetchInput(base sessionplan.Image, plan *sessionplan.SessionPlan, containerDigest string, startSynced bool) (imageprefetch.Input, bool) {
	if base.PullPolicy == config.PullNever {
		return imageprefetch.Input{}, false
	}
	if sessionplan.EnvValue(plan.Env, sessionplan.NoUpdateCheckEnv) != "" {
		return imageprefetch.Input{}, false
	}
	return imageprefetch.Input{
		Ref:             base.Ref,
		ContainerDigest: containerDigest,
		StateDir:        plan.StateDir,
		StartSynced:     startSynced,
		CLIVersion:      version.Version,
	}, true
}

// restampImageDigest rewrites the plan's TOOLBOX_IMAGE_DIGEST to the repo
// digest the base image carries in the local store right now — which is what
// the container about to be created actually runs. Without it, a shell opened
// on the same morning as a release is stamped with the digest that Refresh
// has just superseded, and the update prefetch reports it behind an image it
// is already running. Best-effort and create-only: an unreadable store leaves
// the plan's own answer in place, and a connect/start reads the container.
func restampImageDigest(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, base sessionplan.Image, op runplan.Op) {
	if op.Action != runplan.ActionCreate {
		return
	}
	// Only a store that did not answer leaves the plan's own value standing;
	// a store that answers with no digest is a local build, and stamping its
	// empty answer is what keeps the prefetch from claiming this session is
	// behind an image it cannot read a digest for.
	digest, answered := build.LocalRepoDigest(ctx, cli, base.Ref)
	if !answered {
		return
	}
	plan.Env = sessionplan.WithImageDigest(plan.Env, digest)
}

// createdImageDigest returns the repo digest the attached container was
// created from — the baseline half of "is this session behind the local
// store?". Read off the container rather than recomputed, so it is right on
// the connect path too, where this process never resolved a digest of its
// own. On the create path the container does not exist yet and the plan env
// about to be written is the same answer by construction.
func createdImageDigest(plan *sessionplan.SessionPlan, inspect container.InspectResponse, op runplan.Op) string {
	if op.Action == runplan.ActionCreate {
		return sessionplan.EnvValue(plan.Env, sessionplan.ImageDigestEnv)
	}
	if inspect.Config == nil {
		// An inspect that carries no Config says nothing about the container.
		// Substituting the plan's digest would answer with what *this* process
		// resolved — the very value that makes the comparison come out equal,
		// hiding a real update instead of admitting the baseline is unknown.
		return ""
	}
	return sessionplan.EnvValue(inspect.Config.Env, sessionplan.ImageDigestEnv)
}

// preflightCreate is the half of the host-config check that can fail: a host
// port another container already publishes can never be bound by the create
// that follows, and the daemon's own refusal names neither the port set nor
// the holder. Only a create can conflict — connecting to or starting an
// existing container publishes nothing new — so this is the create path's
// gate, called before any image work by Shell and again by
// replaceForRefresh, which turns a start into a create.
//
// Deliberately before the start-up refresh in Shell: a known-fatal conflict
// must not cost the user a registry pull or an overlay build first. Skipped
// when this session publishes nothing, which is the only reason the check ever
// reaches the daemon.
func preflightCreate(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) error {
	if len(plan.PortBindings) == 0 {
		return nil
	}
	return preflightPortConflicts(ctx, cli, plan)
}

// warnReattachMismatch is the half that cannot fail: what this session asked
// for and cannot have, because `ContainerCreate` fixed it for the lifetime of
// the container being joined — published ports and the peer-messaging PID
// namespace. Both prescribe the same targeted recreate, and at most one is
// emitted: a container whose namespace is already wrong says nothing new by
// also reporting the mount.
//
// Read after the start-up refresh has settled the branch, not before, and the
// create guard is why: on the start branch an accepted recreate *is* the
// `toolbox stop` and retry these warnings prescribe, applied — so warning
// first would prescribe a fix seconds before performing it, about a container
// that no longer exists.
func warnReattachMismatch(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, inspect container.InspectResponse, op runplan.Op) {
	if op.Action == runplan.ActionCreate {
		return
	}
	if missing := sessionplan.MissingPublishPorts(plan.PortBindings, inspect); len(missing) > 0 {
		ui.Warning(formatPublishMismatch(plan, inspect, missing))
	}
	if w := peerMismatchWarning(ctx, cli, plan, inspect); w != "" {
		ui.Warning(w)
	} else if w := peerSocketMountWarning(plan, inspect); w != "" {
		ui.Warning(w)
	}
}

// dispatchOp executes the runplan.Op against the Docker daemon and returns
// the resulting container ID. Each Action maps to a distinct Docker-edge
// sequence; the pure decision lives in runplan.Compute and is observed via
// the typed Op rather than being re-derived from inspect data here.
func dispatchOp(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, op runplan.Op) (string, error) {
	switch op.Action {
	case runplan.ActionConnect:
		ui.Info("Connecting to running container " + plan.ContainerName + "...")
		return op.ExistingID, nil

	case runplan.ActionStart:
		ui.Info("Starting stopped container " + plan.ContainerName + "...")
		// The container's binds are fixed, but the volume behind them is not:
		// the documented cleanup is `docker volume rm toolbox-cc-socks`, and
		// starting against a missing one lets the daemon recreate it
		// root-owned, which Claude Code answers by falling back to a private
		// directory in silence. Best-effort — a failure here costs peer
		// messaging, not the shell.
		if plan.PidMode != "" {
			if volErr := ensurePeerSocketVolume(ctx, cli, plan.Image); volErr != nil {
				ui.Warning(peerWarnPrefix + volErr.Error() + " — this session may reach no peer")
			}
		}
		if _, startErr := cli.ContainerStart(ctx, op.ExistingID, client.ContainerStartOptions{}); startErr != nil {
			return "", fmt.Errorf("failed to start container: %w", startErr)
		}
		return op.ExistingID, nil

	case runplan.ActionCreate:
		return createAndStart(ctx, cli, plan)

	default:
		return "", fmt.Errorf("unknown runplan action: %s", op.Action)
	}
}

// createAndStart owns the not-found path: ensure the image is present,
// create the container from the SessionPlan, start it, return its ID.
func createAndStart(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) (string, error) {
	if ensureErr := imageplan.Ensure(ctx, cli, plan.Image); ensureErr != nil {
		return "", ensureErr
	}

	// Resolved before the bind set is flattened because an unusable anchor or
	// socket volume degrades the session to its own PID namespace, without the
	// shared socket mount, rather than failing it.
	pidMode, planBinds := ensurePeerRuntime(ctx, cli, plan)

	// One pass, two slices: the daemon wants flattened specs (HostConfig.Binds
	// below), dockeridentity wants the in-container targets it keys group-add
	// on. Reading b.Target here rather than re-parsing the spec keeps that
	// decision on the typed field.
	binds := make([]string, len(planBinds))
	bindTargets := make([]string, len(planBinds))
	for i, b := range planBinds {
		binds[i] = b.String()
		bindTargets[i] = b.Target
	}
	identity := dockeridentity.Resolve(bindTargets)

	// proximo: pin the host-routed `.test` names at the host-gateway so they
	// resolve to the host where Traefik publishes :443 instead of the
	// container's own loopback. Discovery needs the Docker client, so it lives
	// here at the create edge rather than in the pure session planner.
	extraHosts := plan.ExtraHosts
	if plan.Proximo {
		extraHosts = augmentProximoHosts(ctx, cli, plan.ExtraHosts)
	}

	ui.Info("Creating container " + plan.ContainerName + "...")
	resp, createErr := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: plan.ContainerName,
		Config: &container.Config{
			Image:        plan.Image.Ref,
			Tty:          true,
			OpenStdin:    true,
			Cmd:          plan.Cmd,
			WorkingDir:   plan.WorkingDir,
			User:         identity.UserSpec,
			ExposedPorts: plan.ExposedPorts,
			Env:          plan.Env,
		},
		HostConfig: &container.HostConfig{
			Binds:        binds,
			GroupAdd:     identity.GroupAdd,
			PortBindings: plan.PortBindings,
			SecurityOpt:  plan.SecurityOpt,
			ExtraHosts:   extraHosts,
			// AutoRemove offloads the (slow on macOS Docker Desktop) bind-mount
			// teardown to the daemon: on shell exit we only kill the container,
			// and the daemon's auto-remove worker deletes it asynchronously so
			// the user's prompt is not blocked on the unmount. See teardown.
			AutoRemove: true,
			// Empty for an ordinary session; `container:<anchor>` when the
			// session opted into cross-container peer messaging.
			PidMode: container.PidMode(pidMode),
		},
	})
	if createErr != nil {
		return "", fmt.Errorf("failed to create container: %w", createErr)
	}

	if _, startErr := cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); startErr != nil {
		return "", fmt.Errorf("failed to start container: %w", startErr)
	}
	ui.Success("Container started")
	return resp.ID, nil
}

// augmentProximoHosts appends the proximo-routed hostnames (discovered from
// the proximo.hosts label on running containers, each pinned to host-gateway)
// to the plan's base ExtraHosts. A Docker error warns and returns base
// unchanged; an empty result returns base silently (no routed stack right now)
// so a missing/stopped proximo stack degrades to "names unreachable" rather
// than failing — or spamming — the shell.
func augmentProximoHosts(ctx context.Context, cli client.APIClient, base []string) []string {
	args := make(client.Filters).Add("label", proximo.HostsLabel)
	list, err := cli.ContainerList(ctx, client.ContainerListOptions{Filters: args})
	if err != nil {
		ui.Warning("proximo: host discovery failed, .test names may be unreachable: " + err.Error())
		return base
	}
	var labels []string
	for _, c := range list.Items {
		if v := c.Labels[proximo.HostsLabel]; v != "" {
			labels = append(labels, v)
		}
	}
	hosts := proximo.ExtraHosts(labels)
	if len(hosts) == 0 {
		// No routed container right now (stack down, or auto-enabled on a host
		// where proximo is installed but idle) — stay silent rather than warn
		// on every shell.
		return base
	}
	ui.Infof("proximo: routing %d host(s) via host-gateway", len(hosts))
	out := make([]string, 0, len(base)+len(hosts))
	out = append(out, base...)
	out = append(out, hosts...)
	return out
}

// Stop stops and removes the toolbox container associated with the workspace.
func Stop(ctx context.Context, cli client.APIClient, workspace string) error {
	return teardown.StopOne(ctx, cli, sessionplan.ContainerNameFor(workspace, ""), teardown.DefaultStopGrace)
}

// StopByName stops and removes the toolbox container for a named shell.
//
// An argument that is already a full toolbox container name (as printed by
// `toolbox list`, or by the peer-mismatch warning) is used verbatim: it is the
// only handle on a container whose name carries a discriminator the CLI cannot
// re-derive from a shell name — a peer opt-in, a profile — and the alternative
// there is `toolbox stop --all`, which takes every sibling shell down with it.
// A named shell whose own name starts with "toolbox-" is the one ambiguous
// case, and it resolves to the container name as typed.
func StopByName(ctx context.Context, cli client.APIClient, name string) error {
	target := sessionplan.NamedContainerName(name)
	if strings.HasPrefix(name, sessionplan.ContainerNamePrefix) {
		target = name
	}
	return teardown.StopOne(ctx, cli, target, teardown.DefaultStopGrace)
}

// StopAll stops and removes every toolbox-managed container on the host.
// Matches the "toolbox-" prefix as well as the legacy singleton name "toolbox".
// Failures on a single container don't short-circuit the rest — partial
// cleanup beats fail-fast when `--all` is meant to be a bulk sweep.
func StopAll(ctx context.Context, cli client.APIClient) error {
	items, err := toolboxContainers(ctx, cli)
	if err != nil {
		return err
	}

	found := 0
	var errs []error
	for _, c := range items {
		if err := teardown.StopOne(ctx, cli, containerName(c), teardown.DefaultStopGrace); err != nil {
			errs = append(errs, err)
			continue
		}
		found++
	}
	if found == 0 && len(errs) == 0 {
		ui.Warning("No toolbox containers found")
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// toolboxContainers lists every toolbox-managed container on the host (all
// states). It centralizes the daemon-side name pre-filter, the nameless-entry
// guard, and the authoritative IsToolboxContainerName test so StopAll and List
// agree on the container set at the query level — not just the name predicate.
func toolboxContainers(ctx context.Context, cli client.APIClient) ([]container.Summary, error) {
	args := make(client.Filters).Add("name", "toolbox")
	list, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var out []container.Summary
	for _, c := range list.Items {
		if len(c.Names) == 0 {
			continue
		}
		if !sessionplan.IsToolboxContainerName(containerName(c)) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// containerName returns the container's primary name without Docker's leading
// "/". Callers must guarantee Names is non-empty: toolboxContainers filters
// nameless entries out for List/StopAll, preflightPortConflicts guards its own
// raw ContainerList the same way.
func containerName(c container.Summary) string {
	return strings.TrimPrefix(c.Names[0], "/")
}

// Item is one row of the host's toolbox-container inventory. Workspace is the
// host path bind-mounted at /workspace, or "-" when no such bind exists
// (e.g. a legacy container). Status is the Docker human status string
// ("Up 2 hours", "Exited (0) 3 minutes ago").
type Item struct {
	Name      string
	Workspace string
	Status    string
}

// List returns every toolbox-managed container on the host (all states),
// sorted by name. It reuses the same name predicate as StopAll so the two
// agree on what counts as a toolbox container.
func List(ctx context.Context, cli client.APIClient) ([]Item, error) {
	summaries, err := toolboxContainers(ctx, cli)
	if err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(summaries))
	for _, c := range summaries {
		// The anchor carries the toolbox- prefix so StopAll sweeps it up, but
		// it is not a shell anyone opened — listing it as one would invite a
		// `toolbox stop` on infrastructure.
		if containerName(c) == sessionplan.PeerAnchorContainerName {
			continue
		}
		items = append(items, Item{
			Name:      containerName(c),
			Workspace: workspaceOf(c),
			Status:    c.Status,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

// workspaceOf extracts the host path bound at /workspace from a container
// summary, or "-" when no such bind is present.
func workspaceOf(c container.Summary) string {
	for _, m := range c.Mounts {
		if m.Destination == mountplan.WorkspaceTarget {
			return m.Source
		}
	}
	return "-"
}
