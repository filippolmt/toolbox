package container

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

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/imageplan"
	"github.com/filippolmt/toolbox/internal/imageprefetch"
	"github.com/filippolmt/toolbox/internal/imagereclaim"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// The image the re-stamp tests resolve against: the repo half is what
// build.RepoDigest matches a RepoDigests entry on, so the two must agree.
const (
	prefetchRepo = "ghcr.io/filippolmt/toolbox"
	prefetchRef  = prefetchRepo + ":latest"
)

// TestMain neutralises the update prefetch for the whole package. Shell
// starts it just before attaching, and its first poll fires immediately — so
// left live it would drive the test's own mock from a second goroutine, which
// is both a data race and a stream of Docker calls no test asked for. Tests
// that assert on the prefetch install their own stub over this one.
func TestMain(m *testing.M) {
	startPrefetch = func(context.Context, client.APIClient, imageprefetch.Input) {}
	// The reclaim sweep is neutralised for the same reason: Shell starts it
	// just before attaching and it would delete images out of the test's own
	// mock from a second goroutine.
	reclaimImages = func(context.Context, client.APIClient, imagereclaim.Input) {}
	os.Exit(m.Run())
}

// stubPrefetch captures what Shell hands the update prefetch instead of
// starting a poller that would talk to a registry. The captured context is
// the one Shell owns, so a test can assert it dies with the session.
func stubPrefetch(t *testing.T) (*[]imageprefetch.Input, *context.Context) {
	t.Helper()
	var got []imageprefetch.Input
	var ctx context.Context
	orig := startPrefetch
	startPrefetch = func(c context.Context, _ client.APIClient, in imageprefetch.Input) {
		ctx = c
		got = append(got, in)
	}
	t.Cleanup(func() { startPrefetch = orig })
	return &got, &ctx
}

// runningContainer is the connect path: a live container whose recorded
// creation digest is the baseline the prefetch compares the local store to.
func runningContainer(env []string) func(context.Context, string) (container.InspectResponse, error) {
	return func(context.Context, string) (container.InspectResponse, error) {
		return container.InspectResponse{
			ID:     "abc123",
			State:  &container.State{Running: true},
			Config: &container.Config{Env: env},
		}, nil
	}
}

// The baseline is read off the container, not recomputed: a host process
// attaching to a container someone else created never resolved a digest of
// its own, and the digest this process would resolve now is precisely the
// newer one the session is behind.
func TestShellPrefetchUsesTheContainersOwnDigest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)

	const created = "sha256:aaaa"
	mock := &mockClient{inspectFn: runningContainer([]string{sessionplan.ImageDigestEnv + "=" + created})}

	plan := testPlan(t, testWorkspace(t), nil)
	if _, err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if len(*got) != 1 {
		t.Fatalf("prefetch started %d times, want 1", len(*got))
	}
	in := (*got)[0]
	if in.ContainerDigest != created {
		t.Errorf("ContainerDigest = %q, want the container's own %q", in.ContainerDigest, created)
	}
	if in.Ref != plan.Image.Ref {
		t.Errorf("Ref = %q, want the resolved base ref %q", in.Ref, plan.Image.Ref)
	}
	if in.StateDir != plan.StateDir {
		t.Errorf("StateDir = %q, want the plan's state mount %q", in.StateDir, plan.StateDir)
	}
}

// createPathMock is the not-found → create sequence, with the local store
// reporting repoDigest for the base image.
func createPathMock(repoDigest string) *mockClient {
	return &mockClient{
		inspectFn: func(context.Context, string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		createFn: func(context.Context, *container.Config, *container.HostConfig, string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "new123"}, nil
		},
		imgInspFn: func(_ context.Context, ref string) (client.ImageInspectResult, error) {
			return dockertest.ImageInspectResult(repoOf(ref), repoDigest), nil
		},
	}
}

// repoOf strips the tag from an image ref so a fake RepoDigests entry matches
// what build.RepoDigest looks for.
func repoOf(ref string) string {
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i]
	}
	return ref
}

