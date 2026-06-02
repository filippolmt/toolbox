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

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"github.com/filippolmt/toolbox/internal/dockeridentity"
	"github.com/filippolmt/toolbox/internal/imageplan"
	"github.com/filippolmt/toolbox/internal/proximo"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/teardown"
	"github.com/filippolmt/toolbox/internal/ui"
)

// execShellFn attaches an interactive shell to a container.
// Exposed as a package-level var so tests can substitute it.
var execShellFn = execShell

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
		wantedPorts = append(wantedPorts, string(port))
	}
	sort.Strings(wantedPorts)

	actual := []string{}
	if inspect.HostConfig != nil {
		for port := range inspect.HostConfig.PortBindings {
			actual = append(actual, string(port))
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

// --- Lifecycle ---

// NewClient returns a Docker client configured from the environment.
func NewClient() (client.APIClient, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
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
// Image ensure: every user runs the canonical GHCR image; Refresh attempts
// a best-effort registry sync, Ensure (called from createAndStart) hard-
// requires the image be present locally — `toolbox build` is the explicit
// path to a local rebuild.
//
// Multi-session caveat: if two terminals open a shell into the same
// workspace, both attach to the same container. When either exits the
// container is removed and the other session dies with it.
func Shell(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) (err error) {
	for _, w := range plan.Warnings {
		ui.Warning("mount skipped: " + w)
	}

	// Best-effort registry sync. Hard guarantee runs in imageplan.Ensure
	// inside createAndStart.
	imageplan.Refresh(ctx, cli, plan.Image)

	inspect, inspectErr := cli.ContainerInspect(ctx, plan.ContainerName)
	op, opErr := runplan.Compute(inspect, inspectErr)
	if opErr != nil {
		return fmt.Errorf("failed to inspect container: %w", opErr)
	}

	if op.Action != runplan.ActionCreate && len(plan.PortBindings) > 0 {
		if missing := sessionplan.MissingPublishPorts(plan.PortBindings, inspect); len(missing) > 0 {
			ui.Warning(formatPublishMismatch(plan, inspect, missing))
		}
	}

	containerID, dispatchErr := dispatchOp(ctx, cli, plan, op)
	if dispatchErr != nil {
		return dispatchErr
	}

	// Auto-remove on exit, unless another shell is still attached to the
	// same container. Policy + fresh-context handling owned by teardown.
	// The shell's own exit error wins over any cleanup error.
	defer func() {
		if cleanupErr := teardown.OnShellExit(cli, plan.ContainerName); cleanupErr != nil && err == nil {
			err = cleanupErr
		}
	}()

	return execShellFn(ctx, cli, containerID, plan.Cmd)
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
		if startErr := cli.ContainerStart(ctx, op.ExistingID, container.StartOptions{}); startErr != nil {
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

	binds := make([]string, len(plan.Binds))
	for i, b := range plan.Binds {
		binds[i] = b.String()
	}
	identity := dockeridentity.Resolve(binds)

	// proximo: pin the host-routed `.test` names at the host-gateway so they
	// resolve to the host where Traefik publishes :443 instead of the
	// container's own loopback. Discovery needs the Docker client, so it lives
	// here at the create edge rather than in the pure session planner.
	extraHosts := plan.ExtraHosts
	if plan.Proximo {
		extraHosts = augmentProximoHosts(ctx, cli, plan.ExtraHosts)
	}

	ui.Info("Creating container " + plan.ContainerName + "...")
	resp, createErr := cli.ContainerCreate(ctx,
		&container.Config{
			Image:        plan.Image.Ref,
			Tty:          true,
			OpenStdin:    true,
			Cmd:          plan.Cmd,
			WorkingDir:   plan.WorkingDir,
			User:         identity.UserSpec,
			ExposedPorts: plan.ExposedPorts,
			Env:          plan.Env,
		},
		&container.HostConfig{
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
		},
		nil, // network config
		nil, // platform
		plan.ContainerName,
	)
	if createErr != nil {
		return "", fmt.Errorf("failed to create container: %w", createErr)
	}

	if startErr := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); startErr != nil {
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
	args := filters.NewArgs(filters.Arg("label", proximo.HostsLabel))
	list, err := cli.ContainerList(ctx, container.ListOptions{Filters: args})
	if err != nil {
		ui.Warning("proximo: host discovery failed, .test names may be unreachable: " + err.Error())
		return base
	}
	var labels []string
	for _, c := range list {
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
	ui.Info(fmt.Sprintf("proximo: routing %d host(s) via host-gateway", len(hosts)))
	out := make([]string, 0, len(base)+len(hosts))
	out = append(out, base...)
	out = append(out, hosts...)
	return out
}

// Stop stops and removes the toolbox container associated with the workspace.
func Stop(ctx context.Context, cli client.APIClient, workspace string) error {
	return teardown.StopOne(ctx, cli, sessionplan.ContainerNameFor(workspace), teardown.DefaultStopGrace)
}

// StopByName stops and removes the toolbox container for a named shell.
func StopByName(ctx context.Context, cli client.APIClient, name string) error {
	return teardown.StopOne(ctx, cli, sessionplan.NamedContainerName(name), teardown.DefaultStopGrace)
}

// StopAll stops and removes every toolbox-managed container on the host.
// Matches the "toolbox-" prefix as well as the legacy singleton name "toolbox".
// Failures on a single container don't short-circuit the rest — partial
// cleanup beats fail-fast when `--all` is meant to be a bulk sweep.
func StopAll(ctx context.Context, cli client.APIClient) error {
	args := filters.NewArgs(filters.Arg("name", "toolbox"))
	list, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	found := 0
	var errs []error
	for _, c := range list {
		if len(c.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		if name != "toolbox" && !strings.HasPrefix(name, sessionplan.ContainerNamePrefix) {
			continue
		}
		if err := teardown.StopOne(ctx, cli, name, teardown.DefaultStopGrace); err != nil {
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
