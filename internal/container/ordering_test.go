package container

// The orderings inside Shell that are contracts rather than conveniences and
// that no data dependency already makes unrepresentable. Every test here fails
// when the two statements it names are swapped in lifecycle.go, which is the
// one property that separates an ordering test from the value tests beside it:
// the value each phase produces is the same either way, and only the sequence
// decides whether it reaches the thing that reads it. Census and rationale:
// `.claude/rules/container-runtime.md`.

import (
	"context"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/imageplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// restampImageDigest rewrites the plan's TOOLBOX_IMAGE_DIGEST, and
// createAndStart hands that same plan.Env straight to ContainerCreate — so the
// re-stamp has to run before the dispatch, not merely before Shell returns.
// After it, the container's own record names the digest cmd resolved before
// the refresh had its chance to pull, and every later session that connects
// reads that stale value as its prefetch baseline: a shell reporting itself
// behind an image it is already running, for as long as the container lives.
//
// What the assertion turns on: the plan is re-stamped either way, so
// TestShellPrefetchRestampsTheDigestOnCreate stays green with the two
// statements swapped. Only the container's own env says which came first.
func TestShellRestampsTheDigestBeforeItCreatesTheContainer(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)

	const pulled = "sha256:fresh"
	mock := createPathMock(pulled)
	var createdEnv []string
	mock.createFn = func(_ context.Context, cfg *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
		createdEnv = slices.Clone(cfg.Env)
		return container.CreateResponse{ID: "new123"}, nil
	}

	plan := testPlan(t, testWorkspace(t), nil)
	// What cmd resolved before the shell-start refresh had its chance.
	plan.Env = append(plan.Env, sessionplan.ImageDigestEnv+"=sha256:stale")

	if _, err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if got := sessionplan.EnvValue(createdEnv, sessionplan.ImageDigestEnv); got != pulled {
		t.Errorf("the container was created carrying %s = %q, want the digest the store holds now (%q)",
			sessionplan.ImageDigestEnv, got, pulled)
	}
}

// A host port another container already publishes can never be bound by the
// create that follows, and preflightCreate runs inside resolveOp precisely so
// that learning it costs nothing: the branch is known-fatal before the
// start-up refresh puts its question, and before a yes to it can spend a
// download. Run the refresh first and the developer is asked to pay for an
// image, waits the pull out, and is then handed a conflict that was knowable
// before the prompt was printed.
//
// The second row is what stops the first from passing vacuously: the same
// session, minus the holder, does reach the refresh.
func TestShellPreflightsThePortConflictBeforeItOffersToRefresh(t *testing.T) {
	for _, tc := range []struct {
		name        string
		holders     []container.Summary
		wantErr     bool
		wantRefresh int
	}{
		{
			name:    "a known-fatal conflict costs no prompt and no pull",
			holders: []container.Summary{holderSummary("/nginx-proxy", 8877)},
			wantErr: true,
		},
		{
			name:        "the same session without a holder does reach the refresh",
			wantRefresh: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, restore := stubExecShell()
			defer restore()
			stubPrefetch(t)
			reasons := stubRefresh(t, imageplan.OutcomeUnsettled)

			mock := createPathMock("sha256:fresh")
			mock.listFn = func(context.Context, client.ContainerListOptions) ([]container.Summary, error) {
				return tc.holders, nil
			}

			_, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), []string{"8877:8877"}))
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("Shell() error = %v, want error = %v", err, tc.wantErr)
			}
			if len(*reasons) != tc.wantRefresh {
				t.Errorf("the start-up refresh ran %d time(s), want %d", len(*reasons), tc.wantRefresh)
			}
		})
	}
}

// recordRefresh stands in for the shell-start refresh and appends to the
// mock's own ordered call log, so the act takes its place in the sequence the
// daemon calls are read from. stubRefresh records into a slice of its own,
// which says whether the refresh ran but never when.
func recordRefresh(t *testing.T, mock *mockClient, out imageplan.Outcome) {
	t.Helper()
	orig := refreshAtStart
	refreshAtStart = func(context.Context, client.APIClient, sessionplan.Image, string, imageplan.Reason) imageplan.Outcome {
		mock.record("Refresh")
		return out
	}
	t.Cleanup(func() { refreshAtStart = orig })
}

// localimage.Ensure pins the overlay's FROM to the base image's ID as the
// local store holds it, so the base has to be refreshed first. Built before,
// the `:local` sits on the very image the developer just agreed to replace,
// and the session runs an overlay of the superseded base — silently, and with
// the rebuild marker recording the old ID, so nothing notices until the next
// shell rebuilds it for a reason of its own.
//
// The refresh is the one phase here that never reaches the daemon, so it is
// recorded into the same log rather than asserted on its own: what is being
// pinned is where it falls among the calls, not that it happened.
func TestShellRefreshesTheBaseBeforeItBuildsTheOverlay(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)

	mock := createPathMock("sha256:fresh")
	recordRefresh(t, mock, imageplan.OutcomeAccepted)

	plan := testPlan(t, testWorkspace(t), nil)
	withOverlay(t, plan)

	if _, err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	order := func(call string) int { return slices.Index(mock.calls, call) }
	for _, step := range []string{"Refresh", "ImageBuild"} {
		if order(step) < 0 {
			t.Fatalf("%s never ran: %v", step, mock.calls)
		}
	}
	if order("Refresh") > order("ImageBuild") {
		t.Errorf("the overlay was built on the base as it stood before the refresh: %v", mock.calls)
	}
}
