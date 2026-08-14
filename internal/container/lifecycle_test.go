package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// mockClient implements the subset of client.APIClient used by the tests.
// Unmocked methods panic to surface unexpected calls.
type mockClient struct {
	client.APIClient

	inspectFn     func(ctx context.Context, id string) (container.InspectResponse, error)
	createFn      func(ctx context.Context, cfg *container.Config, hostCfg *container.HostConfig, name string) (container.CreateResponse, error)
	startFn       func(ctx context.Context, id string, opts client.ContainerStartOptions) error
	stopFn        func(ctx context.Context, id string, opts client.ContainerStopOptions) error
	removeFn      func(ctx context.Context, id string, opts client.ContainerRemoveOptions) error
	imgInspFn     func(ctx context.Context, id string) (client.ImageInspectResult, error)
	imgPullFn     func(ctx context.Context, ref string, opts client.ImagePullOptions) (io.ReadCloser, error)
	listFn        func(ctx context.Context, opts client.ContainerListOptions) ([]container.Summary, error)
	execInspectFn func(ctx context.Context, execID string) (client.ExecInspectResult, error)
}

func (m *mockClient) ContainerInspect(ctx context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	if m.inspectFn != nil {
		inspect, err := m.inspectFn(ctx, id)
		return client.ContainerInspectResult{Container: inspect}, err
	}
	return client.ContainerInspectResult{}, fmt.Errorf("ContainerInspect not mocked")
}

func (m *mockClient) ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	if m.createFn != nil {
		resp, err := m.createFn(ctx, options.Config, options.HostConfig, options.Name)
		return client.ContainerCreateResult{ID: resp.ID, Warnings: resp.Warnings}, err
	}
	return client.ContainerCreateResult{}, fmt.Errorf("ContainerCreate not mocked")
}

func (m *mockClient) ContainerStart(ctx context.Context, id string, opts client.ContainerStartOptions) (client.ContainerStartResult, error) {
	if m.startFn != nil {
		return client.ContainerStartResult{}, m.startFn(ctx, id, opts)
	}
	return client.ContainerStartResult{}, nil
}

func (m *mockClient) ContainerStop(ctx context.Context, id string, opts client.ContainerStopOptions) (client.ContainerStopResult, error) {
	if m.stopFn != nil {
		return client.ContainerStopResult{}, m.stopFn(ctx, id, opts)
	}
	return client.ContainerStopResult{}, nil
}

func (m *mockClient) ContainerRemove(ctx context.Context, id string, opts client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	if m.removeFn != nil {
		return client.ContainerRemoveResult{}, m.removeFn(ctx, id, opts)
	}
	return client.ContainerRemoveResult{}, nil
}

func (m *mockClient) ImageInspect(ctx context.Context, id string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	if m.imgInspFn != nil {
		return m.imgInspFn(ctx, id)
	}
	return client.ImageInspectResult{}, fmt.Errorf("ImageInspect not mocked")
}

func (m *mockClient) ImagePull(ctx context.Context, ref string, opts client.ImagePullOptions) (client.ImagePullResponse, error) {
	if m.imgPullFn != nil {
		rc, err := m.imgPullFn(ctx, ref, opts)
		if err != nil {
			return nil, err
		}
		return dockertest.PullResponse{ReadCloser: rc}, nil
	}
	// Default: succeed with an empty body so Shell can proceed.
	return dockertest.PullResponse{ReadCloser: io.NopCloser(bytes.NewReader(nil))}, nil
}

func (m *mockClient) ContainerList(ctx context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
	if m.listFn != nil {
		items, err := m.listFn(ctx, opts)
		return client.ContainerListResult{Items: items}, err
	}
	return client.ContainerListResult{}, fmt.Errorf("ContainerList not mocked")
}

func (m *mockClient) ExecInspect(ctx context.Context, execID string, _ client.ExecInspectOptions) (client.ExecInspectResult, error) {
	if m.execInspectFn != nil {
		return m.execInspectFn(ctx, execID)
	}
	return client.ExecInspectResult{}, fmt.Errorf("ExecInspect not mocked")
}

func (m *mockClient) Close() error { return nil }

// --- Helpers ---

func testConfig() *config.Config {
	// ResolveImage always returns the canonical registry tag.
	// Shell: "zsh" matches the Load() default.
	return &config.Config{Shell: "zsh"}
}

// testWorkspace returns a stable workspace path for use in tests.
func testWorkspace(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// testPlan composes a *sessionplan.SessionPlan from the standard
// testConfig() so call sites can hand the new (ctx, cli, plan) Shell
// signature a fully populated plan without re-stating the seam call in
// every test body.
func testPlan(t *testing.T, workspace string, publish []string) *sessionplan.SessionPlan {
	t.Helper()
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: workspace, Ports: publish})
	if err != nil {
		t.Fatalf("testPlan: %v", err)
	}
	return plan
}

