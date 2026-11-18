package herdrcore

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/moby/client"
)

// shellExec is the part of the Docker client Installed needs.
type shellExec interface {
	ExecCreate(ctx context.Context, container string, o client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecStart(ctx context.Context, execID string, o client.ExecStartOptions) (client.ExecStartResult, error)
	ExecInspect(ctx context.Context, execID string, o client.ExecInspectOptions) (client.ExecInspectResult, error)
}

// installedCmd tests for the binary `hc machine add --install` and
// `hc machine update` write: the container's writable layer, lost on recreate.
var installedCmd = []string{"sh", "-c", `test -x "$HOME/.local/bin/herdr-core"`}

// Installed reports whether herdr-core's binary is in the running container.
func Installed(ctx context.Context, cli shellExec, container string) (bool, error) {
	created, err := cli.ExecCreate(ctx, container, client.ExecCreateOptions{Cmd: installedCmd})
	if err != nil {
		return false, fmt.Errorf("probe herdr-core in %s: %w", container, err)
	}
	if _, err := cli.ExecStart(ctx, created.ID, client.ExecStartOptions{Detach: true}); err != nil {
		return false, fmt.Errorf("probe herdr-core in %s: %w", container, err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		res, err := cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return false, fmt.Errorf("probe herdr-core in %s: %w", container, err)
		}
		if !res.Running {
			return res.ExitCode == 0, nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return false, fmt.Errorf("probe herdr-core in %s: %w", container, ctx.Err())
		}
	}
}
