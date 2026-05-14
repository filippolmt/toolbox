package runplan

import (
	"errors"
	"testing"

	"github.com/docker/docker/api/types/container"
)

// notFoundErr mimics the errdefs "not found" interface so cerrdefs.IsNotFound
// returns true for it inside Compute. Mirrors internal/container/lifecycle_test.go
// so the two layers share the same NotFound shape.
type notFoundErr struct{ msg string }

func (e *notFoundErr) Error() string { return e.msg }
func (e *notFoundErr) NotFound()     {}
func (e *notFoundErr) Unwrap() error { return nil }

func TestComputeActionConnectWhenRunning(t *testing.T) {
	inspect := container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:    "abc123",
			State: &container.State{Running: true},
		},
	}
	op, err := Compute(inspect, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if op.Action != ActionConnect {
		t.Errorf("Action = %v, want ActionConnect", op.Action)
	}
	if op.ExistingID != "abc123" {
		t.Errorf("ExistingID = %q, want %q", op.ExistingID, "abc123")
	}
}

func TestComputeActionStartWhenStopped(t *testing.T) {
	inspect := container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:    "stopped123",
			State: &container.State{Running: false},
		},
	}
	op, err := Compute(inspect, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if op.Action != ActionStart {
		t.Errorf("Action = %v, want ActionStart", op.Action)
	}
	if op.ExistingID != "stopped123" {
		t.Errorf("ExistingID = %q, want %q", op.ExistingID, "stopped123")
	}
}

func TestComputeActionCreateWhenNotFound(t *testing.T) {
	op, err := Compute(container.InspectResponse{}, &notFoundErr{msg: "no such container"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if op.Action != ActionCreate {
		t.Errorf("Action = %v, want ActionCreate", op.Action)
	}
	if op.ExistingID != "" {
		t.Errorf("ExistingID = %q, want empty", op.ExistingID)
	}
}

// TestComputeActionCreateWhenNilContainerJSONBase pins the regression: an
// InspectResponse whose embedded *ContainerJSONBase is nil must route to
// ActionCreate so callers don't dereference a nil pointer when reading
// inspect.ID / inspect.State / inspect.HostConfig.
func TestComputeActionCreateWhenNilContainerJSONBase(t *testing.T) {
	op, err := Compute(container.InspectResponse{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if op.Action != ActionCreate {
		t.Errorf("Action = %v, want ActionCreate", op.Action)
	}
}

func TestComputeReturnsInspectError(t *testing.T) {
	want := errors.New("daemon connection refused")
	op, err := Compute(container.InspectResponse{}, want)
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if op.Action != 0 || op.ExistingID != "" {
		t.Errorf("Op = %+v, want zero value on error", op)
	}
}

func TestActionString(t *testing.T) {
	cases := map[Action]string{
		ActionConnect: "connect",
		ActionStart:   "start",
		ActionCreate:  "create",
		Action(99):    "unknown",
	}
	for action, want := range cases {
		if got := action.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", action, got, want)
		}
	}
}