// testPlanWithCfg is the variant for tests that supply a non-default cfg
// (e.g. custom shell selection).
func testPlanWithCfg(t *testing.T, cfg *config.Config, workspace string, publish []string) *sessionplan.SessionPlan {
	t.Helper()
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace, Ports: publish})
	if err != nil {
		t.Fatalf("testPlanWithCfg: %v", err)
	}
	return plan
}

// stubExecShell replaces execShellFn with a no-op and returns a restore callback.
func stubExecShell() (called *bool, restore func()) {
	c := false
	orig := execShellFn
	execShellFn = func(_ context.Context, _ client.APIClient, _ string, _ []string) error {
		c = true
		return nil
	}
	return &c, func() { execShellFn = orig }
}

// --- Tests ---

// TestShellContainerNaming exercises the container-naming behaviour through
// the public Shell seam: each subtest invokes Shell with a workspace path and
// asserts on the `name` argument captured from ContainerCreate. Behaviour
// is observed only through Shell, never by calling containerNameFor
// directly.
func TestShellContainerNaming(t *testing.T) {
	// Sandbox HOME so any filesystem touch by mountplan.Plan lands in tmp,
	// not the real ~/.toolbox/. Mirrors internal/mountplan/plan_test.go.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cases := []struct {
		name       string
		workspace  string
		assertName func(t *testing.T, got string)
	}{
		{
			name:      "stable and unique with deterministic hash",
			workspace: "/Users/alice/project/toolbox",
			assertName: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "toolbox-") {
					t.Errorf("name should start with toolbox- prefix, got %q", got)
				}
				if !strings.Contains(got, "-toolbox-") {
					t.Errorf("name should embed basename, got %q", got)
				}
			},
		},
		{
			name:      "different absolute path with same basename produces different name",
			workspace: "/Users/bob/project/toolbox",
			assertName: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "toolbox-") {
					t.Errorf("toolbox- prefix missing: %q", got)
				}
			},
		},
		{
			name:      "sanitises basename of paths with spaces and punctuation",
			workspace: "/tmp/My Weird Dir!",
			assertName: func(t *testing.T, got string) {
				if strings.ContainsAny(got, " !") {
					t.Errorf("name must not contain spaces or special chars: %q", got)
				}
				if !strings.HasPrefix(got, "toolbox-") {
					t.Errorf("toolbox- prefix missing: %q", got)
				}
			},
		},
		{
			name:      "respects 63-char Docker name cap by truncating basename only",
			workspace: "/tmp/" + strings.Repeat("a", 200),
			assertName: func(t *testing.T, got string) {
				if len(got) > 63 {
					t.Errorf("name length %d exceeds 63-char cap: %q", len(got), got)
				}
				if !strings.HasPrefix(got, "toolbox-") {
					t.Errorf("toolbox- prefix missing after truncation: %q", got)
				}
				// Hash suffix must still be 8 hex chars after the final '-'.
				parts := strings.Split(got, "-")
				hash := parts[len(parts)-1]
				if len(hash) != 8 {
					t.Errorf("hash suffix length = %d, want 8: %q", len(hash), got)
				}
			},
		},
	}

	// Capture each subtest's resolved name so we can also assert
	// uniqueness across two cases sharing a basename and determinism
	// across a re-run on the same workspace.
	var firstAlice, firstBob string

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, restore := stubExecShell()
			defer restore()

			var capturedName string
			mock := &mockClient{
				inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
					return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
				},
				imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, nil
				},
				createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, name string) (container.CreateResponse, error) {
					capturedName = name
					return container.CreateResponse{ID: "x"}, nil
				},
			}

			if err := Shell(context.Background(), mock, testPlan(t, tc.workspace, nil)); err != nil {
				t.Fatalf("Shell() error: %v", err)
			}
			tc.assertName(t, capturedName)

			// Cross-case determinism + uniqueness checks.
			switch tc.workspace {
			case "/Users/alice/project/toolbox":
				firstAlice = capturedName
			case "/Users/bob/project/toolbox":
				firstBob = capturedName
				if firstAlice != "" && firstBob == firstAlice {
					t.Errorf("paths with same basename must produce different names: alice=%q bob=%q", firstAlice, firstBob)
				}
			}
		})
	}

	// Determinism: re-running Shell on alice's path must yield the same name.
	if firstAlice != "" {
		_, restore := stubExecShell()
		defer restore()
		var second string
		mock := &mockClient{
			inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
				return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
			},
			imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
				return client.ImageInspectResult{}, nil
			},
			createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, name string) (container.CreateResponse, error) {
				second = name
				return container.CreateResponse{ID: "x"}, nil
			},
		}
		if err := Shell(context.Background(), mock, testPlan(t, "/Users/alice/project/toolbox", nil)); err != nil {
			t.Fatalf("Shell() error on rerun: %v", err)
		}
		if second != firstAlice {
			t.Errorf("containerNameFor not deterministic via Shell: first=%q second=%q", firstAlice, second)
		}
	}
}

