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

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// notFoundError implements the errdefs "not found" interface.
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }
func (e *notFoundError) NotFound()     {}
func (e *notFoundError) Unwrap() error { return nil }

// mockClient implements the subset of client.APIClient used by the tests.
// Unmocked methods panic to surface unexpected calls.
type mockClient struct {
	client.APIClient

	inspectFn     func(ctx context.Context, id string) (container.InspectResponse, error)
	createFn      func(ctx context.Context, cfg *container.Config, hostCfg *container.HostConfig, name string) (container.CreateResponse, error)
	startFn       func(ctx context.Context, id string, opts container.StartOptions) error
	stopFn        func(ctx context.Context, id string, opts container.StopOptions) error
	removeFn      func(ctx context.Context, id string, opts container.RemoveOptions) error
	imgInspFn     func(ctx context.Context, id string) (image.InspectResponse, error)
	imgPullFn     func(ctx context.Context, ref string, opts image.PullOptions) (io.ReadCloser, error)
	listFn        func(ctx context.Context, opts container.ListOptions) ([]container.Summary, error)
	execInspectFn func(ctx context.Context, execID string) (container.ExecInspect, error)
}

func (m *mockClient) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if m.inspectFn != nil {
		return m.inspectFn(ctx, id)
	}
	return container.InspectResponse{}, fmt.Errorf("ContainerInspect not mocked")
}

func (m *mockClient) ContainerCreate(ctx context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, name string) (container.CreateResponse, error) {
	if m.createFn != nil {
		return m.createFn(ctx, cfg, hostCfg, name)
	}
	return container.CreateResponse{}, fmt.Errorf("ContainerCreate not mocked")
}

func (m *mockClient) ContainerStart(ctx context.Context, id string, opts container.StartOptions) error {
	if m.startFn != nil {
		return m.startFn(ctx, id, opts)
	}
	return nil
}

func (m *mockClient) ContainerStop(ctx context.Context, id string, opts container.StopOptions) error {
	if m.stopFn != nil {
		return m.stopFn(ctx, id, opts)
	}
	return nil
}

func (m *mockClient) ContainerRemove(ctx context.Context, id string, opts container.RemoveOptions) error {
	if m.removeFn != nil {
		return m.removeFn(ctx, id, opts)
	}
	return nil
}

func (m *mockClient) ImageInspect(ctx context.Context, id string, _ ...client.ImageInspectOption) (image.InspectResponse, error) {
	if m.imgInspFn != nil {
		return m.imgInspFn(ctx, id)
	}
	return image.InspectResponse{}, fmt.Errorf("ImageInspect not mocked")
}

func (m *mockClient) ImagePull(ctx context.Context, ref string, opts image.PullOptions) (io.ReadCloser, error) {
	if m.imgPullFn != nil {
		return m.imgPullFn(ctx, ref, opts)
	}
	// Default: succeed with an empty body so Shell can proceed.
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (m *mockClient) ContainerList(ctx context.Context, opts container.ListOptions) ([]container.Summary, error) {
	if m.listFn != nil {
		return m.listFn(ctx, opts)
	}
	return nil, fmt.Errorf("ContainerList not mocked")
}

func (m *mockClient) ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error) {
	if m.execInspectFn != nil {
		return m.execInspectFn(ctx, execID)
	}
	return container.ExecInspect{}, fmt.Errorf("ContainerExecInspect not mocked")
}

func (m *mockClient) Close() error { return nil }

// --- Helpers ---

func testConfig() *config.Config {
	// Empty Tools map is treated as default-true, so ResolveImage returns the
	// canonical GHCR image with isLocal=false. That matches the existing test
	// assumptions (pull path, not auto-build).
	// Shell: "zsh" matches the Load() default so ResolveShellCmd succeeds.
	return &config.Config{
		Shell: "zsh",
		Tools: config.DefaultTools(),
	}
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
	plan, err := sessionplan.Plan(testConfig(), workspace, publish, "dev")
	if err != nil {
		t.Fatalf("testPlan: %v", err)
	}
	return plan
}

