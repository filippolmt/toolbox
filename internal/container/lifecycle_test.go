package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/filippolmt/toolbox/internal/config"
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
	createFn      func(ctx context.Context, cfg *container.Config, hostCfg *container.HostConfig) (container.CreateResponse, error)
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
		return m.createFn(ctx, cfg, hostCfg)
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
	// Shell: "zsh" matches the Load() default (D-16) so ResolveShellCmd
	// succeeds and tests exercise the SHELL-02 default path.
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

// stubExecShell replaces execShellFn with a no-op and returns a restore callback.
func stubExecShell() (called *bool, restore func()) {
	c := false
	orig := execShellFn
	execShellFn = func(ctx context.Context, cli client.APIClient, cfg *config.Config, containerID string) error {
		c = true
		return nil
	}
	return &c, func() { execShellFn = orig }
}

// --- Tests ---

func TestContainerNameForStableAndUnique(t *testing.T) {
	a := ContainerNameFor("/Users/alice/project/toolbox")
	b := ContainerNameFor("/Users/alice/project/toolbox")
	if a != b {
		t.Fatalf("ContainerNameFor should be deterministic: %q vs %q", a, b)
	}

	c := ContainerNameFor("/Users/bob/project/toolbox")
	if a == c {
		t.Fatalf("paths with same basename must produce different names: both %q", a)
	}

	if !strings.HasPrefix(a, "toolbox-") {
		t.Fatalf("name should start with toolbox- prefix, got %q", a)
	}
	if !strings.Contains(a, "-toolbox-") {
		t.Fatalf("name should embed basename, got %q", a)
	}
}

func TestContainerNameForSanitizesBasename(t *testing.T) {
	name := ContainerNameFor("/tmp/My Weird Dir!")
	if strings.ContainsAny(name, " !") {
		t.Fatalf("name must not contain spaces or special chars: %q", name)
	}
}

// TestContainerNameForRespects63CharLimit: Docker's conventional name limit is
// 63 chars. A workspace basename longer than ~46 chars would overflow — the
// helper must truncate the basename so the stable prefix and hash suffix
// survive intact.
func TestContainerNameForRespects63CharLimit(t *testing.T) {
	long := "/tmp/" + strings.Repeat("a", 200)
	name := ContainerNameFor(long)
	if len(name) > 63 {
		t.Errorf("name length %d exceeds 63-char cap: %q", len(name), name)
	}
	if !strings.HasPrefix(name, "toolbox-") {
		t.Errorf("name must still start with toolbox- after truncation, got %q", name)
	}
	// The hash suffix must still be 8 hex chars appended after the last '-'.
	parts := strings.Split(name, "-")
	hash := parts[len(parts)-1]
	if len(hash) != 8 {
		t.Errorf("hash suffix length = %d, want 8 — truncation should only trim basename: %q", len(hash), name)
	}
}

func TestShellExecInRunningContainer(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	ws := testWorkspace(t)
	want := ContainerNameFor(ws)

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

	err := Shell(context.Background(), mock, testConfig(), ws, nil)
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

	err := Shell(context.Background(), mock, testConfig(), testWorkspace(t), nil)
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
	wantName := ContainerNameFor(ws)

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
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig) (container.CreateResponse, error) {
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

	err := Shell(context.Background(), mock, testConfig(), ws, nil)
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

	mirror, mirrorOK := workspaceMirrorPath(ws)
	wantWorkDir := WorkspaceTarget
	if mirrorOK {
		wantWorkDir = mirror
	}
	if capturedWorkDir != wantWorkDir {
		t.Errorf("WorkingDir = %q, want %q", capturedWorkDir, wantWorkDir)
	}

	expectedBinds := []string{ws + ":" + WorkspaceTarget + ":rw"}
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
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig) (container.CreateResponse, error) {
			capturedSecurityOpt = hostCfg.SecurityOpt
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testConfig(), testWorkspace(t), nil); err != nil {
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
	ensureImage = func(_ context.Context, _ client.APIClient, _ *config.Config, _ string, isLocal bool) error {
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
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig) (container.CreateResponse, error) {
			capturedSecurityOpt = hostCfg.SecurityOpt
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, cfg, testWorkspace(t), nil); err != nil {
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
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig) (container.CreateResponse, error) {
			capturedBinds = hostCfg.Binds
			capturedWorkDir = cfg.WorkingDir
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testConfig(), ws, nil); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if capturedWorkDir != ws {
		t.Errorf("WorkingDir = %q, want %q", capturedWorkDir, ws)
	}
	wantCanonical := ws + ":" + WorkspaceTarget + ":rw"
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
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig) (container.CreateResponse, error) {
			capturedBinds = hostCfg.Binds
			capturedWorkDir = cfg.WorkingDir
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testConfig(), ws, nil); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	if capturedWorkDir != WorkspaceTarget {
		t.Errorf("WorkingDir = %q, want %q", capturedWorkDir, WorkspaceTarget)
	}
	mirrorBind := ws + ":" + ws + ":rw"
	if slices.Contains(capturedBinds, mirrorBind) {
		t.Errorf("mirror bind must be skipped for reserved path, got %v", capturedBinds)
	}
	canonical := ws + ":" + WorkspaceTarget + ":rw"
	if !slices.Contains(capturedBinds, canonical) {
		t.Errorf("expected canonical bind %q in %v", canonical, capturedBinds)
	}
}

func TestWorkspaceMirrorPath(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"/Users/alice/project", "/Users/alice/project", true},
		{"/home/alice/code", "/home/alice/code", true},
		{"/mnt/data/repo", "/mnt/data/repo", true},
		{"/tmp/work/x", "/tmp/work/x", true},
		{"/", "", false},
		{WorkspaceTarget, "", false},
		{WorkspaceTarget + "/nested", "", false},
		{"/home/toolbox", "", false},
		{"/home/toolbox/app", "", false},
		{"/usr/local/src", "", false},
		{"/etc/toolbox", "", false},
		{"", "", false},
		{"relative/path", "", false},
	}
	for _, tc := range cases {
		got, ok := workspaceMirrorPath(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("workspaceMirrorPath(%q) = (%q, %v), want (%q, %v)",
				tc.in, got, ok, tc.want, tc.wantOK)
		}
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

	err := Shell(context.Background(), mock, testConfig(), testWorkspace(t), nil)
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
	ensureImage = func(_ context.Context, _ client.APIClient, _ *config.Config, _ string, isLocal bool) error {
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
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	err := Shell(context.Background(), mock, cfg, testWorkspace(t), nil)
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
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	err := Shell(context.Background(), mock, testConfig(), testWorkspace(t), nil)
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
		createFn: func(_ context.Context, cfg *container.Config, _ *container.HostConfig) (container.CreateResponse, error) {
			capturedEnv = cfg.Env
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	if err := Shell(context.Background(), mock, testConfig(), ws, nil); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}

	want := "TOOLBOX_HOST_WORKSPACE=" + ws
	if !slices.Contains(capturedEnv, want) {
		t.Errorf("expected env %q in %v", want, capturedEnv)
	}

	mirror, ok := workspaceMirrorPath(ws)
	wantPWD := "PWD=" + WorkspaceTarget
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

	if err := Shell(context.Background(), mock, testConfig(), testWorkspace(t), nil); err != nil {
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

	if err := Shell(context.Background(), mock, testConfig(), testWorkspace(t), nil); err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !stopCalled {
		t.Fatal("ContainerStop should be called when no sibling exec is running")
	}
}
