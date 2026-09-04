package imagereclaim

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/ui"
)

const (
	testRepo    = "ghcr.io/filippolmt/toolbox"
	keptDigest  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	staleDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	otherDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

// storeStub is the store one sweep works over and the record of what it did.
// It implements no Docker method of its own: docker() wires the shared fake to
// this state, stubbing the two endpoints a sweep may reach and no others, so a
// sweep that reached for anything else panics on the method it named instead
// of returning a silent zero value.
type storeStub struct {
	mu       sync.Mutex
	items    []image.Summary
	listErr  error
	removeBy map[string]error
	// onRemove runs inside ImageRemove, before it answers — the seam for a
	// session that ends while an unlink is in flight.
	onRemove func(id string)

	fake    *dockertest.Fake
	removed []string
	opts    []client.ImageRemoveOptions
}

// docker returns the daemon a sweep sees, built once and reused so the call
// counters survive across the sweep and the assertions that read them. Called
// from the test goroutine only, before the sweep it hands the fake to runs.
func (m *storeStub) docker() *dockertest.Fake {
	if m.fake != nil {
		return m.fake
	}
	m.fake = &dockertest.Fake{
		ImageListFn: func(context.Context) (client.ImageListResult, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.listErr != nil {
				return client.ImageListResult{}, m.listErr
			}
			return client.ImageListResult{Items: m.items}, nil
		},
		ImageRemoveFn: func(_ context.Context, id string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.removed = append(m.removed, id)
			m.opts = append(m.opts, opts)
			if m.onRemove != nil {
				m.onRemove(id)
			}
			if err := m.removeBy[id]; err != nil {
				return client.ImageRemoveResult{}, err
			}
			return client.ImageRemoveResult{}, nil
		},
	}
	return m.fake
}

func (m *storeStub) removals() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.removed...)
}

// summary builds one local-store image entry: an id, the tags it still holds
// and the repo digests it carries.
func summary(id string, tags []string, digests ...string) image.Summary {
	refs := make([]string, 0, len(digests))
	for _, d := range digests {
		refs = append(refs, testRepo+"@"+d)
	}
	return image.Summary{ID: id, RepoTags: tags, RepoDigests: refs}
}

