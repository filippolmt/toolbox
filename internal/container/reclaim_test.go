package container

import (
	"context"
	"slices"
	"testing"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/imagereclaim"
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

// The ordering is the design and not an optimisation: only once this
// workspace's container exists and references the new image is every surviving
// reference to the old one somebody else's real reference. Run any earlier and
// the removal is guaranteed to be refused, because the session doing the
// reclaiming is itself the last holder.
func TestShellReclaimsOnlyOnceTheContainerReferencesTheImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	const pulled = "sha256:fresh"
	mock := createPathMock(pulled)
	got := stubReclaim(t, mock)

	plan := testPlan(t, testWorkspace(t), nil)
	if _, err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if len(*got) != 1 {
		t.Fatalf("reclaim started %d times, want 1", len(*got))
	}
	call := (*got)[0]
	if !slices.Contains(call.before, "ContainerCreate") {
		t.Errorf("reclaim started before ContainerCreate (calls so far: %v)", call.before)
	}
	if call.in.Repo != plan.Image.Ref {
		t.Errorf("Repo = %q, want the resolved base ref %q", call.in.Repo, plan.Image.Ref)
	}
	if call.in.KeepDigest != pulled {
		t.Errorf("KeepDigest = %q, want the digest this session runs %q", call.in.KeepDigest, pulled)
	}
}

// Cancelled with the session, which is safe rather than merely tolerated: a
// candidate the sweep did not reach is still a candidate at the next shell.
func TestShellReclaimDiesWithTheSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	mock := createPathMock("sha256:fresh")
	got := stubReclaim(t, mock)

	if _, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if len(*got) != 1 {
		t.Fatalf("reclaim started %d times, want 1", len(*got))
	}
	select {
	case <-(*got)[0].ctx.Done():
	default:
		t.Error("the reclaim context outlived the session")
	}
}

// `image_reclaim: false` is the developer disabling the act in so many words,
// and nothing else may.
func TestShellSkipsTheReclaimWhenTheDeveloperOptedOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	mock := createPathMock("sha256:fresh")
	got := stubReclaim(t, mock)

	plan := testPlan(t, testWorkspace(t), nil)
	plan.ReclaimImages = false
	if _, err := Shell(context.Background(), mock, plan); err != nil {
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
func TestShellReclaimTracksTheBaseRefNotTheOverlay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()

	mock := createPathMock("sha256:fresh")
	got := stubReclaim(t, mock)

	plan := testPlan(t, testWorkspace(t), nil)
	base := plan.Image.Ref
	if _, err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if len(*got) != 1 {
		t.Fatalf("reclaim started %d times, want 1", len(*got))
	}
	if repo := (*got)[0].in.Repo; repo != base {
		t.Errorf("Repo = %q, want the base ref %q (plan.Image is now %q)", repo, base, plan.Image.Ref)
	}
}