func TestShellExecInRunningContainer(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	ws := testWorkspace(t)
	want := sessionplan.ContainerNameFor(ws, "")

	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			if id != want {
				t.Errorf("inspect called with %q, want %q", id, want)
			}
			return container.InspectResponse{
				ID:    "abc123",
				State: &container.State{Running: true},
			}, nil
		},
	}

	err := Shell(context.Background(), mock, testPlan(t, ws, nil))
	if err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !*called {
		t.Fatal("execShellFn was not called")
	}
}

func TestShellStartsStoppedContainer(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	startCalled := false
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ID:    "stopped123",
				State: &container.State{Running: false},
			}, nil
		},
		startFn: func(_ context.Context, id string, _ client.ContainerStartOptions) error {
			startCalled = true
			if id != "stopped123" {
				t.Errorf("start called with id=%q, want %q", id, "stopped123")
			}
			return nil
		},
	}

	err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil))
	if err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !startCalled {
		t.Fatal("ContainerStart was not called")
	}
	if !*called {
		t.Fatal("execShellFn was not called after start")
	}
}

// A ContainerStart the daemon refuses aborts the shell: Shell returns the
// wrapped reason and never execs, rather than attaching to a container that is
// not running. Cleanup is the daemon's — AutoRemove force-removes a container
// whose start failed — so Shell itself removes nothing.
func TestShellAbortsWhenStartFails(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	startCalled := false

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "created123"}, nil
		},
		startFn: func(_ context.Context, _ string, _ client.ContainerStartOptions) error {
			startCalled = true
			return errors.New("daemon refused")
		},
	}

	err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil))
	if err == nil {
		t.Fatal("Shell() should fail when ContainerStart fails")
	}
	if !startCalled {
		t.Fatal("ContainerStart was not called")
	}
	if !strings.Contains(err.Error(), "failed to start container:") {
		t.Errorf("Shell() error = %q, want it to wrap %q", err, "failed to start container:")
	}
	if *called {
		t.Error("execShellFn was called after a failed start: the container is not running")
	}
}

func TestShellCreatesNewContainer(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	ws := testWorkspace(t)
	wantName := sessionplan.ContainerNameFor(ws, "")

	createCalled := false
	startCalled := false
	var capturedBinds []string
	var capturedWorkDir string

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			createCalled = true
			capturedBinds = hostCfg.Binds
			capturedWorkDir = cfg.WorkingDir
			return container.CreateResponse{ID: "new123"}, nil
		},
		startFn: func(_ context.Context, id string, _ client.ContainerStartOptions) error {
			startCalled = true
			if id != "new123" {
				t.Errorf("start called with id=%q, want %q", id, "new123")
			}
			return nil
		},
	}

	err := Shell(context.Background(), mock, testPlan(t, ws, nil))
	if err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !createCalled {
		t.Fatal("ContainerCreate was not called")
	}
	if !startCalled {
		t.Fatal("ContainerStart was not called")
	}
	if !*called {
		t.Fatal("execShellFn was not called after create+start")
	}

	mirror, mirrorOK := mountplan.WorkspaceMirrorPath(ws)
	wantWorkDir := mountplan.WorkspaceTarget
	if mirrorOK {
		wantWorkDir = mirror
	}
	if capturedWorkDir != wantWorkDir {
		t.Errorf("WorkingDir = %q, want %q", capturedWorkDir, wantWorkDir)
	}

	expectedBinds := []string{ws + ":" + mountplan.WorkspaceTarget + ":rw"}
	if mirrorOK {
		expectedBinds = append(expectedBinds, ws+":"+mirror+":rw")
	}
	for _, want := range expectedBinds {
		if !slices.Contains(capturedBinds, want) {
			t.Errorf("expected workspace bind %q in %v", want, capturedBinds)
		}
	}

	if !strings.HasPrefix(wantName, "toolbox-") {
		t.Errorf("expected container name with toolbox- prefix, got %q", wantName)
	}
}

func TestShellSetsCodexSecurityOptByDefault(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	var capturedSecurityOpt []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			capturedSecurityOpt = hostCfg.SecurityOpt
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !slices.Contains(capturedSecurityOpt, "seccomp=unconfined") {
		t.Errorf("expected seccomp=unconfined for default Codex-enabled config, got %v", capturedSecurityOpt)
	}
}

