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
	"github.com/filippolmt/toolbox/internal/localimage"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/proximo"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/teardown"
	"github.com/filippolmt/toolbox/internal/ui"
)

// execShellFn attaches an interactive shell to a container.
// Exposed as a package-level var so tests can substitute it.
var execShellFn = execShell

// startPrefetch launches the host-side update probe + prefetch for the
// lifetime of the attached session. A package-level var for the same reason
// as execShellFn: every lifecycle test would otherwise start a goroutine that
// talks to a registry.
var startPrefetch = imageprefetch.Start

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
	// very container being retired. The ordering inside is #834's: refresh and
	// prove the image, only then destroy.
	if plan.ReloadFrom != nil {
		if reloadErr := replaceForReload(ctx, cli, plan); reloadErr != nil {
			return nil, reloadErr
		}
	}

	inspectResult, inspectErr := cli.ContainerInspect(ctx, plan.ContainerName, client.ContainerInspectOptions{})
	inspect := inspectResult.Container
	op, opErr := runplan.Compute(inspect, inspectErr)
	if opErr != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", opErr)
	}

	if preflightErr := preflightHostConfig(ctx, cli, plan, inspect, op); preflightErr != nil {
		return nil, preflightErr
	}

	// Best-effort registry sync of the base image. Hard guarantee runs in
	// imageplan.Ensure inside createAndStart.
	imageplan.Refresh(ctx, cli, plan.Image)

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

	// Re-stamp the container's image-digest record from the store as it is
	// *now*: cmd resolved it before planning, which is before Refresh had the
	// chance to pull. Only the create path can be wrong — a connect or start
	// reads the digest off a container that already exists.
	restampImageDigest(ctx, cli, plan, baseImage, op)

	containerID, dispatchErr := dispatchOp(ctx, cli, plan, op)
	if dispatchErr != nil {
		return nil, dispatchErr
	}

	// Auto-remove on exit, unless another shell is still attached to the
	// same container. Policy + fresh-context handling owned by teardown.
	// The shell's own exit error wins over any cleanup error.
	//
	// Suppressed on the reload path — the named result *is* the flag. The
	// reload's teardown belongs to the next host process, after its
	// verify: destroying the container here would void that gate and leave a
	// failed re-exec with nothing to go back to.
	defer func() {
		if rl != nil {
			return
		}
		if cleanupErr := teardown.OnShellExit(cli, plan.ContainerName); cleanupErr != nil && err == nil {
			err = cleanupErr
		}
	}()

	// One detector, host-side, for as long as the shell is attached: the
	// probe that decides whether to pull is the same act that knows whether
	// the bytes landed, which is the fact the prompt banner states. Cancelled
	// with the session — an interrupted pull leaves no blob behind.
	prefetchCtx, stopPrefetch := context.WithCancel(ctx)
	defer stopPrefetch()
	if in, ok := prefetchInput(baseImage, plan, createdImageDigest(plan, inspect, op)); ok {
		startPrefetch(prefetchCtx, cli, in)
	}

	execCmd := plan.Cmd
	if plan.ExecCmd != nil {
		execCmd = plan.ExecCmd
	}
	execErr := execShellFn(ctx, cli, containerID, execCmd)

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
func prefetchInput(base sessionplan.Image, plan *sessionplan.SessionPlan, containerDigest string) (imageprefetch.Input, bool) {
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
	res, err := cli.ImageInspect(ctx, base.Ref)
	if err != nil {
		return
	}
	plan.Env = sessionplan.WithImageDigest(plan.Env, build.RepoDigest(base.Ref, res.RepoDigests))
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

// preflightHostConfig checks the parts of HostConfig that ContainerCreate
// fixes for the container's lifetime — published ports and the peer-messaging
// PID namespace — against what this session asks for. It runs before any image
// work: neither check depends on the image, and a known-fatal port conflict
// should not cost the user a registry pull or an overlay build first.
//
// Only a conflicting create is fatal. Publish ports are only ever bound by a
// create, so connecting to a live container publishes nothing new and can only
// be short of what was asked for; the peer namespace is the same story. Hence
// the split — a pre-flight error on the create path, a warning everywhere else.
func preflightHostConfig(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, inspect container.InspectResponse, op runplan.Op) error {
	if op.Action == runplan.ActionCreate {
		if len(plan.PortBindings) > 0 {
			return preflightPortConflicts(ctx, cli, plan)
		}
		return nil
	}

	if missing := sessionplan.MissingPublishPorts(plan.PortBindings, inspect); len(missing) > 0 {
		ui.Warning(formatPublishMismatch(plan, inspect, missing))
	}
	// One warning at a time: both prescribe the same targeted recreate, and a
	// container whose namespace is already wrong says nothing new by also
	// reporting the mount.
	if w := peerMismatchWarning(ctx, cli, plan, inspect); w != "" {
		ui.Warning(w)
	} else if w := peerSocketMountWarning(plan, inspect); w != "" {
		ui.Warning(w)
	}
	return nil
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
