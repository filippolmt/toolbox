package container

import (
	"context"
	"slices"
	"testing"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/imagereclaim"
	"github.com/filippolmt/toolbox/internal/localimage"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// reclaimCall is what one Shell hands Image Reclamation, plus the two facts
// only the call site can testify to: the context it was given (which must die
// with the session) and the daemon calls that had already happened.
type reclaimCall struct {
	in     imagereclaim.Input
	ctx    context.Context
	before []string
}

// stubReclaim captures the sweep instead of running one against the test's own
// mock from a second goroutine.
func stubReclaim(t *testing.T, mock *mockClient) *[]reclaimCall {
	t.Helper()
	var got []reclaimCall
	orig := reclaimImages
	reclaimImages = func(c context.Context, _ client.APIClient, in imagereclaim.Input) {
		got = append(got, reclaimCall{in: in, ctx: c, before: slices.Clone(mock.calls)})
	}
	t.Cleanup(func() { reclaimImages = orig })
	return &got
}

// reclaimFixture is the create-path session every test here drives: a
// throwaway HOME, the exec seam stubbed, a not-found -> create mock whose local
// store reports sessionDigest, and the reclaim captured instead of run. Folded
// into one helper because the four tests differ only in what they assert about
// the single captured call.
func reclaimFixture(t *testing.T, sessionDigest string) (*mockClient, *sessionplan.SessionPlan, *[]reclaimCall) {
	t.Helper()
	_, restore := stubExecShell()
	t.Cleanup(restore)

	mock := createPathMock(sessionDigest)
	return mock, testPlan(t, testWorkspace(t), nil), stubReclaim(t, mock)
}

// oneReclaim runs the session and returns the single call it started.
func oneReclaim(t *testing.T, mock *mockClient, plan *sessionplan.SessionPlan, got *[]reclaimCall) reclaimCall {
	t.Helper()
	if _, err := Shell(t.Context(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if len(*got) != 1 {
		t.Fatalf("reclaim started %d times, want 1", len(*got))
	}
	return (*got)[0]
}

// The ordering is the design and not an optimisation: only once this
// workspace's container exists and references the new image is every surviving
// reference to the old one somebody else's real reference. Run any earlier and
// the removal is guaranteed to be refused, because the session doing the
// reclaiming is itself the last holder.
func TestShellReclaimsOnlyOnceTheContainerReferencesTheImage(t *testing.T) {
	const pulled = "sha256:fresh"
	mock, plan, got := reclaimFixture(t, pulled)
	base := plan.Image.Ref

	call := oneReclaim(t, mock, plan, got)

	if !slices.Contains(call.before, "ContainerCreate") {
		t.Errorf("reclaim started before ContainerCreate (calls so far: %v)", call.before)
	}
	if call.in.Ref != base {
		t.Errorf("Ref = %q, want the resolved base ref %q", call.in.Ref, base)
	}
	if call.in.KeepDigest != pulled {
		t.Errorf("KeepDigest = %q, want the digest this session runs %q", call.in.KeepDigest, pulled)
	}
}

// Cancelled with the session, which is safe rather than merely tolerated: a
// candidate the sweep did not reach is still a candidate at the next shell.
func TestShellReclaimDiesWithTheSession(t *testing.T) {
	mock, plan, got := reclaimFixture(t, "sha256:fresh")

	call := oneReclaim(t, mock, plan, got)

	select {
	case <-call.ctx.Done():
	default:
		t.Error("the reclaim context outlived the session")
	}
}

// `image_reclaim: false` is the developer disabling the act in so many words,
// and nothing else may.
func TestShellSkipsTheReclaimWhenTheDeveloperOptedOut(t *testing.T) {
	mock, plan, got := reclaimFixture(t, "sha256:fresh")
	plan.ReclaimImages = false

	if _, err := Shell(t.Context(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if len(*got) != 0 {
		t.Fatalf("reclaim started %d times, want none", len(*got))
	}
}

// The base ref, never the `:local` overlay tag: the overlay is built rather
// than pulled, so it carries no repo digest for this repo and the sweep would
// nominate nothing at all — while the base underneath it is what accumulates
// a generation per merge.
//
// Driven through Shell with an overlay in place, which is the one state where
// the two refs differ: on every ordinary session localimage.Ensure is a
// passthrough and the base is what the session runs anyway, so an assertion
// would be comparing the base against itself.
func TestShellReclaimTracksTheBaseRefNotTheOverlay(t *testing.T) {
	mock, plan, got := reclaimFixture(t, "sha256:fresh")
	base := plan.Image.Ref
	withOverlay(t, plan)

	call := oneReclaim(t, mock, plan, got)

	if call.in.Ref != base {
		t.Errorf("Ref = %q, want the base ref %q, not the overlay %q", call.in.Ref, base, localimage.LocalRef)
	}
}