// (Test removed: per-tool opt-out no longer exists. Codex security opt is
// unconditional; see TestShellAppliesCodexSecurityOpt for the always-on
// invariant.)

// TestShellGrantsGroupAddForSockBindTarget pins the create path's half of
// the group-add decision: the targets handed to dockeridentity.Resolve are
// the binds' Target field, never their Source and never the flattened
// "src:target:mode" spec. The bind below deliberately has Source != Target,
// so reading the wrong field (or passing the specs, as this call site did
// before) yields no match and GroupAdd comes back nil — a container that
// starts fine and then fails every in-container `docker` command with
// "permission denied ... /var/run/docker.sock". Only gid 0 is asserted:
// the host GID depends on whether a real socket exists on the test machine.
func TestShellGrantsGroupAddForSockBindTarget(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	var capturedGroupAdd []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			capturedGroupAdd = hostCfg.GroupAdd
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	plan := testPlan(t, testWorkspace(t), nil)
	plan.Binds = []mountplan.Bind{{Source: "/host/alt-docker.sock", Target: "/var/run/docker.sock", Mode: "rw"}}

	if err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !slices.Contains(capturedGroupAdd, "0") {
		t.Errorf("GroupAdd = %v, want it to contain %q for a docker.sock target", capturedGroupAdd, "0")
	}
}

// TestShellMirrorsWorkspaceAtHostPath verifies that a workspace with a safe
// absolute host path is bind-mounted at BOTH /workspace and its own host path,
// and the shell WorkingDir is set to the host path so that $PWD-based bind
// mounts issued from inside the shell (DooD) pass a host-resolvable path to
// the daemon.
func TestShellMirrorsWorkspaceAtHostPath(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	ws := "/Users/alice/projects/demo"

	var capturedBinds []string
	var capturedWorkDir string

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			capturedBinds = hostCfg.Binds
			capturedWorkDir = cfg.WorkingDir
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testPlan(t, ws, nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if capturedWorkDir != ws {
		t.Errorf("WorkingDir = %q, want %q", capturedWorkDir, ws)
	}
	wantCanonical := ws + ":" + mountplan.WorkspaceTarget + ":rw"
	wantMirror := ws + ":" + ws + ":rw"
	if !slices.Contains(capturedBinds, wantCanonical) {
		t.Errorf("missing canonical bind %q in %v", wantCanonical, capturedBinds)
	}
	if !slices.Contains(capturedBinds, wantMirror) {
		t.Errorf("missing mirror bind %q in %v", wantMirror, capturedBinds)
	}
}

// TestShellSkipsMirrorForReservedPath verifies that a workspace nested under a
// reserved in-container directory (e.g. /home/toolbox) is mounted ONLY at
// /workspace to avoid shadowing the container's own filesystem.
func TestShellSkipsMirrorForReservedPath(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	ws := "/home/toolbox/demo"

	var capturedBinds []string
	var capturedWorkDir string

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			capturedBinds = hostCfg.Binds
			capturedWorkDir = cfg.WorkingDir
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testPlan(t, ws, nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if capturedWorkDir != mountplan.WorkspaceTarget {
		t.Errorf("WorkingDir = %q, want %q", capturedWorkDir, mountplan.WorkspaceTarget)
	}
	mirrorBind := ws + ":" + ws + ":rw"
	if slices.Contains(capturedBinds, mirrorBind) {
		t.Errorf("mirror bind must be skipped for reserved path, got %v", capturedBinds)
	}
	canonical := ws + ":" + mountplan.WorkspaceTarget + ":rw"
	if !slices.Contains(capturedBinds, canonical) {
		t.Errorf("expected canonical bind %q in %v", canonical, capturedBinds)
	}
}

func TestShellErrorOnMissingImage(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, &dockertest.NotFoundError{Msg: "no such image"}
		},
		imgPullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (io.ReadCloser, error) {
			// Pull fails (offline) and there is no local image either.
			return nil, errors.New("pull failed")
		},
	}

	err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil))
	if err == nil {
		t.Fatal("Shell() should have returned error for missing image")
	}
	if !strings.Contains(err.Error(), "not available locally") {
		t.Fatalf("error should mention that the image is not available locally, got: %v", err)
	}
}

// (Test removed: per-tool opt-out no longer exists, so there is no
// local-hash auto-build path. The single canonical image is always
// pulled from the registry; TestShellErrorOnMissingImage covers the
// missing-image case.)