// On the create path the container does not exist yet, so the baseline comes
// from the plan — but only after it has been re-stamped from the store. The
// host resolves the digest before planning, which is before the shell-start
// refresh can pull: without the re-stamp a container created *from* the new
// image carries the old digest and the prefetch reports it behind an image it
// is already running.
func TestShellPrefetchRestampsTheDigestOnCreate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)

	plan := testPlan(t, testWorkspace(t), nil)
	// What cmd resolved before the refresh had its chance.
	plan.Env = append(plan.Env, sessionplan.ImageDigestEnv+"=sha256:stale")

	const pulled = "sha256:fresh"
	if _, err := Shell(context.Background(), createPathMock(pulled), plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if len(*got) != 1 || (*got)[0].ContainerDigest != pulled {
		t.Fatalf("prefetch input = %+v, want the digest the store holds now (%s)", *got, pulled)
	}
	// The container's own record must agree, or it would name an image it is
	// not running.
	if v := sessionplan.EnvValue(plan.Env, sessionplan.ImageDigestEnv); v != pulled {
		t.Errorf("%s = %q, want %q", sessionplan.ImageDigestEnv, v, pulled)
	}
}

// A store with no repo digest for the base — a local `toolbox build` — leaves
// no baseline to claim, and the entry is dropped rather than left stale.
func TestShellPrefetchDropsTheDigestForALocalBuild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)

	plan := testPlan(t, testWorkspace(t), nil)
	plan.Env = append(plan.Env, sessionplan.ImageDigestEnv+"=sha256:stale")

	if _, err := Shell(context.Background(), createPathMock(""), plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if len(*got) != 1 || (*got)[0].ContainerDigest != "" {
		t.Fatalf("prefetch input = %+v, want no baseline", *got)
	}
	if v := sessionplan.EnvValue(plan.Env, sessionplan.ImageDigestEnv); v != "" {
		t.Errorf("%s = %q, want it dropped", sessionplan.ImageDigestEnv, v)
	}
}

// An inspect that carries no Config says nothing about the container.
// Substituting the plan's digest would answer with the value that makes the
// comparison come out equal, hiding a real update instead of admitting the
// baseline is unknown.
func TestShellPrefetchMakesNoClaimWithoutAContainerConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)

	plan := testPlan(t, testWorkspace(t), nil)
	plan.Env = append(plan.Env, sessionplan.ImageDigestEnv+"=sha256:this-process")

	mock := &mockClient{
		inspectFn: func(context.Context, string) (container.InspectResponse, error) {
			return container.InspectResponse{ID: "abc123", State: &container.State{Running: true}}, nil
		},
	}
	if _, err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if len(*got) != 1 || (*got)[0].ContainerDigest != "" {
		t.Fatalf("prefetch input = %+v, want no baseline", *got)
	}
}

// `pull: never` means "do not talk to the registry", and the probe talks to
// the registry — so it silences probe, prefetch and banner as one act.
// TOOLBOX_NO_UPDATE_CHECK silences the same act from the other direction.
func TestShellPrefetchRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *config.Config
	}{
		{"pull never", &config.Config{Shell: "zsh", Pull: config.PullNever}},
		{"opted out", &config.Config{Shell: "zsh", Env: map[string]string{sessionplan.NoUpdateCheckEnv: "1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			_, restore := stubExecShell()
			defer restore()
			got, _ := stubPrefetch(t)

			mock := &mockClient{inspectFn: runningContainer(nil)}
			if _, err := Shell(context.Background(), mock, testPlanWithCfg(t, tc.cfg, testWorkspace(t), nil)); err != nil {
				t.Fatalf("Shell() error: %v", err)
			}
			if len(*got) != 0 {
				t.Errorf("prefetch started under %s: %+v", tc.name, *got)
			}
		})
	}
}

// The poller must not outlive the shell it belongs to: the context Shell
// hands it is cancelled on return, which is also what cancels an in-flight
// pull.
func TestShellPrefetchStopsWhenTheShellExits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	_, ctx := stubPrefetch(t)

	mock := &mockClient{inspectFn: runningContainer(nil)}
	if _, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if *ctx == nil {
		t.Fatal("prefetch was never started")
	}
	select {
	case <-(*ctx).Done():
	default:
		t.Error("prefetch context still live after Shell returned")
	}
}

