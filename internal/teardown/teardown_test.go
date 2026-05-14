package teardown

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// notFoundErr satisfies cerrdefs.IsNotFound — mirrors the stub in
// internal/container/lifecycle_test.go and internal/runplan/runplan_test.go
// so all three layers share the same NotFound shape.
type notFoundErr struct{ msg string }

func (e *notFoundErr) Error() string { return e.msg }
func (e *notFoundErr) NotFound()     {}
func (e *notFoundErr) Unwrap() error { return nil }

// mockClient implements the subset of client.APIClient used by teardown.
type mockClient struct {
	client.APIClient
	stopFn        func(ctx context.Context, id string, opts container.StopOptions) error
	removeFn      func(ctx context.Context, id string, opts container.RemoveOptions) error
	inspectFn     func(ctx context.Context, id string) (container.InspectResponse, error)
	execInspectFn func(ctx context.Context, execID string) (container.ExecInspect, error)
}

func (m *mockClient) ContainerStop(ctx context.Context, id string, opts container.StopOptions) error {
	if m.stopFn != nil {
		return m.stopFn(ctx, id, opts)
	}
	return nil
}

func (m *mockClient) ContainerRemove(ctx context.Context, id string, opts container.RemoveOptions) error {
	if m.removeFn != nil {
		return m.removeFn(ctx, id, opts)
	}
	return nil
}

func (m *mockClient) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if m.inspectFn != nil {
		return m.inspectFn(ctx, id)
	}
	return container.InspectResponse{}, fmt.Errorf("ContainerInspect not mocked")
}

func (m *mockClient) ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error) {
	if m.execInspectFn != nil {
		return m.execInspectFn(ctx, execID)
	}
	return container.ExecInspect{}, fmt.Errorf("ContainerExecInspect not mocked")
}

func (m *mockClient) Close() error { return nil }

func TestStopOneStopsAndRemoves(t *testing.T) {
	stopCalled := false
	removeCalled := false
	var capturedRemoveOpts container.RemoveOptions
	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			stopCalled = true
			return nil
		},
		removeFn: func(_ context.Context, _ string, opts container.RemoveOptions) error {
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
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			return &notFoundErr{msg: "no such container"}
		},
	}
	if err := StopOne(context.Background(), mock, "missing", DefaultStopGrace); err != nil {
		t.Fatalf("StopOne should swallow NotFound, got: %v", err)
	}
}

func TestStopOnePassesGraceTimeout(t *testing.T) {
	var capturedTimeout *int
	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, opts container.StopOptions) error {
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
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:      "x",
					ExecIDs: []string{"sibling"},
				},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (container.ExecInspect, error) {
			return container.ExecInspect{Running: true}, nil
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

func TestHasActiveExecsFalseOnNilContainerJSONBase(t *testing.T) {
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, nil
		},
	}
	if HasActiveExecs(context.Background(), mock, "toolbox-x") {
		t.Fatal("nil ContainerJSONBase must yield false")
	}
}

func TestOnShellExitSkipsStopWhenSiblingExecRunning(t *testing.T) {
	stopCalled := false
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:      "x",
					ExecIDs: []string{"sibling"},
				},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (container.ExecInspect, error) {
			return container.ExecInspect{Running: true}, nil
		},
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
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
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:      "x",
					ExecIDs: []string{"ours"},
				},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (container.ExecInspect, error) {
			return container.ExecInspect{Running: false}, nil
		},
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			stopCalled = true
			return nil
		},
	}
	if err := OnShellExit(mock, "toolbox-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !stopCalled {
		t.Fatal("OnShellExit should stop when no sibling exec is Running")
	}
}