func TestShellSurvivesPullFailureWhenImageLocal(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			// Local image exists even though pull failed.
			return client.ImageInspectResult{}, nil
		},
		imgPullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (io.ReadCloser, error) {
			return nil, errors.New("offline")
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil))
	if err != nil {
		t.Fatalf("Shell() should not error when pull fails but local image exists, got: %v", err)
	}
	if !*called {
		t.Fatal("execShellFn was not called")
	}
}

func TestStopAndRemove(t *testing.T) {
	stopCalled := false
	removeCalled := false
	var capturedRemoveOpts client.ContainerRemoveOptions

	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			stopCalled = true
			return nil
		},
		removeFn: func(_ context.Context, _ string, opts client.ContainerRemoveOptions) error {
			removeCalled = true
			capturedRemoveOpts = opts
			return nil
		},
	}

	err := Stop(context.Background(), mock, testWorkspace(t))
	if err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if !stopCalled {
		t.Fatal("ContainerStop was not called")
	}
	if !removeCalled {
		t.Fatal("ContainerRemove was not called")
	}
	if !capturedRemoveOpts.Force {
		t.Fatal("ContainerRemove should use Force=true")
	}
}

func TestStopContainerNotFound(t *testing.T) {
	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			return &dockertest.NotFoundError{Msg: "no such container"}
		},
	}

	err := Stop(context.Background(), mock, testWorkspace(t))
	if err != nil {
		t.Fatalf("Stop() should not error on NotFound, got: %v", err)
	}
}

func TestStopAll(t *testing.T) {
	stopped := map[string]bool{}
	removed := map[string]bool{}

	mock := &mockClient{
		listFn: func(_ context.Context, _ client.ContainerListOptions) ([]container.Summary, error) {
			return []container.Summary{
				{Names: []string{"/toolbox-project-a-abcdef12"}},
				{Names: []string{"/toolbox-project-b-11223344"}},
				{Names: []string{"/toolbox"}}, // legacy singleton
				{Names: []string{"/unrelated-toolbox-clone"}},
			}, nil
		},
		stopFn: func(_ context.Context, name string, _ client.ContainerStopOptions) error {
			stopped[name] = true
			return nil
		},
		removeFn: func(_ context.Context, name string, _ client.ContainerRemoveOptions) error {
			removed[name] = true
			return nil
		},
	}

	err := StopAll(context.Background(), mock)
	if err != nil {
		t.Fatalf("StopAll() error: %v", err)
	}

	expectedStopped := []string{
		"toolbox-project-a-abcdef12",
		"toolbox-project-b-11223344",
		"toolbox",
	}
	for _, name := range expectedStopped {
		if !stopped[name] {
			t.Errorf("expected stop on %q", name)
		}
		if !removed[name] {
			t.Errorf("expected remove on %q", name)
		}
	}
	if stopped["unrelated-toolbox-clone"] {
		t.Error("StopAll should not touch containers outside the toolbox- prefix")
	}
}

// TestShellSetsHostWorkspaceEnv verifies that ContainerCreate receives
// TOOLBOX_HOST_WORKSPACE set to the absolute host workspace path, so that
// nested `docker run -v` invocations against the bind-mounted host socket
// can resolve /workspace/* to a path the host daemon knows.
func TestShellSetsHostWorkspaceEnv(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	ws := testWorkspace(t)
	var capturedEnv []string

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
			capturedEnv = cfg.Env
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testPlan(t, ws, nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	want := "TOOLBOX_HOST_WORKSPACE=" + ws
	if !slices.Contains(capturedEnv, want) {
		t.Errorf("expected env %q in %v", want, capturedEnv)
	}

	mirror, ok := mountplan.WorkspaceMirrorPath(ws)
	wantPWD := "PWD=" + mountplan.WorkspaceTarget
	if ok {
		wantPWD = "PWD=" + mirror
	}
	if !slices.Contains(capturedEnv, wantPWD) {
		t.Errorf("expected env %q in %v", wantPWD, capturedEnv)
	}
}

// TestShellSkipsStopWhenSiblingExecRunning simulates a second terminal
// attached to the same container: on exit of the current shell, the
// ExecIDs list still includes a Running exec, so the container must NOT
// be stopped — otherwise sibling terminals lose their shell.
func TestShellSkipsStopWhenSiblingExecRunning(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	stopCalled := false
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ID:      "abc123",
				State:   &container.State{Running: true},
				ExecIDs: []string{"sibling-exec"},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{Running: true}, nil
		},
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			stopCalled = true
			return nil
		},
	}

	if err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if stopCalled {
		t.Fatal("ContainerStop must not be called while a sibling exec is still Running")
	}
}

