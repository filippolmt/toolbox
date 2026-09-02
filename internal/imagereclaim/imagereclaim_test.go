package imagereclaim

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

const (
	testRepo    = "ghcr.io/filippolmt/toolbox"
	keptDigest  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	staleDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	otherDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

// mockClient implements the two Docker calls one sweep can make. The embedded
// nil client.APIClient is the point: an unstubbed call panics, so a sweep that
// reached for anything else fails the test loudly instead of returning a
// silent zero value.
type mockClient struct {
	client.APIClient

	mu       sync.Mutex
	items    []image.Summary
	listErr  error
	removeBy map[string]error

	listCalls int
	removed   []string
	opts      []client.ImageRemoveOptions
}

func (m *mockClient) ImageList(context.Context, client.ImageListOptions) (client.ImageListResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listCalls++
	if m.listErr != nil {
		return client.ImageListResult{}, m.listErr
	}
	return client.ImageListResult{Items: m.items}, nil
}

func (m *mockClient) ImageRemove(_ context.Context, id string, opts client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = append(m.removed, id)
	m.opts = append(m.opts, opts)
	if err := m.removeBy[id]; err != nil {
		return client.ImageRemoveResult{}, err
	}
	return client.ImageRemoveResult{}, nil
}

func (m *mockClient) removals() []string {
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
	cli := &mockClient{items: []image.Summary{
		summary("sha256:aaa", nil, staleDigest),
	}}

	sweep(context.Background(), cli, Input{Repo: testRepo, KeepDigest: keptDigest})

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
	cli := &mockClient{items: []image.Summary{
		summary("sha256:tagged", []string{testRepo + ":latest"}, otherDigest),
		summary("sha256:running", nil, keptDigest),
		foreign,
	}}

	sweep(context.Background(), cli, Input{Repo: testRepo, KeepDigest: keptDigest})

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
	cli := &mockClient{items: []image.Summary{summary("sha256:aaa", nil, staleDigest)}}

	sweep(context.Background(), cli, Input{Repo: testRepo, KeepDigest: keptDigest})

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
	cli := &mockClient{
		items:    []image.Summary{summary("sha256:held", nil, staleDigest)},
		removeBy: map[string]error{"sha256:held": errors.New("conflict: unable to delete (must be forced)")},
	}

	sweep(context.Background(), cli, Input{Repo: testRepo, KeepDigest: keptDigest})

	if lines := said(); len(lines) != 0 {
		t.Fatalf("summary said %q, want silence", lines)
	}
}

// One refusal must not end the sweep — the store holds a generation per merge,
// and a single overlay base pinning one of them would otherwise cost every
// later candidate.
func TestSweepKeepsGoingPastARefusal(t *testing.T) {
	captureAnnounce(t)
	cli := &mockClient{
		items: []image.Summary{
			summary("sha256:held", nil, staleDigest),
			summary("sha256:free", nil, otherDigest),
		},
		removeBy: map[string]error{"sha256:held": errors.New("conflict: unable to delete")},
	}

	sweep(context.Background(), cli, Input{Repo: testRepo, KeepDigest: keptDigest})

	if got := cli.removals(); len(got) != 2 {
		t.Fatalf("attempted %v, want both candidates", got)
	}
}

// The summary is the one line the act is allowed, and it states what happened
// rather than what was attempted.
func TestSweepSummarisesOnlyWhatItRemoved(t *testing.T) {
	said := captureAnnounce(t)
	cli := &mockClient{
		items: []image.Summary{
			summary("sha256:held", nil, keptDigest+"x"),
			summary("sha256:free", nil, staleDigest),
			summary("sha256:free2", nil, otherDigest),
		},
		removeBy: map[string]error{"sha256:held": errors.New("conflict: unable to delete")},
	}

	sweep(context.Background(), cli, Input{Repo: testRepo, KeepDigest: keptDigest})

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
	cli := &mockClient{items: []image.Summary{summary("sha256:aaa", nil, staleDigest)}}

	// t.Context(), not context.Background(): the sweep is a goroutine, and one
	// scoped to the process would outlive the test — reading the announce seam
	// that Cleanup is restoring underneath it, and printing through a later
	// test's capture.
	Start(t.Context(), cli, Input{Repo: testRepo, KeepDigest: keptDigest})

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
	cli := &mockClient{listErr: errors.New("dial unix /var/run/docker.sock: connect: permission denied")}

	sweep(context.Background(), cli, Input{Repo: testRepo, KeepDigest: keptDigest})

	if lines := said(); len(lines) != 0 {
		t.Fatalf("summary said %q, want silence", lines)
	}
	if got := cli.removals(); len(got) != 0 {
		t.Fatalf("removed %v, want nothing", got)
	}
}

// The summary lands on a tty the attached shell has already put in raw mode
// (term.MakeRaw clears ONLCR), where a bare LF drops a line without returning
// the carriage and staircases everything printed after it. Unlinking a
// multi-gigabyte image takes seconds, so this is the normal case rather than a
// race: by the time the sweep has something to say, the shell is attached.
func TestSweepSummaryIsSafeOnARawModeTty(t *testing.T) {
	said := captureAnnounce(t)
	cli := &mockClient{items: []image.Summary{summary("sha256:aaa", nil, staleDigest)}}

	sweep(context.Background(), cli, Input{Repo: testRepo, KeepDigest: keptDigest})

	lines := said()
	if len(lines) != 1 {
		t.Fatalf("summary said %q, want one line", lines)
	}
	if !strings.HasSuffix(lines[0], "\r") {
		t.Errorf("summary %q is not carriage-return terminated — it will staircase the attached shell", lines[0])
	}
}

// A cancelled sweep must stop asking. Every remaining candidate would
// otherwise cost a doomed daemon round-trip whose error is indistinguishable
// from the refusal that means "some container still needs this".
func TestSweepStopsAskingOnceCancelled(t *testing.T) {
	captureAnnounce(t)
	cli := &mockClient{items: []image.Summary{
		summary("sha256:a", nil, staleDigest),
		summary("sha256:b", nil, otherDigest),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sweep(ctx, cli, Input{Repo: testRepo, KeepDigest: keptDigest})

	if got := cli.removals(); len(got) != 0 {
		t.Fatalf("attempted %v on a cancelled context, want nothing", got)
	}
}