// restampPlan is a create-path plan carrying the digest cmd resolved before
// planning — the value the re-stamp is there to correct.
func restampPlan(digest string) *sessionplan.SessionPlan {
	env := []string{"TOOLBOX_CLI_VERSION=1.2.3"}
	if digest != "" {
		env = append(env, sessionplan.ImageDigestEnv+"="+digest)
	}
	return &sessionplan.SessionPlan{Env: env, Image: sessionplan.Image{Ref: prefetchRef}}
}

// TestRestampImageDigest covers the three answers the local store can give a
// shell that is about to create a container. cmd resolves the digest *before*
// planning, which is before Refresh has had its chance to pull, so a shell
// opened on the morning of a release would otherwise be stamped with the
// digest Refresh has just superseded — and the prefetch would report the
// session behind an image it is already running.
func TestRestampImageDigest(t *testing.T) {
	const stale = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	const fresh = "sha256:2222222222222222222222222222222222222222222222222222222222222222"

	tests := []struct {
		name string
		res  client.ImageInspectResult
		err  error
		op   runplan.Op
		want string
	}{
		{
			name: "the store's answer wins on the create path",
			res:  dockertest.ImageInspectResult(prefetchRepo, fresh),
			op:   runplan.Op{Action: runplan.ActionCreate},
			want: fresh,
		},
		{
			// A `toolbox build` retags the canonical ref onto an image with no
			// RepoDigests. Dropping the stamp is the honest answer: the
			// prefetch then makes no reload-worthiness claim at all, rather
			// than comparing the store against a digest this image never had.
			name: "a local build clears the stamp",
			res:  client.ImageInspectResult{},
			op:   runplan.Op{Action: runplan.ActionCreate},
		},
		{
			name: "an unreadable store leaves the plan's own answer alone",
			err:  os.ErrNotExist,
			op:   runplan.Op{Action: runplan.ActionCreate},
			want: stale,
		},
		{
			// Connect and start read the digest off the container that
			// already exists, so there is nothing here to correct.
			name: "connect is not re-stamped",
			op:   runplan.Op{Action: runplan.ActionConnect},
			want: stale,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := restampPlan(stale)
			cli := &mockClient{imgInspFn: func(context.Context, string) (client.ImageInspectResult, error) {
				return tc.res, tc.err
			}}

			restampImageDigest(t.Context(), cli, plan, plan.Image, tc.op)

			if got := sessionplan.EnvValue(plan.Env, sessionplan.ImageDigestEnv); got != tc.want {
				t.Errorf("%s = %q, want %q", sessionplan.ImageDigestEnv, got, tc.want)
			}
		})
	}
}

// stubRefresh replaces the shell-start refresh with a fixed outcome and
// records the stake each call was put at. The decision tree it stands for is
// imageplan's and is tested there; what Shell owns is what a yes costs on the
// branch it took and what it then does with the answer — the one thing a
// terminal would otherwise be needed to reach.
func stubRefresh(t *testing.T, out imageplan.Outcome) *[]imageplan.Stake {
	t.Helper()
	var stakes []imageplan.Stake
	orig := refreshAtStart
	refreshAtStart = func(_ context.Context, _ client.APIClient, _ sessionplan.Image, _ string, stake imageplan.Stake) imageplan.Outcome {
		stakes = append(stakes, stake)
		return out
	}
	t.Cleanup(func() { refreshAtStart = orig })
	return &stakes
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
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)
	stubRefresh(t, imageplan.Outcome{Declined: true})

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
	t.Setenv("HOME", t.TempDir())
	execed, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)
	stubRefresh(t, imageplan.Outcome{Interrupted: true})

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
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	got, _ := stubPrefetch(t)
	stubRefresh(t, imageplan.Outcome{Synced: true})

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
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stakes := stubRefresh(t, imageplan.Outcome{})

	mock := &mockClient{inspectFn: runningContainer(nil)}
	if _, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if len(*stakes) != 0 {
		t.Errorf("the connect path ran the start-up refresh %d times, want 0", len(*stakes))
	}
}

