package teardown

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
)

// conflictErr satisfies cerrdefs.IsConflict — models the daemon's "removal
// already in progress" response when an explicit remove races AutoRemove.
type conflictErr struct{ msg string }

func (e *conflictErr) Error() string { return e.msg }
func (e *conflictErr) Conflict()     {}
func (e *conflictErr) Unwrap() error { return nil }

// mockClient implements the subset of client.APIClient used by teardown.
type mockClient struct {
	client.APIClient
	stopFn        func(ctx context.Context, id string, opts client.ContainerStopOptions) error
	removeFn      func(ctx context.Context, id string, opts client.ContainerRemoveOptions) error
	killFn        func(ctx context.Context, id string, signal string) error
	inspectFn     func(ctx context.Context, id string) (container.InspectResponse, error)
	execInspectFn func(ctx context.Context, execID string) (client.ExecInspectResult, error)
}

func (m *mockClient) ContainerStop(ctx context.Context, id string, opts client.ContainerStopOptions) (client.ContainerStopResult, error) {
	if m.stopFn != nil {
		return client.ContainerStopResult{}, m.stopFn(ctx, id, opts)
	}
	return client.ContainerStopResult{}, nil
}

func (m *mockClient) ContainerKill(ctx context.Context, id string, opts client.ContainerKillOptions) (client.ContainerKillResult, error) {
	if m.killFn != nil {
		return client.ContainerKillResult{}, m.killFn(ctx, id, opts.Signal)
	}
	return client.ContainerKillResult{}, nil
}

func (m *mockClient) ContainerRemove(ctx context.Context, id string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	if m.removeFn != nil {
		return client.ContainerRemoveResult{}, m.removeFn(ctx, id, opts)
	}
	return client.ContainerRemoveResult{}, nil
}

func (m *mockClient) ContainerInspect(ctx context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	if m.inspectFn != nil {
		inspect, err := m.inspectFn(ctx, id)
		return client.ContainerInspectResult{Container: inspect}, err
	}
	return client.ContainerInspectResult{}, fmt.Errorf("ContainerInspect not mocked")
}

func (m *mockClient) ExecInspect(ctx context.Context, execID string, _ client.ExecInspectOptions) (client.ExecInspectResult, error) {
	if m.execInspectFn != nil {
		return m.execInspectFn(ctx, execID)
	}
	return client.ExecInspectResult{}, fmt.Errorf("ExecInspect not mocked")
}

func (m *mockClient) Close() error { return nil }

func TestStopOneStopsAndRemoves(t *testing.T) {
	stopCalled := false
	removeCalled := false
	var capturedRemoveOpts client.ContainerRemoveOptions
	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			stopCalled = true
			return nil
		},
		removeFn: func(_ context.Context, _ string, opts client.ContainerRemoveOptions) error {
			removeCalled = true
			capturedRemoveOpts = opts
			return nil
		},
	}
	if err := StopOne(context.Background(), mock, "toolbox-x", DefaultStopGrace); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopCalled || !removeCalled {
		t.Fatalf("stopCalled=%v removeCalled=%v", stopCalled, removeCalled)
	}
	if !capturedRemoveOpts.Force {
		t.Errorf("ContainerRemove must use Force=true")
	}
}

func TestStopOneSwallowsNotFound(t *testing.T) {
	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			return &dockertest.NotFoundError{Msg: "no such container"}
		},
	}
	if err := StopOne(context.Background(), mock, "missing", DefaultStopGrace); err != nil {
		t.Fatalf("StopOne should swallow NotFound, got: %v", err)
	}
}

func TestStopOnePassesGraceTimeout(t *testing.T) {
	var capturedTimeout *int
	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, opts client.ContainerStopOptions) error {
			capturedTimeout = opts.Timeout
			return nil
		},
	}
	if err := StopOne(context.Background(), mock, "toolbox-x", 7); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedTimeout == nil || *capturedTimeout != 7 {
		t.Errorf("stop timeout = %v, want 7", capturedTimeout)
	}
}

