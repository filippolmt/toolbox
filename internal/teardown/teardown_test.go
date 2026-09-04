package teardown

import (
	"context"
	"errors"
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

// exitStub is what one teardown's daemon answers with. It implements no Docker
// method of its own: docker() wires the shared fake to the fields a test set,
// and leaves the rest unstubbed — so an endpoint this teardown had no business
// reaching panics on the method it named instead of answering a silent zero
// value. The inspect field is spelled in container.InspectResponse, the shape
// every caller here actually reads.
type exitStub struct {
	stopFn        func(ctx context.Context, id string, opts client.ContainerStopOptions) error
	removeFn      func(ctx context.Context, id string, opts client.ContainerRemoveOptions) error
	killFn        func(ctx context.Context, id string, signal string) error
	inspectFn     func(ctx context.Context, id string) (container.InspectResponse, error)
	execInspectFn func(ctx context.Context, execID string) (client.ExecInspectResult, error)

	fake *dockertest.Fake
}

// docker returns the daemon the teardown sees, built once and reused so the
// call counters survive the run and the assertions that read them. Called from
// the test goroutine only.
func (m *exitStub) docker() *dockertest.Fake {
	if m.fake != nil {
		return m.fake
	}
	m.fake = &dockertest.Fake{}
	if m.stopFn != nil {
		m.fake.ContainerStopFn = func(ctx context.Context, id string, opts client.ContainerStopOptions) (client.ContainerStopResult, error) {
			return client.ContainerStopResult{}, m.stopFn(ctx, id, opts)
		}
	}
	if m.removeFn != nil {
		m.fake.ContainerRemoveFn = func(ctx context.Context, id string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
			return client.ContainerRemoveResult{}, m.removeFn(ctx, id, opts)
		}
	}
	if m.killFn != nil {
		m.fake.ContainerKillFn = func(ctx context.Context, id string, opts client.ContainerKillOptions) (client.ContainerKillResult, error) {
			return client.ContainerKillResult{}, m.killFn(ctx, id, opts.Signal)
		}
	}
	if m.inspectFn != nil {
		m.fake.ContainerInspectFn = func(ctx context.Context, id string) (client.ContainerInspectResult, error) {
			inspect, err := m.inspectFn(ctx, id)
			return client.ContainerInspectResult{Container: inspect}, err
		}
	}
	if m.execInspectFn != nil {
		m.fake.ExecInspectFn = m.execInspectFn
	}
	return m.fake
}

func TestStopOneStopsAndRemoves(t *testing.T) {
	stopCalled := false
	removeCalled := false
	var capturedRemoveOpts client.ContainerRemoveOptions
	mock := &exitStub{
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
	if err := StopOne(context.Background(), mock.docker(), "toolbox-x", DefaultStopGrace); err != nil {
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
	mock := &exitStub{
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			return &dockertest.NotFoundError{Msg: "no such container"}
		},
	}
	if err := StopOne(context.Background(), mock.docker(), "missing", DefaultStopGrace); err != nil {
		t.Fatalf("StopOne should swallow NotFound, got: %v", err)
	}
}

func TestStopOnePassesGraceTimeout(t *testing.T) {
	var capturedTimeout *int
	mock := &exitStub{
		stopFn: func(_ context.Context, _ string, opts client.ContainerStopOptions) error {
			capturedTimeout = opts.Timeout
			return nil
		},
		// Reached and not asserted here, stated anyway: an unstubbed endpoint
		// panics, which is what keeps the assertions of absence honest.
		removeFn: func(context.Context, string, client.ContainerRemoveOptions) error { return nil },
	}
	if err := StopOne(context.Background(), mock.docker(), "toolbox-x", 7); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedTimeout == nil || *capturedTimeout != 7 {
		t.Errorf("stop timeout = %v, want 7", capturedTimeout)
	}
}

func TestHasActiveExecsTrueOnRunningSibling(t *testing.T) {
	mock := &exitStub{
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
	if !HasActiveExecs(context.Background(), mock.docker(), "toolbox-x") {
		t.Fatal("expected HasActiveExecs=true when a sibling exec is Running")
	}
}

func TestHasActiveExecsFalseOnInspectError(t *testing.T) {
	mock := &exitStub{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, errors.New("daemon hiccup")
		},
	}
	if HasActiveExecs(context.Background(), mock.docker(), "toolbox-x") {
		t.Fatal("inspect errors must be treated as no-active-execs")
	}
}

func TestHasActiveExecsFalseOnEmptyInspect(t *testing.T) {
	mock := &exitStub{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, nil
		},
	}
	if HasActiveExecs(context.Background(), mock.docker(), "toolbox-x") {
		t.Fatal("empty inspect must yield false")
	}
}

func TestOnShellExitSkipsStopWhenSiblingExecRunning(t *testing.T) {
	stopCalled := false
	mock := &exitStub{
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
	if err := OnShellExit(mock.docker(), "toolbox-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stopCalled {
		t.Fatal("OnShellExit must skip stop when a sibling exec is Running")
	}
}

func TestOnShellExitStopsWhenNoSiblingExec(t *testing.T) {
	stopCalled := false
	mock := &exitStub{
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
		// The legacy path is a full StopOne: the remove follows the stop.
		removeFn: func(context.Context, string, client.ContainerRemoveOptions) error { return nil },
	}
	if err := OnShellExit(mock.docker(), "toolbox-x"); err != nil {
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
	mock := &exitStub{
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
	if err := OnShellExit(mock.docker(), "toolbox-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !killCalled {
		t.Fatal("AutoRemove path must ContainerKill")
	}
}

func TestOnShellExitKillSwallowsNotFound(t *testing.T) {
	mock := &exitStub{
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
	if err := OnShellExit(mock.docker(), "toolbox-x"); err != nil {
		t.Fatalf("kill NotFound must be swallowed, got: %v", err)
	}
}

func TestOnShellExitNoOpWhenInspectFails(t *testing.T) {
	mock := &exitStub{
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
	if err := OnShellExit(mock.docker(), "toolbox-x"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestOnShellExitKillSwallowsConflict covers the disk-full death: the
// container crashed on its own, so ContainerKill returns Conflict ("container
// is not running"). killAutoRemove must treat it as success — the daemon reaps
// the stopped AutoRemove container regardless — so the noisy kill error never
// masks the real (disk) failure the shell surfaces.
func TestOnShellExitKillSwallowsConflict(t *testing.T) {
	mock := &exitStub{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return autoRemoveInspect(), nil
		},
		execInspectFn: func(_ context.Context, _ string) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{Running: false}, nil
		},
		killFn: func(_ context.Context, _ string, _ string) error {
			return &conflictErr{msg: "cannot kill container: toolbox-x is not running"}
		},
	}
	if err := OnShellExit(mock.docker(), "toolbox-x"); err != nil {
		t.Fatalf("kill Conflict must be swallowed, got: %v", err)
	}
}

func TestStopOneSwallowsConflictOnRemove(t *testing.T) {
	mock := &exitStub{
		// The stop comes first and must succeed for the remove to be reached.
		stopFn: func(context.Context, string, client.ContainerStopOptions) error { return nil },
		removeFn: func(_ context.Context, _ string, _ client.ContainerRemoveOptions) error {
			return &conflictErr{msg: "removal of container is already in progress"}
		},
	}
	if err := StopOne(context.Background(), mock.docker(), "toolbox-x", DefaultStopGrace); err != nil {
		t.Fatalf("StopOne should swallow Conflict (AutoRemove race), got: %v", err)
	}
}