// An image carrying this repo's digest and no tag is the whole predicate: this
// CLI pulled it and a later move of `latest` took its name away.
func TestSweepRemovesASupersededImage(t *testing.T) {
	cli := &storeStub{items: []image.Summary{
		summary("sha256:aaa", nil, staleDigest),
	}}

	sweep(context.Background(), cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	if got := cli.removals(); len(got) != 1 || got[0] != "sha256:aaa" {
		t.Fatalf("removed %v, want [sha256:aaa]", got)
	}
}

// The perimeter, stated as the three things the predicate must never
// nominate. The foreign-repo case is the load-bearing one: an image this
// project never pulled carries no digest for this repo, so it is not a
// candidate whatever else is true of it — which is what `dangling=true`
// would have got wrong in the other direction.
func TestSweepLeavesEverythingElseAlone(t *testing.T) {
	foreign := image.Summary{ID: "sha256:foreign", RepoDigests: []string{"docker.io/library/postgres@" + otherDigest}}
	cli := &storeStub{items: []image.Summary{
		summary("sha256:tagged", []string{testRepo + ":latest"}, otherDigest),
		summary("sha256:running", nil, keptDigest),
		foreign,
	}}

	sweep(context.Background(), cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	if got := cli.removals(); len(got) != 0 {
		t.Fatalf("removed %v, want nothing", got)
	}
}

// The absent flags are the load-bearing half of ADR 0007 and both are easy to
// add back by someone reading the call as merely timid: `force` turns every
// refusal into the silent removal of an image a colleague's stopped session is
// waiting for, and `PruneChildren` reaches into the `:local` overlay built on
// top of the candidate, which no registry can reproduce.
func TestSweepNeverForcesAndNeverPrunesChildren(t *testing.T) {
	cli := &storeStub{items: []image.Summary{summary("sha256:aaa", nil, staleDigest)}}

	sweep(context.Background(), cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	cli.mu.Lock()
	defer cli.mu.Unlock()
	if len(cli.opts) != 1 {
		t.Fatalf("ImageRemove called %d times, want 1", len(cli.opts))
	}
	if got := cli.opts[0]; got.Force || got.PruneChildren {
		t.Fatalf("ImageRemove options %+v, want neither force nor PruneChildren", got)
	}
}

// captureAnnounce redirects the summary line for one test and returns what the
// sweep said, joined. The seam is a package var for the same reason the rest of
// the repo uses one: the real writer is the attached tty.
func captureAnnounce(t *testing.T) func() []string {
	t.Helper()
	var (
		mu    sync.Mutex
		lines []string
	)
	prev := announce
	announce = func(format string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, a...))
	}
	t.Cleanup(func() { announce = prev })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
}

// A refusal is an answer, not a failure: the daemon owns the in-use question,
// so the image stays and the sweep keeps going. And it says nothing — an
// unattended act that narrated every stopped container someone else owns would
// train the developer to ignore the one line that matters.
func TestSweepStaysSilentWhenTheDaemonRefuses(t *testing.T) {
	said := captureAnnounce(t)
	cli := &storeStub{
		items:    []image.Summary{summary("sha256:held", nil, staleDigest)},
		removeBy: map[string]error{"sha256:held": errors.New("conflict: unable to delete (must be forced)")},
	}

	sweep(context.Background(), cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	if lines := said(); len(lines) != 0 {
		t.Fatalf("summary said %q, want silence", lines)
	}
}

// One refusal must not end the sweep — the store holds a generation per merge,
// and a single overlay base pinning one of them would otherwise cost every
// later candidate.
func TestSweepKeepsGoingPastARefusal(t *testing.T) {
	captureAnnounce(t)
	cli := &storeStub{
		items: []image.Summary{
			summary("sha256:held", nil, staleDigest),
			summary("sha256:free", nil, otherDigest),
		},
		removeBy: map[string]error{"sha256:held": errors.New("conflict: unable to delete")},
	}

	sweep(context.Background(), cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	if got := cli.removals(); len(got) != 2 {
		t.Fatalf("attempted %v, want both candidates", got)
	}
}

// The summary is the one line the act is allowed, and it states what happened
// rather than what was attempted.
func TestSweepSummarisesOnlyWhatItRemoved(t *testing.T) {
	said := captureAnnounce(t)
	cli := &storeStub{
		items: []image.Summary{
			summary("sha256:held", nil, keptDigest+"x"),
			summary("sha256:free", nil, staleDigest),
			summary("sha256:free2", nil, otherDigest),
		},
		removeBy: map[string]error{"sha256:held": errors.New("conflict: unable to delete")},
	}

	sweep(context.Background(), cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	lines := said()
	if len(lines) != 1 {
		t.Fatalf("summary said %q, want one line", lines)
	}
	if !strings.Contains(lines[0], "2") {
		t.Fatalf("summary %q does not name the two images it removed", lines[0])
	}
}

// Start is the caller-facing half: the sweep runs beside the attached shell
// and never in front of it, so a store full of generations cannot delay the
// prompt. Cancellation needs no test of its own — the context reaches every
// daemon call, and a candidate the sweep did not reach is still a candidate at
// the next shell.
func TestStartSweepsBesideTheSession(t *testing.T) {
	said := captureAnnounce(t)
	cli := &storeStub{items: []image.Summary{summary("sha256:aaa", nil, staleDigest)}}

	// t.Context(), not context.Background(): the sweep is a goroutine, and one
	// scoped to the process would outlive the test — reading the announce seam
	// that Cleanup is restoring underneath it, and printing through a later
	// test's capture.
	Start(t.Context(), cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	// Waiting on the summary rather than on the removal is what makes the
	// goroutine finished rather than merely started: the announce is its last
	// act, so once it has been said there is nothing left to race with.
	deadline := time.Now().Add(5 * time.Second)
	for len(said()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the background sweep never removed the candidate")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := cli.removals(); len(got) != 1 || got[0] != "sha256:aaa" {
		t.Fatalf("removed %v, want [sha256:aaa]", got)
	}
}

// A store the daemon will not enumerate is not an error worth a word: the act
// is opportunistic, and the next shell asks again.
func TestSweepStaysSilentWhenTheStoreCannotBeListed(t *testing.T) {
	said := captureAnnounce(t)
	cli := &storeStub{listErr: errors.New("dial unix /var/run/docker.sock: connect: permission denied")}

	sweep(context.Background(), cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	if lines := said(); len(lines) != 0 {
		t.Fatalf("summary said %q, want silence", lines)
	}
	if got := cli.removals(); len(got) != 0 {
		t.Fatalf("removed %v, want nothing", got)
	}
}

// The summary is emitted through ui.InfoAsyncf and not ui.Infof, because by
// the time a removal finishes the attached shell has put the tty in raw mode.
// That terminator is ui's invariant, pinned by TestInfoAsyncfReturnsTheCarriage;
// what this package owes is only that it reaches for the background writer.
func TestSweepAnnouncesThroughTheBackgroundWriter(t *testing.T) {
	if reflect.ValueOf(announce).Pointer() != reflect.ValueOf(ui.InfoAsyncf).Pointer() {
		t.Error("announce is not ui.InfoAsyncf — a summary printed with Infof staircases the attached shell")
	}
}

// A cancelled sweep must stop asking. Every remaining candidate would
// otherwise cost a doomed daemon round-trip whose error is indistinguishable
// from the refusal that means "some container still needs this".
func TestSweepStopsAskingOnceCancelled(t *testing.T) {
	captureAnnounce(t)
	cli := &storeStub{items: []image.Summary{
		summary("sha256:a", nil, staleDigest),
		summary("sha256:b", nil, otherDigest),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sweep(ctx, cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	if got := cli.removals(); len(got) != 0 {
		t.Fatalf("attempted %v on a cancelled context, want nothing", got)
	}
}

// Cancellation stops the work, not the accounting: a session that exits
// mid-sweep has already freed whatever it freed, and dropping the summary
// would leave the developer with gigabytes gone and no line saying so.
func TestSweepStillReportsWhatACancelledRunRemoved(t *testing.T) {
	said := captureAnnounce(t)
	ctx, cancel := context.WithCancel(context.Background())
	cli := &storeStub{
		items: []image.Summary{
			summary("sha256:first", nil, staleDigest),
			summary("sha256:second", nil, otherDigest),
		},
		// The session exits while the first unlink is in flight.
		onRemove: func(string) { cancel() },
	}

	sweep(ctx, cli.docker(), Input{Ref: testRepo, KeepDigest: keptDigest})

	if got := cli.removals(); len(got) != 1 {
		t.Fatalf("attempted %v, want to stop after the cancellation", got)
	}
	lines := said()
	if len(lines) != 1 {
		t.Fatalf("summary said %q, want the one image it did remove reported", lines)
	}
}

// An empty ref is not a wildcard: build.RepoDigest compares the bare registry
// path, and the empty path matches a malformed `@sha256:…` RepoDigests entry —
// which would nominate an image from a project that is not this one. The
// sibling prefetch refuses its own empty input for the same reason.
func TestStartRefusesAnEmptyRef(t *testing.T) {
	captureAnnounce(t)
	// The unstubbed endpoints are the assertion: reaching anything but the
	// listing or a removal panics on the method it asked for.
	Start(t.Context(), (&storeStub{}).docker(), Input{Ref: "", KeepDigest: keptDigest})

	if got := sweepRefused(t, Input{KeepDigest: keptDigest}); !got {
		t.Error("an empty Ref reached the daemon")
	}
}

// sweepRefused reports whether sweep abstained before touching the client.
func sweepRefused(t *testing.T, in Input) bool {
	t.Helper()
	cli := &storeStub{items: []image.Summary{summary("sha256:aaa", nil, staleDigest)}}
	sweep(context.Background(), cli.docker(), in)
	return cli.docker().ImageListCalls() == 0
}
