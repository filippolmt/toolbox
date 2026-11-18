package herdrcore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
)

// probeDaemon answers the probe's exec: created as "probe", started, and
// inspected as inspect says.
func probeDaemon(inspect func(context.Context, string) (client.ExecInspectResult, error)) *dockertest.Fake {
	return &dockertest.Fake{
		ExecCreateFn: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
			return client.ExecCreateResult{ID: "probe"}, nil
		},
		ExecStartFn: func(context.Context, string, client.ExecStartOptions) (client.ExecStartResult, error) {
			return client.ExecStartResult{}, nil
		},
		ExecInspectFn: inspect,
	}
}

func exited(code int) func(context.Context, string) (client.ExecInspectResult, error) {
	return func(context.Context, string) (client.ExecInspectResult, error) {
		return client.ExecInspectResult{ExitCode: code}, nil
	}
}

func stillRunning(context.Context, string) (client.ExecInspectResult, error) {
	return client.ExecInspectResult{Running: true}, nil
}

func TestInstalledReadsTheProbesExitCode(t *testing.T) {
	for code, want := range map[int]bool{0: true, 1: false} {
		got, err := Installed(t.Context(), probeDaemon(exited(code)), "toolbox-herdr")
		if err != nil || got != want {
			t.Errorf("exit %d: Installed = %v, %v; want %v, nil", code, got, err, want)
		}
	}
}

// Every daemon failure is an error naming the container, never "missing":
// that would make toolbox herdr update a machine it cannot see.
func TestInstalledReportsEveryDaemonFailure(t *testing.T) {
	boom := errors.New("daemon said no")
	createFails := probeDaemon(exited(0))
	createFails.ExecCreateFn = func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
		return client.ExecCreateResult{}, boom
	}
	startFails := probeDaemon(exited(0))
	startFails.ExecStartFn = func(context.Context, string, client.ExecStartOptions) (client.ExecStartResult, error) {
		return client.ExecStartResult{}, boom
	}
	inspectFails := probeDaemon(func(context.Context, string) (client.ExecInspectResult, error) {
		return client.ExecInspectResult{}, boom
	})
	for name, cli := range map[string]*dockertest.Fake{"create": createFails, "start": startFails, "inspect": inspectFails} {
		_, err := Installed(t.Context(), cli, "toolbox-herdr")
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "toolbox-herdr") {
			t.Errorf("%s fails: Installed err = %v, want the daemon's error naming toolbox-herdr", name, err)
		}
	}
}

func TestInstalledStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Installed(ctx, probeDaemon(stillRunning), "toolbox-herdr"); !errors.Is(err, context.Canceled) {
		t.Errorf("Installed err = %v, want context.Canceled", err)
	}
}

// An exec that never finishes does not hang toolbox herdr: the probe has its
// own deadline.
func TestInstalledIsBounded(t *testing.T) {
	orig := probeTimeout
	probeTimeout = 20 * time.Millisecond
	t.Cleanup(func() { probeTimeout = orig })
	if _, err := Installed(t.Context(), probeDaemon(stillRunning), "toolbox-herdr"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Installed err = %v, want context.DeadlineExceeded", err)
	}
}
