package container

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"

	"github.com/filippolmt/toolbox/internal/config"
)

// TestResolveShellCmdZshEnabled (SHELL-02, D-22): the default path returns
// the zsh binary when tools.zsh is enabled.
func TestResolveShellCmdZshEnabled(t *testing.T) {
	cfg := &config.Config{
		Shell: "zsh",
		Tools: config.DefaultTools(),
	}
	cmd, err := ResolveShellCmd(cfg)
	if err != nil {
		t.Fatalf("ResolveShellCmd err = %v, want nil", err)
	}
	if len(cmd) != 1 || cmd[0] != "/bin/zsh" {
		t.Errorf("cmd = %v, want [/bin/zsh]", cmd)
	}
}

// TestResolveShellCmdBash (SHELL-02, D-22): bash selection returns /bin/bash
// regardless of tools.zsh (bash is always available).
func TestResolveShellCmdBash(t *testing.T) {
	cfg := &config.Config{
		Shell: "bash",
		Tools: config.DefaultTools(),
	}
	cmd, err := ResolveShellCmd(cfg)
	if err != nil {
		t.Fatalf("ResolveShellCmd err = %v, want nil", err)
	}
	if len(cmd) != 1 || cmd[0] != "/bin/bash" {
		t.Errorf("cmd = %v, want [/bin/bash]", cmd)
	}
}

// TestResolveShellCmdZshDisabledError (SHELL-03, D-22): the incoherent
// combination fails with a typed error whose message contains the two
// substrings locked by SPEC Requirement 10 acceptance.
func TestResolveShellCmdZshDisabledError(t *testing.T) {
	cfg := &config.Config{
		Shell: "zsh",
		Tools: map[string]bool{"zsh": false},
	}
	cmd, err := ResolveShellCmd(cfg)
	if err == nil {
		t.Fatalf("ResolveShellCmd should have errored, got cmd=%v", cmd)
	}
	if cmd != nil {
		t.Errorf("cmd should be nil on error, got %v", cmd)
	}
	var mismatch *ShellMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err should be *ShellMismatchError, got %T: %v", err, err)
	}
	if mismatch.Shell != "zsh" {
		t.Errorf("ShellMismatchError.Shell = %q, want %q", mismatch.Shell, "zsh")
	}
	msg := err.Error()
	for _, want := range []string{"shell: zsh", "tools.zsh: false"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should contain %q (SPEC Requirement 10)", msg, want)
		}
	}
}

// TestShellEarlyExitOnShellMismatch (SHELL-03, D-22): Shell() must return the
// mismatch error BEFORE any Docker API call. This is the "no container
// created" acceptance from SPEC Requirement 10.
func TestShellEarlyExitOnShellMismatch(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	cfg := &config.Config{
		Shell: "zsh",
		Tools: map[string]bool{"zsh": false},
	}

	inspectCalls := 0
	createCalls := 0
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			inspectCalls++
			return container.InspectResponse{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig) (container.CreateResponse, error) {
			createCalls++
			return container.CreateResponse{}, nil
		},
	}

	err := Shell(context.Background(), mock, cfg, testWorkspace(t), nil)
	if err == nil {
		t.Fatal("Shell() should have returned an error for shell:zsh + tools.zsh:false")
	}
	var mismatch *ShellMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected *ShellMismatchError, got %T: %v", err, err)
	}
	if inspectCalls != 0 {
		t.Errorf("ContainerInspect should NOT be called on shell mismatch, got %d calls", inspectCalls)
	}
	if createCalls != 0 {
		t.Errorf("ContainerCreate should NOT be called on shell mismatch, got %d calls", createCalls)
	}
}

// TestShellCreateUsesResolvedShellCmd (SHELL-02): verify the Cmd captured by
// ContainerCreate uses the resolved shell binary. Covers both the `shell:
// bash` regression path and the `shell: zsh` default path at the integration
// unit level.
//
// X-08: explicit `imgPullFn` + `imgInspFn` mock wiring mirrors the pattern in
// TestShellAutoBuildsCustomImage (lifecycle_test.go:322). Without an explicit
// `imgPullFn`, the test would depend on the default mock behaviour for
// ImagePull — which may change — and become flaky. Wiring it to return an
// offline error forces the code down the "image present locally" branch,
// matching what TestShellSurvivesPullFailureWhenImageLocal already does.
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
				// Local image exists — short-circuit the pull path.
				imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
					return image.InspectResponse{}, nil
				},
				// X-08: explicit imgPullFn (mirrors
				// TestShellAutoBuildsCustomImage / TestShellSurvivesPullFailureWhenImageLocal).
				// Offline error forces deterministic "use local image" path —
				// test is not sensitive to changes in the default mock's
				// ImagePull behaviour.
				imgPullFn: func(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
					return nil, errors.New("offline — use local image")
				},
				createFn: func(_ context.Context, cfg *container.Config, _ *container.HostConfig) (container.CreateResponse, error) {
					capturedCmd = cfg.Cmd
					return container.CreateResponse{ID: "new"}, nil
				},
			}

			cfg := &config.Config{
				Shell: tc.shell,
				Tools: config.DefaultTools(),
			}

			if err := Shell(context.Background(), mock, cfg, testWorkspace(t), nil); err != nil {
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