func TestHasActiveExecsTrueOnRunningSibling(t *testing.T) {
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ID:      "x",
				ExecIDs: []string{"sibling"},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{Running: true}, nil
		},
	}
	if !HasActiveExecs(context.Background(), mock, "toolbox-x") {
		t.Fatal("expected HasActiveExecs=true when a sibling exec is Running")
	}
}

func TestHasActiveExecsFalseOnInspectError(t *testing.T) {
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, errors.New("daemon hiccup")
		},
	}
	if HasActiveExecs(context.Background(), mock, "toolbox-x") {
		t.Fatal("inspect errors must be treated as no-active-execs")
	}
}

func TestHasActiveExecsFalseOnEmptyInspect(t *testing.T) {
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, nil
		},
	}
	if HasActiveExecs(context.Background(), mock, "toolbox-x") {
		t.Fatal("empty inspect must yield false")
	}
}

func TestOnShellExitSkipsStopWhenSiblingExecRunning(t *testing.T) {
	stopCalled := false
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ID:      "x",
				ExecIDs: []string{"sibling"},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{Running: true}, nil
		},
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			stopCalled = true
			return nil
		},
	}
	if err := OnShellExit(mock, "toolbox-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopCalled {
		t.Fatal("OnShellExit must skip stop when a sibling exec is Running")
	}
}

func TestOnShellExitStopsWhenNoSiblingExec(t *testing.T) {
	stopCalled := false
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ID:      "x",
				ExecIDs: []string{"ours"},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{Running: false}, nil
		},
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			stopCalled = true
			return nil
		},
	}
	if err := OnShellExit(mock, "toolbox-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopCalled {
		t.Fatal("OnShellExit should stop when no sibling exec is Running (legacy, AutoRemove false)")
	}
}

// autoRemoveInspect builds an inspect for a running AutoRemove container with
// a single drained (Running:false) exec — the just-exited shell.
func autoRemoveInspect() container.InspectResponse {
	return container.InspectResponse{
		ID:         "x",
		ExecIDs:    []string{"ours"},
		HostConfig: &container.HostConfig{AutoRemove: true},
	}
}

func TestOnShellExitKillsAutoRemoveWithoutStop(t *testing.T) {
	killCalled := false
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return autoRemoveInspect(), nil
		},
		execInspectFn: func(_ context.Context, _ string) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{Running: false}, nil
		},
		killFn: func(_ context.Context, _ string, sig string) error {
			killCalled = true
			if sig != "KILL" {
				t.Errorf("kill signal = %q, want KILL", sig)
			}
			return nil
		},
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			t.Fatal("AutoRemove path must not call ContainerStop")
			return nil
		},
		removeFn: func(_ context.Context, _ string, _ client.ContainerRemoveOptions) error {
			t.Fatal("AutoRemove path must not call ContainerRemove (daemon reaps it)")
			return nil
		},
	}
	if err := OnShellExit(mock, "toolbox-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !killCalled {
		t.Fatal("AutoRemove path must ContainerKill")
	}
}

func TestOnShellExitKillSwallowsNotFound(t *testing.T) {
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return autoRemoveInspect(), nil
		},
		execInspectFn: func(_ context.Context, _ string) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{Running: false}, nil
		},
		killFn: func(_ context.Context, _ string, _ string) error {
			return &dockertest.NotFoundError{Msg: "no such container"}
		},
	}
	if err := OnShellExit(mock, "toolbox-x"); err != nil {
		t.Fatalf("kill NotFound must be swallowed, got: %v", err)
	}
}

func TestOnShellExitNoOpWhenInspectFails(t *testing.T) {
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			t.Fatal("missing container must be a no-op")
			return nil
		},
		killFn: func(_ context.Context, _ string, _ string) error {
			t.Fatal("missing container must be a no-op")
			return nil
		},
	}
	if err := OnShellExit(mock, "toolbox-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStopOneSwallowsConflictOnRemove(t *testing.T) {
	mock := &mockClient{
		removeFn: func(_ context.Context, _ string, _ client.ContainerRemoveOptions) error {
			return &conflictErr{msg: "removal of container is already in progress"}
		},
	}
	if err := StopOne(context.Background(), mock, "toolbox-x", DefaultStopGrace); err != nil {
		t.Fatalf("StopOne should swallow Conflict (AutoRemove race), got: %v", err)
	}
}
