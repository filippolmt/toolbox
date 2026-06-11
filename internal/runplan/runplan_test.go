package runplan

import (
	"errors"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/filippolmt/toolbox/internal/dockertest"
)

func TestComputeActionConnectWhenRunning(t *testing.T) {
	inspect := container.InspectResponse{
		ID:    "abc123",
		State: &container.State{Running: true},
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
		ID:    "stopped123",
		State: &container.State{Running: false},
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
	op, err := Compute(container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"})
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

// TestComputeActionCreateWhenEmptyInspect pins the regression: a zero
// InspectResponse (empty ID, nil State) must route to ActionCreate so
// callers don't act on a half-populated record when reading
// inspect.ID / inspect.State / inspect.HostConfig.
func TestComputeActionCreateWhenEmptyInspect(t *testing.T) {
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
