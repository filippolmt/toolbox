package container

// Start-up Refresh Prompt: what Shell does with the answer. The decision tree
// that puts the question is imageplan's and is tested there; these pin the
// half Shell owns — the stake each branch is asked at, the postponement stamp
// a decline leaves, and the container a yes on the start branch spends.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/imageplan"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// stubRefresh replaces the shell-start refresh with a fixed outcome and
// records the stake each call was put at. The decision tree it stands for is
// imageplan's and is tested there; what Shell owns is what a yes costs on the
// branch it took and what it then does with the answer — the one thing a
// terminal would otherwise be needed to reach.
func stubRefresh(t *testing.T, out imageplan.Outcome) *[]imageplan.Reason {
	t.Helper()
	var reasons []imageplan.Reason
	orig := refreshAtStart
	refreshAtStart = func(_ context.Context, _ client.APIClient, _ sessionplan.Image, _ string, reason imageplan.Reason) imageplan.Outcome {
		reasons = append(reasons, reason)
		return out
	}
	t.Cleanup(func() { refreshAtStart = orig })
	return &reasons
}

// stoppedContainer is the start path: a container the daemon still holds but
// is not running, which is what a daemon restart or a hand-typed
// `docker stop` leaves behind.
func stoppedContainer(env []string) func(context.Context, string) (container.InspectResponse, error) {
	return func(context.Context, string) (container.InspectResponse, error) {
		return container.InspectResponse{
			ID:     "stopped123",
			State:  &container.State{Running: false},
			Config: &container.Config{Env: env},
		}, nil
	}
}

// startPathMock is the start branch with a create behind it: the stopped
// container the question is about, and the name the recreate asks back for.
func startPathMock(repoDigest string) *mockClient {
	mock := createPathMock(repoDigest)
	mock.inspectFn = stoppedContainer(nil)
	return mock
}

// A "no" at the start-up prompt is a postponement, not a refusal, and the
// stamp is what makes it one: it is the origin of the window a session gets
// before it may recreate itself onto the image the prefetch is fetching
// anyway. Nothing is claimed about the registry — the store is still behind it.
func TestShellStampsADeclinedRefresh(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)
	stubRefresh(t, imageplan.OutcomeDeclined)

	plan := testPlan(t, testWorkspace(t), nil)
	if _, err := Shell(context.Background(), createPathMock("sha256:fresh"), plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if _, err := os.Stat(reload.DeclinedPath(plan.StateDir, plan.ContainerName)); err != nil {
		t.Errorf("a declined refresh left no stamp: %v", err)
	}
	if len(*got) != 1 || (*got)[0].StartSynced {
		t.Errorf("prefetch input = %+v, want StartSynced false after a decline", *got)
	}
}

// A ctrl+c at the start-up prompt is not the "no" it resembles: it stops the
// command. The prompt has already re-raised the signal raw mode swallowed, but
// whether the signal context has cancelled by now is a matter of scheduling —
// so the outcome is read directly, and no session is built in the window where
// it has not.
func TestShellAbandonsAnInterruptedRefresh(t *testing.T) {
	execed, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)
	stubRefresh(t, imageplan.OutcomeInterrupted)

	plan := testPlan(t, testWorkspace(t), nil)
	if _, err := Shell(context.Background(), createPathMock("sha256:fresh"), plan); err == nil {
		t.Fatal("Shell() error = nil, want the interrupted start to fail")
	}

	if *execed {
		t.Error("a session was started for a command the developer had stopped")
	}
	if _, err := os.Stat(reload.DeclinedPath(plan.StateDir, plan.ContainerName)); err == nil {
		t.Error("an interrupt stamped a postponement: there is no session left to postpone for")
	}
	if len(*got) != 0 {
		t.Errorf("prefetch input = %+v, want no prefetch after an interrupt", *got)
	}
}

// The other two outcomes leave no stamp: an accepted download and a store the
// probe proved current are both "this session has had its turn at the
// registry", which is what StartSynced says and what stops the poller asking
// the same question seconds later.
func TestShellStampsNothingWhenTheStoreIsCurrent(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)
	stubRefresh(t, imageplan.OutcomeCurrent)

	plan := testPlan(t, testWorkspace(t), nil)
	if _, err := Shell(context.Background(), createPathMock("sha256:fresh"), plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if _, err := os.Stat(reload.DeclinedPath(plan.StateDir, plan.ContainerName)); err == nil {
		t.Error("a synced refresh must leave no decline stamp")
	}
	if len(*got) != 1 || !(*got)[0].StartSynced {
		t.Errorf("prefetch input = %+v, want StartSynced true", *got)
	}
}