// A stopped container runs nothing and holds nobody's session, so a yes here
// can be honoured — by rebuilding it. The question is therefore asked, and it
// is asked with the container at stake: what a yes costs is not the download
// alone, and the tree wording it has no way of knowing that.
func TestShellStartAsksWithTheContainerAtStake(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stakes := stubRefresh(t, imageplan.Outcome{})

	mock := &mockClient{inspectFn: stoppedContainer(nil)}
	if _, err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if want := []imageplan.Stake{imageplan.StakeRecreate}; !slices.Equal(*stakes, want) {
		t.Errorf("the start path asked at %v, want %v", *stakes, want)
	}
}

// Honouring the yes reuses the create that already knows how to pull, create
// and start: the stopped container is destroyed and the branch becomes the
// create it just turned into. Removal before the create, or the create would
// ask for a name the daemon has not released.
func TestShellStartRecreatesOnAnAcceptedRefresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	execed, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stubRefresh(t, imageplan.Outcome{Synced: true, Accepted: true})

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
	t.Setenv("HOME", t.TempDir())
	execed, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stubRefresh(t, imageplan.Outcome{Synced: true, Accepted: true})

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
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stubRefresh(t, imageplan.Outcome{Synced: true, Accepted: true})

	mock := startPathMock("sha256:fresh")
	// The overlay pins its FROM to the base image's ID, so a store that
	// cannot answer for the base is a build that cannot start.
	mock.imgInspFn = func(context.Context, string) (client.ImageInspectResult, error) {
		return client.ImageInspectResult{}, errors.New("no such image")
	}

	plan := testPlan(t, testWorkspace(t), nil)
	plan.OverlayDockerfile = filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(plan.OverlayDockerfile, []byte("FROM base\nRUN true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	for _, tc := range []struct {
		name       string
		second     func(context.Context, string) (container.InspectResponse, error)
		wantCreate bool
		wantExec   string
	}{
		{
			// A sibling shell started it: that session's owner never
			// volunteered to lose it, which is the whole reason a running
			// container is never asked about. This one joins it.
			name: "running again: the sibling's session is joined, not killed",
			second: func(context.Context, string) (container.InspectResponse, error) {
				return container.InspectResponse{ID: "stopped123", State: &container.State{Running: true}}, nil
			},
			wantExec: "stopped123",
		},
		{
			// A `toolbox stop` or a `docker rm` got there first: the name is
			// free, which is all the removal was for.
			name: "already gone: the name is free and the create takes it",
			second: func(context.Context, string) (container.InspectResponse, error) {
				return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
			},
			wantCreate: true, wantExec: "new123",
		},
		{
			// Nothing is destroyed on an answer the daemon would not give.
			name: "unreadable: the container the answer was about is left alone",
			second: func(context.Context, string) (container.InspectResponse, error) {
				return container.InspectResponse{}, errors.New("daemon unreachable")
			},
			wantExec: "stopped123",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			stubPrefetch(t)
			stubRefresh(t, imageplan.Outcome{Synced: true, Accepted: true})

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
					return stoppedContainer(nil)(ctx, name)
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
			// None of the three reads is the container the answer was about,
			// so none of them may be destroyed by the recreate.
			if destroyed {
				t.Errorf("a container was destroyed for a recreate that stood down: %v", mock.calls)
			}
			if execedID != tc.wantExec {
				t.Errorf("the session attached to %q, want %q", execedID, tc.wantExec)
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
			refresh: imageplan.Outcome{Synced: true, Accepted: true},
		},
		{
			// Nothing was replaced, so the container being joined really is
			// short of what was asked for, and the advice stands.
			name:     "a declined refresh leaves the mismatch to warn about",
			refresh:  imageplan.Outcome{Declined: true},
			wantWarn: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
	_, restore := stubExecShell()
	defer restore()
	stubPrefetch(t)
	stubRefresh(t, imageplan.Outcome{Declined: true})

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
