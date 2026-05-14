// Package teardown owns the container stop/remove + shell-exit cleanup
// policy that previously lived inline at the bottom of
// internal/container/lifecycle.go::Shell.
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
//     cancelled by Ctrl+C), skips the stop when a sibling exec is
//     running, otherwise calls StopOne. Returns the cleanup error so
//     the caller can fold it into its own error chain.
//
// Before this concept was named, the policy was a 4-deep nested defer
// block inside Shell, with the timing constants (cleanupTimeout=30s,
// stopShellGrace=2) as package-level vars in lifecycle.go and the
// active-exec probe + stop+remove logic as loose helpers. Adding any
// pre/post-cleanup behaviour (log dump, notification, longer grace for a
// busy daemon) required editing inline inside the defer block.
// Lifting the policy here flattens the defer to one call, moves the
// timing knobs to typed defaults, and gives `toolbox stop` and the
// shell-exit path a single owner.
package teardown

import (
	"context"
	"fmt"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

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
	stopErr := cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout})

	if cerrdefs.IsNotFound(stopErr) {
		ui.Warning("Container " + name + " not found")
		return nil
	}
	if stopErr != nil {
		return fmt.Errorf("failed to stop container %s: %w", name, stopErr)
	}

	rmErr := cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if rmErr != nil && !cerrdefs.IsNotFound(rmErr) {
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
	inspect, err := cli.ContainerInspect(ctx, name)
	if err != nil {
		return false
	}
	if inspect.ContainerJSONBase == nil {
		return false
	}
	for _, execID := range inspect.ExecIDs {
		exec, err := cli.ContainerExecInspect(ctx, execID)
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
// bounded context. If a sibling exec is still attached, the container is
// left running so the sibling shell survives; otherwise the container is
// stopped and removed. Returns the cleanup error so the caller can fold
// it into the shell-exit error chain — a failed stop is noisy, not
// fatal, and should not overwrite an earlier shell error.
func OnShellExit(cli client.APIClient, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	if HasActiveExecs(ctx, cli, name) {
		ui.Info("Container " + name + " still has active sessions — leaving it running")
		return nil
	}
	return StopOne(ctx, cli, name, DefaultStopGrace)
}