// A running container may have panes attached that would die with it, so the
// one branch that cannot honour a yes is also the one that must not ask: the
// wait would buy this session nothing and the prefetch fetches behind it
// either way. The idle reload is the answer for that case.
func TestShellConnectNeverReachesTheStartUpPrompt(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	reasons := stubRefresh(t, imageplan.OutcomeUnsettled)

	mock := &mockClient{inspectFn: runningContainer(nil)}
	if _, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if len(*reasons) != 0 {
		t.Errorf("the connect path ran the start-up refresh %d times, want 0", len(*reasons))
	}
}

// A stopped container runs nothing and holds nobody's session, so a yes here
// can be honoured — by rebuilding it. The question is therefore asked, and it
// is asked with the container at stake: what a yes costs is not the download
// alone, and the tree wording it has no way of knowing that.
func TestShellStartAsksWithTheContainerAtStake(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	reasons := stubRefresh(t, imageplan.OutcomeUnsettled)

	mock := &mockClient{inspectFn: stoppedContainer(nil)}
	if _, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if want := []imageplan.Reason{imageplan.ReasonStart}; !slices.Equal(*reasons, want) {
		t.Errorf("the start path asked under %v, want %v", *reasons, want)
	}
}

// Honouring the yes reuses the create that already knows how to pull, create
// and start: the stopped container is destroyed and the branch becomes the
// create it just turned into. Removal before the create, or the create would
// ask for a name the daemon has not released.
func TestShellStartRecreatesOnAnAcceptedRefresh(t *testing.T) {
	execed, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stubRefresh(t, imageplan.OutcomeAccepted)

	mock := startPathMock("sha256:fresh")
	plan := testPlan(t, testWorkspace(t), nil)
	// What the banner would render at the first prompt: a result published
	// about the container that is being replaced.
	stale := filepath.Join(plan.StateDir, "update-check")
	if err := os.WriteFile(stale, []byte("image_update=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if _, err := os.Stat(stale); err == nil {
		t.Error("the recreated session kept a banner cache describing the container it replaced")
	}
	order := func(call string) int { return slices.Index(mock.calls, call) }
	for _, step := range []string{"ContainerRemove", "ContainerCreate"} {
		if order(step) < 0 {
			t.Fatalf("%s never reached the daemon: %v", step, mock.calls)
		}
	}
	if order("ContainerRemove") > order("ContainerCreate") {
		t.Errorf("the create raced the stopped container's name: %v", mock.calls)
	}
	if !*execed {
		t.Error("the rebuilt session was never attached to")
	}
}

// The create the yes turns into can fail on something no removal can undo: a
// host port another container holds is fixed at ContainerCreate, so it must be
// pre-flighted while the container the developer was asked about is still
// there. Learning it afterwards would cost them that container and hand them
// the daemon's opaque refusal in its place.
func TestShellStartKeepsTheContainerWhenTheRecreateCannotSucceed(t *testing.T) {
	execed, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stubRefresh(t, imageplan.OutcomeAccepted)

	mock := startPathMock("sha256:fresh")
	mock.listFn = func(context.Context, client.ContainerListOptions) ([]container.Summary, error) {
		return []container.Summary{holderSummary("/nginx-proxy", 8877)}, nil
	}

	_, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), []string{"8877:8877"}))
	if err == nil {
		t.Fatal("Shell() error = nil, want the port conflict to fail the start")
	}
	if !strings.Contains(err.Error(), "nginx-proxy") {
		t.Errorf("Shell() error = %q, want it to name the holder", err)
	}
	if slices.Contains(mock.calls, "ContainerRemove") {
		t.Errorf("the container was destroyed for a create that could not succeed: %v", mock.calls)
	}
	if *execed {
		t.Error("a session was attached after a failed pre-flight")
	}
}