// TestShellStopsWhenNoSiblingExecs covers the symmetric path: the only
// exec recorded on the container is already exited (ours), so teardown
// must proceed as normal.
func TestShellStopsWhenNoSiblingExecs(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	stopCalled := false
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ID:      "abc123",
				State:   &container.State{Running: true},
				ExecIDs: []string{"our-exec"},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{Running: false}, nil
		},
		stopFn: func(_ context.Context, _ string, _ client.ContainerStopOptions) error {
			stopCalled = true
			return nil
		},
	}

	if err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !stopCalled {
		t.Fatal("ContainerStop should be called when no sibling exec is running")
	}
}

// captureStderr swaps os.Stderr for a pipe during fn() and returns whatever
// was written there. Used by TestShellPublishMismatchWarning to observe the
// warning emitted via ui.Warning without intercepting the function itself
// (behaviour observed only through the public Shell seam).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}

// TestShellPublishPopulatesBindings verifies the happy path: --publish values
// end up as both ExposedPorts on the container config and PortBindings on the
// host config when a new container is created. Table-driven absorption of the
// former TestParsePublishSpecs happy-path cases.
func TestShellPublishPopulatesBindings(t *testing.T) {
	cases := []struct {
		name     string
		specs    []string
		wantPort string
		wantHost string
		wantHP   string // "" if no specific host port asserted
	}{
		{name: "port only defaults to localhost", specs: []string{"7171"}, wantPort: "7171/tcp", wantHost: "127.0.0.1"},
		{name: "host:container defaults to localhost", specs: []string{"7171:7171"}, wantPort: "7171/tcp", wantHost: "127.0.0.1", wantHP: "7171"},
		{name: "explicit host IP preserved", specs: []string{"0.0.0.0:7171:7171"}, wantPort: "7171/tcp", wantHost: "0.0.0.0", wantHP: "7171"},
		{name: "explicit loopback preserved", specs: []string{"127.0.0.1:7171:7171"}, wantPort: "7171/tcp", wantHost: "127.0.0.1", wantHP: "7171"},
		{name: "udp proto", specs: []string{"7171:7171/udp"}, wantPort: "7171/udp", wantHost: "127.0.0.1", wantHP: "7171"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, restore := stubExecShell()
			defer restore()

			var capturedCfg *container.Config
			var capturedHost *container.HostConfig
			mock := &mockClient{
				inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
					return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
				},
				imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, nil
				},
				createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
					capturedCfg = cfg
					capturedHost = hostCfg
					return container.CreateResponse{ID: "new123"}, nil
				},
			}

			if err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), tc.specs)); err != nil {
				t.Fatalf("Shell() error: %v", err)
			}
			if capturedCfg == nil || capturedHost == nil {
				t.Fatal("ContainerCreate was not invoked")
			}
			port := network.MustParsePort(tc.wantPort)
			if _, ok := capturedCfg.ExposedPorts[port]; !ok {
				t.Errorf("ExposedPorts missing %q: %v", port, capturedCfg.ExposedPorts)
			}
			binds := capturedHost.PortBindings[port]
			if len(binds) != 1 {
				t.Fatalf("want 1 host binding, got %d", len(binds))
			}
			if binds[0].HostIP.String() != tc.wantHost {
				t.Errorf("HostIP = %q, want %q", binds[0].HostIP, tc.wantHost)
			}
			if tc.wantHP != "" && binds[0].HostPort != tc.wantHP {
				t.Errorf("HostPort = %q, want %q", binds[0].HostPort, tc.wantHP)
			}
		})
	}
}

// holderSummary is the container-list entry of a container publishing
// hostPort/tcp with a workspace bind, i.e. what the pre-flight reads to build
// its occupied set.
func holderSummary(name string, hostPort uint16) container.Summary {
	return container.Summary{
		Names: []string{name},
		Ports: []container.PortSummary{{PublicPort: hostPort, Type: "tcp"}},
		Mounts: []container.MountPoint{
			{Destination: mountplan.WorkspaceTarget, Source: "/home/u/other"},
		},
	}
}

