// Package runplan owns the pure decision step inside container.Shell:
// given the result of ContainerInspect, decide whether to connect to a
// running container, start a stopped one, or create a fresh one.
//
// Mirrors the Plan-family pattern: ConfigPlan / MountPlan / SessionPlan
// compose at design-time before any Docker call; RunPlan composes at
// runtime from the ContainerInspect snapshot. Lifecycle dispatches on
// the typed Op rather than re-deriving the branch inline.
package runplan

import (
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
)

// Action enumerates the three terminal outcomes Compute can return.
type Action int

const (
	// ActionConnect: container exists and is Running — exec directly.
	ActionConnect Action = iota
	// ActionStart: container exists but is not running — start, then exec.
	ActionStart
	// ActionCreate: no usable container record — ensure image, create,
	// start, exec.
	ActionCreate
)

// String renders the Action as a stable lowercase token. Used by tests
// and by callers building log messages.
func (a Action) String() string {
	switch a {
	case ActionConnect:
		return "connect"
	case ActionStart:
		return "start"
	case ActionCreate:
		return "create"
	default:
		return "unknown"
	}
}

// Op is the typed decision returned by Compute. ExistingID carries the
// container ID for ActionConnect / ActionStart so the caller can dispatch
// ContainerStart / exec without re-inspecting. Empty for ActionCreate.
type Op struct {
	Action     Action
	ExistingID string
}

// Compute derives the Op from a ContainerInspect result.
//
// An InspectResponse with an empty ID is treated as "no usable record":
// the SDK can return that on edge cases (mocks, daemon shape changes),
// and dispatching ContainerStart / exec on an empty ExistingID would
// target nothing. Both empty-ID and NotFound errors route to
// ActionCreate so the caller falls through to create-fresh instead of
// touching a half-populated record. Any other inspect error is returned
// verbatim and the caller should abort.
func Compute(inspect container.InspectResponse, inspectErr error) (Op, error) {
	hasData := inspectErr == nil && inspect.ID != ""
	if hasData {
		if inspect.State != nil && inspect.State.Running {
			return Op{Action: ActionConnect, ExistingID: inspect.ID}, nil
		}
		return Op{Action: ActionStart, ExistingID: inspect.ID}, nil
	}
	if inspectErr == nil || cerrdefs.IsNotFound(inspectErr) {
		return Op{Action: ActionCreate}, nil
	}
	return Op{}, inspectErr
}