// The port pre-flight is only the last of the three things that can fail: a
// `:local` overlay that will not build is another, and failing it once the
// container is gone would leave the developer with neither a session nor the
// container they were asked about. Everything that can fail runs first.
func TestShellStartKeepsTheContainerWhenTheOverlayCannotBuild(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stubRefresh(t, imageplan.OutcomeAccepted)

	mock := startPathMock("sha256:fresh")
	// The overlay pins its FROM to the base image's ID, so a store that
	// cannot answer for the base is a build that cannot start.
	mock.imgInspFn = func(context.Context, string) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, errors.New("no such image")
	}

	plan := testPlan(t, testWorkspace(t), nil)
	withOverlay(t, plan)

	if _, err := Shell(context.Background(), mock, plan); err == nil {
		t.Fatal("Shell() error = nil, want the overlay failure to fail the start")
	}
	if slices.Contains(mock.calls, "ContainerRemove") {
		t.Errorf("the container was destroyed for a session that could not start: %v", mock.calls)
	}
}

// The answer describes what was true when the question was asked, and the
// question held the terminal for seconds. So the container is read again, and
// what that read finds decides — through the same runplan.Compute that chose
// the branch in the first place.
func TestShellStartRereadsTheContainerBeforeReplacingIt(t *testing.T) {
	// The digest the question was put about, carried by the container the first
	// read finds. What the second read carries instead is how each case is told
	// apart on the baseline axis as well as on the ID.
	const askedAbout = "sha256:the-image-it-was-created-from"
	digestEnv := func(digest string) *container.Config {
		return &container.Config{Env: []string{sessionplan.ImageDigestEnv + "=" + digest}}
	}

	for _, tc := range []struct {
		name       string
		second     func(context.Context, string) (container.InspectResponse, error)
		wantCreate bool
		wantExec   string
		wantDigest string
	}{
		{
			// A sibling shell started it: that session's owner never
			// volunteered to lose it, which is the whole reason a running
			// container is never asked about. This one joins it.
			name: "running again: the sibling's session is joined, not killed",
			second: func(context.Context, string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ID:     "stopped123",
					State:  &container.State{Running: true},
					Config: digestEnv(askedAbout),
				}, nil
			},
			wantExec: "stopped123", wantDigest: askedAbout,
		},
		{
			// A sibling shell answered the same question yes, so what carries
			// the name now is a *different* container, built from a *different*
			// image. The answer was about one that no longer exists, and both
			// the ID the caller dispatches and the digest its prefetch is
			// baselined on have to come off the container that is there now.
			name: "recreated by a sibling: the session joins the container that is there now",
			second: func(context.Context, string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ID:     "recreated456",
					State:  &container.State{Running: true},
					Config: digestEnv("sha256:the-image-the-sibling-built-it-from"),
				}, nil
			},
			wantExec: "recreated456", wantDigest: "sha256:the-image-the-sibling-built-it-from",
		},
		{
			// A `toolbox stop` or a `docker rm` got there first: the name is
			// free, which is all the removal was for. No container to read a
			// baseline off, so it comes from the re-stamped plan.
			name: "already gone: the name is free and the create takes it",
			second: func(context.Context, string) (container.InspectResponse, error) {
				return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
			},
			wantCreate: true, wantExec: "new123", wantDigest: "sha256:fresh",
		},
		{
			// Nothing is destroyed on an answer the daemon would not give, and
			// nothing is learned from it either: the start stays exactly the
			// one the question was put about.
			name: "unreadable: the container the answer was about is left alone",
			second: func(context.Context, string) (container.InspectResponse, error) {
				return container.InspectResponse{}, errors.New("daemon unreachable")
			},
			wantExec: "stopped123", wantDigest: askedAbout,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefetched, _ := stubPrefetch(t)
			stubRefresh(t, imageplan.OutcomeAccepted)

			execedID := ""
			origExec := execShellFn
			execShellFn = func(_ context.Context, _ client.APIClient, id string, _ []string) error {
				execedID = id
				return nil
			}
			t.Cleanup(func() { execShellFn = origExec })

			mock := startPathMock("sha256:fresh")
			reads := 0
			mock.inspectFn = func(ctx context.Context, name string) (container.InspectResponse, error) {
				reads++
				if reads == 1 {
					return stoppedContainer([]string{sessionplan.ImageDigestEnv + "=" + askedAbout})(ctx, name)
				}
				return tc.second(ctx, name)
			}
			// A removal before the session attaches is the recreate's; the one
			// after it is the shell-exit teardown, which every case reaches.
			destroyed := false
			mock.removeFn = func(context.Context, string, client.ContainerRemoveOptions) error {
				destroyed = destroyed || execedID == ""
				return nil
			}

			if _, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
				t.Fatalf("Shell() error: %v", err)
			}
			if got := slices.Contains(mock.calls, "ContainerCreate"); got != tc.wantCreate {
				t.Errorf("a container was created = %v, want %v: %v", got, tc.wantCreate, mock.calls)
			}
			// No second read here finds the container the answer was about,
			// so none of these cases may destroy one.
			if destroyed {
				t.Errorf("a container was destroyed for a recreate that stood down: %v", mock.calls)
			}
			if execedID != tc.wantExec {
				t.Errorf("the session attached to %q, want %q", execedID, tc.wantExec)
			}
			// The ID is not the only thing the second read settles: the
			// prefetch is baselined on the digest of the container this
			// session is actually attached to, and a baseline read off one
			// that is gone announces an update the session has adopted.
			if len(*prefetched) != 1 {
				t.Fatalf("the prefetch was started %d times, want once", len(*prefetched))
			}
			if got := (*prefetched)[0].ContainerDigest; got != tc.wantDigest {
				t.Errorf("the prefetch was baselined on %q, want %q", got, tc.wantDigest)
			}
		})
	}
}