// TestShellPortConflictFailsBeforeCreate covers the pre-flight conflict
// check on the create path: port bindings are fixed at ContainerCreate, so a
// host port already held by another container can only produce the daemon's
// opaque "port is already allocated" failure. Shell must fail first, naming
// the holder — and, when the holder is another toolbox, pointing at it as the
// place to finish the login (credentials are shared through ~/.toolbox)
// without ever suggesting the user stop someone else's session.
//
// The table also pins the pass-through half of the contract: a list failure,
// an unpublished port, and a host-port-less publish spec must all let the
// create reach the daemon rather than blocking the shell on a check that
// cannot see everything.
func TestShellPortConflictFailsBeforeCreate(t *testing.T) {
	cases := []struct {
		name        string
		specs       []string
		summaries   []container.Summary
		listErr     error
		wantCreate  bool
		wantSnippet []string
		wantAbsent  []string
	}{
		{
			name:        "toolbox holder points at the shared session",
			specs:       []string{"8877:8877"},
			summaries:   []container.Summary{holderSummary("/toolbox-other-a1b2c3d4", 8877)},
			wantSnippet: []string{"8877/tcp", "toolbox-other-a1b2c3d4", "/home/u/other", "run the login inside it"},
			wantAbsent:  []string{"toolbox stop"},
		},
		{
			name:        "foreign holder gets no toolbox suggestion",
			specs:       []string{"8877:8877"},
			summaries:   []container.Summary{holderSummary("/nginx-proxy", 8877)},
			wantSnippet: []string{"8877/tcp", "nginx-proxy"},
			wantAbsent:  []string{"another toolbox"},
		},
		{
			// PublicPort 0 = exposed but not published: nothing is bound on the
			// host, so it must not register as occupancy.
			name:  "exposed-but-unpublished port is not occupancy",
			specs: []string{"8877:8877"},
			summaries: []container.Summary{{
				Names: []string{"/sibling"},
				Ports: []container.PortSummary{{PrivatePort: 8877, Type: "tcp"}},
			}},
			wantCreate: true,
		},
		{
			// `-p 8877` leaves the host side to the daemon (HostPort ""), so
			// there is no host port to compare and nothing to pre-flight.
			name:       "publish with no host port never conflicts",
			specs:      []string{"8877"},
			summaries:  []container.Summary{holderSummary("/nginx-proxy", 8877)},
			wantCreate: true,
		},
		{
			name:       "list failure waves the create through",
			specs:      []string{"8877:8877"},
			listErr:    errors.New("daemon unreachable"),
			wantCreate: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, restore := stubExecShell()
			defer restore()

			created := false
			mock := &mockClient{
				inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
					return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
				},
				imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, nil
				},
				listFn: func(_ context.Context, _ client.ContainerListOptions) ([]container.Summary, error) {
					return tc.summaries, tc.listErr
				},
				createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
					created = true
					return container.CreateResponse{ID: "x"}, nil
				},
			}

			err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), tc.specs))
			if tc.wantCreate {
				if err != nil {
					t.Fatalf("Shell() error: %v", err)
				}
				if !created {
					t.Error("ContainerCreate must still run when no conflict is known")
				}
				return
			}

			if err == nil {
				t.Fatal("Shell must fail when a wanted host port is already bound")
			}
			for _, want := range tc.wantSnippet {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(err.Error(), absent) {
					t.Errorf("error %q must not mention %q", err, absent)
				}
			}
			if created {
				t.Error("ContainerCreate must not run once a conflict is known")
			}
		})
	}
}

// TestFormatPortConflictGroupsByHolder pins the wording contract the Shell
// table only samples: one line per holder (holders sorted, ports in the
// conflict order ConflictingPublishPorts already sorted them into), the
// workspace suffix omitted for an unknown workspace, and the toolbox hint
// attached to the toolbox holder alone.
func TestFormatPortConflictGroupsByHolder(t *testing.T) {
	got := formatPortConflict(
		[]sessionplan.PortConflict{
			{Port: "8877/tcp", Holder: "toolbox-b-22222222"},
			{Port: "8878/tcp", Holder: "toolbox-b-22222222"},
			{Port: "9000/tcp", Holder: "nginx"},
		},
		map[string]string{"toolbox-b-22222222": "/home/u/b", "nginx": "-"},
	)

	want := "cannot publish ports already bound on this host (bindings are fixed at container creation):" +
		"\n  9000/tcp held by container \"nginx\"" +
		"\n  8877/tcp, 8878/tcp held by container \"toolbox-b-22222222\" (workspace /home/u/b)" +
		"\n    that is another toolbox: run the login inside it — credentials are shared through the ~/.toolbox mounts"
	if got != want {
		t.Errorf("formatPortConflict() =\n%s\nwant\n%s", got, want)
	}
}