// testPlanWithCfg is the variant for tests that vary cfg.Tools (e.g.
// codex disabled, custom tools triggering local-build).
func testPlanWithCfg(t *testing.T, cfg *config.Config, workspace string, publish []string) *sessionplan.SessionPlan {
	t.Helper()
	plan, err := sessionplan.Plan(cfg, workspace, publish, "dev")
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
					return container.InspectResponse{}, &notFoundError{msg: "no such container"}
				},
				imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
					return image.InspectResponse{}, nil
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
				return container.InspectResponse{}, &notFoundError{msg: "no such container"}
			},
			imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
				return image.InspectResponse{}, nil
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
	want := sessionplan.ContainerNameFor(ws)

	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			if id != want {
				t.Errorf("inspect called with %q, want %q", id, want)
			}
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc123",
					State: &container.State{Running: true},
				},
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
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "stopped123",
					State: &container.State{Running: false},
				},
			}, nil
		},
		startFn: func(_ context.Context, id string, _ container.StartOptions) error {
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

func TestShellCreatesNewContainer(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	ws := testWorkspace(t)
	wantName := sessionplan.ContainerNameFor(ws)

	createCalled := false
	startCalled := false
	var capturedBinds []string
	var capturedWorkDir string

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			createCalled = true
			capturedBinds = hostCfg.Binds
			capturedWorkDir = cfg.WorkingDir
			return container.CreateResponse{ID: "new123"}, nil
		},
		startFn: func(_ context.Context, id string, _ container.StartOptions) error {
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
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
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

func TestShellSkipsCodexSecurityOptWhenCodexDisabled(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	cfg := testConfig()
	cfg.Tools["codex"] = false

	origEnsure := ensureImage
	ensureImage = func(_ context.Context, _ client.APIClient, _ string, isLocal bool, _ map[string]*string) error {
		if !isLocal {
			t.Error("expected isLocal=true when Codex is disabled")
		}
		return nil
	}
	defer func() { ensureImage = origEnsure }()

	var capturedSecurityOpt []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			capturedSecurityOpt = hostCfg.SecurityOpt
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testPlanWithCfg(t, cfg, testWorkspace(t), nil)); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if len(capturedSecurityOpt) != 0 {
		t.Errorf("expected no security opts when Codex is disabled, got %v", capturedSecurityOpt)
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
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
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
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
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
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, &notFoundError{msg: "no such image"}
		},
		imgPullFn: func(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
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

// TestShellAutoBuildsCustomImage covers the new opt-out path: when the user's
// tools config differs from defaults, ResolveImage returns a local hash-tagged
// ref; if the image is missing, Shell should trigger a build instead of
// erroring out. The build call is stubbed by replacing ensureImage.
func TestShellAutoBuildsCustomImage(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	cfg := &config.Config{Shell: "zsh", Tools: config.DefaultTools()}
	cfg.Tools["gcloud"] = false // triggers local hash tag

	buildCalled := false
	origEnsure := ensureImage
	ensureImage = func(_ context.Context, _ client.APIClient, _ string, isLocal bool, _ map[string]*string) error {
		if !isLocal {
			t.Error("expected isLocal=true when tools differ from defaults")
		}
		buildCalled = true
		return nil
	}
	defer func() { ensureImage = origEnsure }()

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	err := Shell(context.Background(), mock, testPlanWithCfg(t, cfg, testWorkspace(t), nil))
	if err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !buildCalled {
		t.Fatal("ensureImage (auto-build) was not invoked for custom tools config")
	}
	if !*called {
		t.Fatal("execShellFn was not called after auto-build")
	}
}

func TestShellSurvivesPullFailureWhenImageLocal(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			// Local image exists even though pull failed.
			return image.InspectResponse{}, nil
		},
		imgPullFn: func(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
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
	var capturedRemoveOpts container.RemoveOptions

	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			stopCalled = true
			return nil
		},
		removeFn: func(_ context.Context, _ string, opts container.RemoveOptions) error {
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
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			return &notFoundError{msg: "no such container"}
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
		listFn: func(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
			return []container.Summary{
				{Names: []string{"/toolbox-project-a-abcdef12"}},
				{Names: []string{"/toolbox-project-b-11223344"}},
				{Names: []string{"/toolbox"}}, // legacy singleton
				{Names: []string{"/unrelated-toolbox-clone"}},
			}, nil
		},
		stopFn: func(_ context.Context, name string, _ container.StopOptions) error {
			stopped[name] = true
			return nil
		},
		removeFn: func(_ context.Context, name string, _ container.RemoveOptions) error {
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

// Verify notFoundError satisfies cerrdefs.IsNotFound.
func TestNotFoundErrorSatisfiesErrdefs(t *testing.T) {
	err := &notFoundError{msg: "test"}
	if !cerrdefs.IsNotFound(err) {
		t.Fatal("notFoundError should satisfy cerrdefs.IsNotFound")
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
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
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
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:      "abc123",
					State:   &container.State{Running: true},
					ExecIDs: []string{"sibling-exec"},
				},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (container.ExecInspect, error) {
			return container.ExecInspect{Running: true}, nil
		},
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
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
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:      "abc123",
					State:   &container.State{Running: true},
					ExecIDs: []string{"our-exec"},
				},
			}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (container.ExecInspect, error) {
			return container.ExecInspect{Running: false}, nil
		},
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
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
					return container.InspectResponse{}, &notFoundError{msg: "no such container"}
				},
				imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
					return image.InspectResponse{}, nil
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
			port := nat.Port(tc.wantPort)
			if _, ok := capturedCfg.ExposedPorts[port]; !ok {
				t.Errorf("ExposedPorts missing %q: %v", port, capturedCfg.ExposedPorts)
			}
			binds := capturedHost.PortBindings[port]
			if len(binds) != 1 {
				t.Fatalf("want 1 host binding, got %d", len(binds))
			}
			if binds[0].HostIP != tc.wantHost {
				t.Errorf("HostIP = %q, want %q", binds[0].HostIP, tc.wantHost)
			}
			if tc.wantHP != "" && binds[0].HostPort != tc.wantHP {
				t.Errorf("HostPort = %q, want %q", binds[0].HostPort, tc.wantHP)
			}
		})
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
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
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

// TestShellInspectNilContainerJSONBase pins the regression: when
// ContainerInspect returns an InspectResponse whose embedded
// *ContainerJSONBase is nil (e.g. a future SDK shape change, a misbehaving
// daemon, or a hand-rolled mock returning the zero value), Shell must not
// panic on the promoted-field access. inspect.State is a promoted field
// through the embedded pointer, but so are inspect.ID, inspect.HostConfig,
// inspect.ExecIDs, etc. Guarding only the running derivation leaves
// inspect.ID accesses on the start-by-ID branch unprotected. The fix lifts
// the nil-base check into a single hasInspectData boolean that gates every
// promoted-field access; a nil base falls through to the create-fresh
// branch (logically: "no usable container record, must create"). The test
// asserts: (a) Shell does not panic, (b) the running derivation evaluates
// to false, (c) warnMissingPublish does not emit a "publish mismatch"
// warning even when the user passed --publish, (d) Shell falls through to
// ContainerCreate (no usable inspect data ⇒ create from scratch).
func TestShellInspectNilContainerJSONBase(t *testing.T) {
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
		// Zero-value InspectResponse — embedded *ContainerJSONBase is naturally nil.
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, nil
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
			createCalled = true
			return container.CreateResponse{ID: "fresh"}, nil
		},
		startFn: func(_ context.Context, _ string, _ container.StartOptions) error {
			startCalled = true
			return nil
		},
	}

	// Non-empty publish bindings — exercises both the nil-base running
	// derivation and the warnMissingPublish call path on the nil-base path.
	publish := []string{"127.0.0.1:8080:8080"}

	captured := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, testPlan(t, ws, publish)); err != nil {
			t.Fatalf("Shell returned error: %v", err)
		}
	})

	if !createCalled {
		t.Fatalf("expected Shell to fall through to ContainerCreate when inspect data is unusable (nil ContainerJSONBase)")
	}
	if !startCalled {
		t.Fatalf("expected Shell to call ContainerStart on the freshly created container")
	}
	// Use Contains against the publish-mismatch substring — captureStderr
	// can also pick up unrelated warnings (mount-skipped, etc.), matching
	// TestShellPublishMismatchWarning's pattern.
	if strings.Contains(captured, "publish mismatch") {
		t.Fatalf("expected no publish-mismatch warning on nil-base path, got stderr: %q", captured)
	}
}