// The reattach warnings describe what this session asked for and cannot have
// on the container it is joining, and both prescribe a `toolbox stop` and
// retry. An accepted recreate *is* that, applied — so the warning has to wait
// for the branch to settle rather than prescribe, seconds early, a fix about a
// container that is about to stop existing.
func TestShellStartWarnsAboutAContainerItIsActuallyJoining(t *testing.T) {
	for _, tc := range []struct {
		name     string
		refresh  imageplan.Outcome
		wantWarn bool
	}{
		{
			name:    "an accepted recreate applies the fix instead of prescribing it",
			refresh: imageplan.OutcomeAccepted,
		},
		{
			// Nothing was replaced, so the container being joined really is
			// short of what was asked for, and the advice stands.
			name:     "a declined refresh leaves the mismatch to warn about",
			refresh:  imageplan.OutcomeDeclined,
			wantWarn: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, restore := stubExecShell()
			defer restore()
			stubPrefetch(t)
			stubRefresh(t, tc.refresh)

			mock := startPathMock("sha256:fresh")
			// A HostConfig that binds nothing is what makes the wanted port
			// missing rather than unknowable: a record with no HostConfig at
			// all says nothing about what the container publishes.
			mock.inspectFn = func(context.Context, string) (container.InspectResponse, error) {
				return container.InspectResponse{
					ID:         "stopped123",
					State:      &container.State{Running: false},
					HostConfig: &container.HostConfig{},
				}, nil
			}
			plan := testPlan(t, testWorkspace(t), []string{"127.0.0.1:8080:8080"})

			captured := captureStderr(t, func() {
				if _, err := Shell(context.Background(), mock, plan); err != nil {
					t.Fatalf("Shell() error: %v", err)
				}
			})

			if got := strings.Contains(captured, "publish mismatch"); got != tc.wantWarn {
				t.Errorf("publish-mismatch warning = %v, want %v: %q", got, tc.wantWarn, captured)
			}
		})
	}
}

// A "no" leaves the container exactly as it was — started, not rebuilt — and
// is stamped as the postponement it is, which is what arms the idle reload for
// this session. The alternative would turn a "later" into the very
// destruction the developer just declined.
func TestShellStartKeepsTheContainerOnADeclinedRefresh(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stubRefresh(t, imageplan.OutcomeDeclined)

	started := ""
	mock := startPathMock("sha256:fresh")
	mock.startFn = func(_ context.Context, id string, _ client.ContainerStartOptions) error {
		started = id
		return nil
	}

	plan := testPlan(t, testWorkspace(t), nil)
	if _, err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if started != "stopped123" {
		t.Errorf("the stopped container was started as %q, want it started as it was", started)
	}
	if slices.Contains(mock.calls, "ContainerCreate") {
		t.Errorf("a declined refresh rebuilt the container anyway: %v", mock.calls)
	}
	if _, err := os.Stat(reload.DeclinedPath(plan.StateDir, plan.ContainerName)); err != nil {
		t.Errorf("a declined refresh left no stamp: %v", err)
	}
}
