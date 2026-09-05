package container

// The orderings inside Shell that leave no effect behind. Each is a contract
// stated in the doc comment of the phase that owns it — preflightCreate's,
// the overlay block in Shell, restampImageDigest's, replaceForReload's — and
// each test here fails only when the two statements that comment names are
// swapped. That is what separates these from the value tests beside them: the
// value every phase produces is the same either way, and only the sequence
// decides whether it reaches the thing that reads it. Which orderings are
// pinned, and by what: `.claude/rules/container-runtime.md`.

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
		t.Errorf("restampImageDigest ran after dispatchOp: the container was created carrying %s = %q, want the digest the store holds now (%q)",
			sessionplan.ImageDigestEnv, got, pulled)
	}
}

// preflightCreate runs inside resolveOp, before offerRefresh, so that a
// known-fatal branch costs nothing to learn.
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

			mock := createPathMock("sha256:fresh")
			mock.listFn = func(context.Context, client.ContainerListOptions) ([]container.Summary, error) {
				return tc.holders, nil
			}
			refreshes := stubRefresh(t, mock, imageplan.OutcomeUnsettled)

			_, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), []string{"8877:8877"}))
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("Shell() error = %v, want error = %v", err, tc.wantErr)
			}
			switch got := len(*refreshes); {
			case got > tc.wantRefresh:
				t.Errorf("preflightCreate ran after offerRefresh: the conflict was known-fatal, yet the start-up refresh had already put its question (%d time(s))", got)
			case got < tc.wantRefresh:
				t.Errorf("the start-up refresh never ran on a session with no holder to conflict with — the fixture no longer exercises the ordering")
			}
		})
	}
}

// localimage.Ensure pins the overlay's FROM to the base image's ID as the
// local store holds it, so the base has to be refreshed first.
//
// The refresh is the one phase here that never reaches the daemon, so it is
// placed by the call log it snapshots on the way in rather than by an effect:
// what is pinned is where it falls among the daemon calls, not that it ran.
func TestShellRefreshesTheBaseBeforeItBuildsTheOverlay(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)

	mock := createPathMock("sha256:fresh")
	refreshes := stubRefresh(t, mock, imageplan.OutcomeAccepted)

	plan := testPlan(t, testWorkspace(t), nil)
	withOverlay(t, plan)

	if _, err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if len(*refreshes) != 1 {
		t.Fatalf("the start-up refresh ran %d time(s), want exactly one: %v", len(*refreshes), mock.calls)
	}
	if before := (*refreshes)[0].before; slices.Contains(before, "ImageBuild") {
		t.Errorf("localimage.Ensure ran before offerRefresh: the overlay's FROM pins the base as it stood before the refresh (calls before the refresh: %v)", before)
	}
}

// The casualty list is what makes a reload's collateral visible, and it can
// only be gathered while the container is still alive: enumerated after
// removeAndWait it names nobody, and printed before the teardown it would name
// casualties a reload that then failed never made. Enumerate, destroy, print.
//
// Both halves reach the daemon, so the ordering is read straight off the call
// log rather than inferred from what the fake answered — ContainerTop returns
// the same processes whether the container is still there or not, which is
// precisely why the answer cannot testify to this and the sequence can.
func TestShellReloadEnumeratesTheCasualtiesBeforeTheTeardown(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)

	mock := createAfterReloadMock()
	if _, err := Shell(context.Background(), mock, reloadPlan(t, "toolbox-old-1234abcd")); err != nil {
		t.Fatalf("Shell(): %v", err)
	}

	order := func(call string) int { return slices.Index(mock.calls, call) }
	for _, step := range []string{"ContainerTop", "ContainerRemove"} {
		if order(step) < 0 {
			t.Fatalf("%s never reached the daemon: %v", step, mock.calls)
		}
	}
	if order("ContainerTop") > order("ContainerRemove") {
		t.Errorf("casualties enumerated after the teardown, so the reload can only ever report an empty list: %v", mock.calls)
	}
}