// TestSessionPlanEarlyExitOnShellMismatch: the shell mismatch is caught at
// sessionplan.Plan composition time, before cmd/shell.go has constructed a
// Docker client. No container is created on this path.
func TestSessionPlanEarlyExitOnShellMismatch(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	ws := t.TempDir()

	cfg := &config.Config{
		Shell: "zsh",
		Tools: map[string]bool{"zsh": false},
	}

	plan, err := sessionplan.Plan(cfg, ws, nil, "dev")
	if err == nil {
		t.Fatal("sessionplan.Plan should have errored for shell:zsh + tools.zsh:false")
	}
	if plan != nil {
		t.Errorf("plan should be nil on error, got %+v", plan)
	}
	var mismatch *sessionplan.ShellMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected *sessionplan.ShellMismatchError, got %T: %v", err, err)
	}
}

// TestShellCreateUsesResolvedShellCmd: verify the Cmd captured by
// ContainerCreate uses the resolved shell binary. Covers both the `shell:
// bash` regression path and the `shell: zsh` default path at the integration
// unit level.
func TestShellCreateUsesResolvedShellCmd(t *testing.T) {
	cases := []struct {
		name    string
		shell   string
		wantCmd []string
	}{
		{"default zsh", "zsh", []string{"/bin/zsh"}},
		{"explicit bash", "bash", []string{"/bin/bash"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called, restore := stubExecShell()
			defer restore()

			var capturedCmd []string
			mock := &mockClient{
				inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
					return container.InspectResponse{}, &notFoundError{msg: "no such container"}
				},
				imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
					return image.InspectResponse{}, nil
				},
				imgPullFn: func(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
					return nil, errors.New("offline — use local image")
				},
				createFn: func(_ context.Context, cfg *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
					capturedCmd = cfg.Cmd
					return container.CreateResponse{ID: "new"}, nil
				},
			}

			cfg := &config.Config{
				Shell: tc.shell,
				Tools: config.DefaultTools(),
			}

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
