package container

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/imageprefetch"
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
