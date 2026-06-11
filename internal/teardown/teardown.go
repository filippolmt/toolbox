// Package teardown owns the container stop/remove + shell-exit cleanup
// policy invoked by container.Shell on exit and by the `toolbox stop`
// commands.
//
// Three seams, one policy:
//
//   - StopOne: stop + remove a single named container, NotFound-safe.
//     Used by the public `toolbox stop` command, by `toolbox stop --all`,
//     and by the shell-exit path. Caller passes its own context; the
//     SIGTERM grace is a small integer (seconds) controlled by
//     DefaultStopGrace.
//   - HasActiveExecs: probe whether a sibling shell is still attached.
//     Inspect errors are treated as "no active execs" so a daemon hiccup
//     never strands a container nobody will ever clean up.
//   - OnShellExit: the deferred post-shell policy. Creates its own
//     bounded context fresh from context.Background (parent ctx may be
//     cancelled by Ctrl+C), skips teardown when a sibling exec is
//     running, kills (without waiting on the remove) when the container
//     was created with AutoRemove so the daemon reaps it asynchronously,
//     and falls back to synchronous StopOne for legacy containers.
//     Returns the cleanup error so the caller can fold it into its own
//     error chain.
package teardown

import (
	"context"
	"fmt"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/ui"
)

// DefaultTimeout bounds the best-effort shell-exit teardown. Sized to
// absorb a slow Docker daemon while keeping the user-visible
// prompt-to-prompt latency low: StopOne adds at most DefaultStopGrace
// seconds for SIGTERM, remove is sub-second, so the remainder is pure
// margin for the daemon socket itself.
const DefaultTimeout = 30 * time.Second

// DefaultStopGrace is the SIGTERM grace (seconds) passed to
// ContainerStop on shell-exit teardown. Kept short because the image's
// PID 1 child is `sleep infinity` (terminates instantly on SIGTERM) and
// persistent state lives on bind mounts — nothing to flush. Older
// images that shipped `CMD ["zsh"]` would fall back to SIGKILL after
// this grace; user-visible delta is "2s tail" instead of the prior 10s.
const DefaultStopGrace = 2

// StopOne stops and removes the named container. NotFound on stop is
// treated as success (the container is already gone). NotFound on
// remove is also tolerated — Docker's auto-remove may have raced us.
// Any other error propagates.
func StopOne(ctx context.Context, cli client.APIClient, name string, stopGrace int) error {
	timeout := stopGrace
	_, stopErr := cli.ContainerStop(ctx, name, client.ContainerStopOptions{Timeout: &timeout})

	if cerrdefs.IsNotFound(stopErr) {
		ui.Warning("Container " + name + " not found")
		return nil
	}
	if stopErr != nil {
		return fmt.Errorf("failed to stop container %s: %w", name, stopErr)
	}

	_, rmErr := cli.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
	// Conflict ("removal already in progress") is tolerated alongside NotFound:
	// on an AutoRemove container the ContainerStop above may have already
	// triggered the daemon's auto-remove worker, so a racing explicit remove
	// is redundant, not an error — the container is (being) gone either way.
	if rmErr != nil && !cerrdefs.IsNotFound(rmErr) && !cerrdefs.IsConflict(rmErr) {
		return fmt.Errorf("failed to remove container %s: %w", name, rmErr)
	}

	ui.Success("Container " + name + " stopped and removed")
	return nil
}

// HasActiveExecs reports whether the named container has any still-running
// exec session. Called on shell exit: the invoking exec has already drained
// (io.Copy returned) and is Running:false by the time this runs, so it does
// not self-report. A true result means a sibling terminal is still attached,
// and stopping the container would kill it — the caller skips teardown.
// Inspect errors are treated as "no active execs" so a transient daemon
// hiccup does not strand a container that nobody will ever clean up.
func HasActiveExecs(ctx context.Context, cli client.APIClient, name string) bool {
	result, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return false
	}
	return execsRunning(ctx, cli, result.Container)
}

// execsRunning is the inspect-driven core of HasActiveExecs, split out so
// OnShellExit can read both the sibling-exec signal and HostConfig.AutoRemove
// from a single ContainerInspect instead of inspecting twice.
func execsRunning(ctx context.Context, cli client.APIClient, inspect container.InspectResponse) bool {
	if inspect.ID == "" {
		return false
	}
	for _, execID := range inspect.ExecIDs {
		exec, err := cli.ExecInspect(ctx, execID, client.ExecInspectOptions{})
		if err != nil {
			continue
		}
		if exec.Running {
			return true
		}
	}
	return false
}

// OnShellExit runs the deferred post-shell teardown policy with a fresh,
// bounded context. A single inspect drives three outcomes:
//   - sibling exec still attached -> leave the container running.
//   - AutoRemove container -> ContainerKill and return immediately; the
//     daemon's auto-remove worker performs the (slow on macOS) bind-mount
//     teardown asynchronously, off the user's prompt critical path.
//   - legacy container (AutoRemove false) -> synchronous StopOne so nothing
//     leaks during the upgrade window.
//
// Returns the cleanup error so the caller can fold it into the shell-exit
// error chain — a failed teardown is noisy, not fatal, and should not
// overwrite an earlier shell error. A missing container (inspect fails) is a
// no-op: there is nothing left to clean up.
func OnShellExit(cli client.APIClient, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	result, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return nil
	}
	inspect := result.Container

	if execsRunning(ctx, cli, inspect) {
		ui.Info("Container " + name + " still has active sessions — leaving it running")
		return nil
	}

	// HostConfig is a pointer field on InspectResponse; guard it before
	// dereferencing so a degenerate inspect can't panic.
	if inspect.HostConfig != nil && inspect.HostConfig.AutoRemove {
		return killAutoRemove(ctx, cli, name)
	}
	// Transitional fallback for containers created before AutoRemove was set
	// at create time. Once every long-lived user container has been recreated
	// (containers are disposable, so within one upgrade cycle), this branch is
	// dead and OnShellExit collapses to inspect -> sibling?leave : killAutoRemove.
	return StopOne(ctx, cli, name, DefaultStopGrace)
}

// killAutoRemove SIGKILLs an AutoRemove container and returns without waiting
// on the remove. PID 1 is `sleep infinity` with all state on bind mounts, so
// there is nothing to flush — SIGKILL is safe and skips the SIGTERM grace.
// The daemon's auto-remove worker deletes the container afterwards. NotFound
// means it is already gone (a race with a prior teardown), which is success.
func killAutoRemove(ctx context.Context, cli client.APIClient, name string) error {
	if _, err := cli.ContainerKill(ctx, name, client.ContainerKillOptions{Signal: "KILL"}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("failed to kill container %s: %w", name, err)
	}
	ui.Success("Container " + name + " stopped (removing in background)")
	return nil
}