// TestShellPublishEmptyYieldsNoBindings verifies the empty-publish path:
// nil specs yield zero ExposedPorts and zero PortBindings on the
// container config handed to Docker. The error cases (bogus / not-a-port)
// are caught at sessionplan.Plan composition time — no Docker client is
// ever constructed, so they live in
// internal/sessionplan/plan_test.go::TestPlanRejectsBadPort.
func TestShellPublishEmptyYieldsNoBindings(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	var capturedCfg *container.Config
	var capturedHost *container.HostConfig
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			capturedCfg = cfg
			capturedHost = hostCfg
			return container.CreateResponse{ID: "x"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testPlan(t, testWorkspace(t), nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedCfg == nil || capturedHost == nil {
		t.Fatal("ContainerCreate was not invoked")
	}
	if len(capturedCfg.ExposedPorts) != 0 {
		t.Errorf("ExposedPorts must be empty for nil specs, got %v", capturedCfg.ExposedPorts)
	}
	if len(capturedHost.PortBindings) != 0 {
		t.Errorf("PortBindings must be empty for nil specs, got %v", capturedHost.PortBindings)
	}
}

// TestShellInspectZeroValueResponse pins the regression: when
// ContainerInspect returns a zero-value container.InspectResponse with a
// nil error (e.g. a future SDK shape change, a misbehaving daemon, or a
// hand-rolled mock returning the zero value), Shell must not treat the
// empty record as a usable container. inspect.State is nil and inspect.ID
// is empty, so the running derivation and the start-by-ID branch would
// both misfire on a half-populated record. The fix gates every field
// access on a single has-data check; an empty record falls through to the
// create-fresh branch (logically: "no usable container record, must
// create"). The test asserts: (a) Shell does not panic, (b) the running
// derivation evaluates to false, (c) warnMissingPublish does not emit a
// "publish mismatch" warning even when the user passed --publish, (d)
// Shell falls through to ContainerCreate (no usable inspect data ⇒ create
// from scratch).
func TestShellInspectZeroValueResponse(t *testing.T) {
	// Stub the real exec — no Docker attach during the test.
	_, restore := stubExecShell()
	defer restore()

	// Sandbox HOME so mountplan.Plan's filesystem touches (~/.toolbox/...)
	// land in tmp. Mirrors TestShellContainerNaming.
	t.Setenv("HOME", t.TempDir())

	ws := testWorkspace(t)

	createCalled := false
	startCalled := false
	mock := &mockClient{
		// Zero-value InspectResponse — no usable container record.
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, nil
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
			createCalled = true
			return container.CreateResponse{ID: "fresh"}, nil
		},
		startFn: func(_ context.Context, _ string, _ client.ContainerStartOptions) error {
			startCalled = true
			return nil
		},
	}

	// Non-empty publish bindings — exercises both the zero-value running
	// derivation and the warnMissingPublish call path on the zero-value path.
	publish := []string{"127.0.0.1:8080:8080"}

	captured := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, testPlan(t, ws, publish)); err != nil {
			t.Fatalf("Shell returned error: %v", err)
		}
	})

	if !createCalled {
		t.Fatalf("expected Shell to fall through to ContainerCreate when inspect data is unusable (zero-value InspectResponse)")
	}
	if !startCalled {
		t.Fatalf("expected Shell to call ContainerStart on the freshly created container")
	}
	// Use Contains against the publish-mismatch substring — captureStderr
	// can also pick up unrelated warnings (mount-skipped, etc.), matching
	// TestShellPublishMismatchWarning's pattern.
	if strings.Contains(captured, "publish mismatch") {
		t.Fatalf("expected no publish-mismatch warning on zero-value path, got stderr: %q", captured)
	}
}

// (Test removed: per-tool opt-out no longer exists, so the shell/tools
// mismatch path is gone. Shell selection still validates the value
// against config.SupportedShells.)

// TestShellCreateUsesResolvedShellCmd: verify the Cmd captured by
// ContainerCreate uses the resolved shell binary. Only zsh is a supported
// interactive shell, but the test keeps the table shape to make future
// shell additions a one-row change rather than a refactor.
func TestShellCreateUsesResolvedShellCmd(t *testing.T) {
	cases := []struct {
		name    string
		shell   string
		wantCmd []string
	}{
		{"default zsh", "zsh", []string{"/bin/zsh"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called, restore := stubExecShell()
			defer restore()

			var capturedCmd []string
			mock := &mockClient{
				inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
					return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
				},
				imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
					return client.ImageInspectResult{}, nil
				},
				imgPullFn: func(_ context.Context, _ string, _ client.ImagePullOptions) (io.ReadCloser, error) {
					return nil, errors.New("offline — use local image")
				},
				createFn: func(_ context.Context, cfg *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
					capturedCmd = cfg.Cmd
					return container.CreateResponse{ID: "new"}, nil
				},
			}

			cfg := &config.Config{Shell: tc.shell}

			if err := Shell(context.Background(), mock, testPlanWithCfg(t, cfg, testWorkspace(t), nil)); err != nil {
				t.Fatalf("Shell() error: %v", err)
			}
			if !*called {
				t.Fatal("execShellFn should have been called")
			}
			if len(capturedCmd) != len(tc.wantCmd) || capturedCmd[0] != tc.wantCmd[0] {
				t.Errorf("Cmd = %v, want %v", capturedCmd, tc.wantCmd)
			}
		})
	}
}
