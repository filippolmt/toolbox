package container

import (
	"context"
	"errors"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// attachMock is a minimal Docker SDK mock for the exec + inspect methods used
// by execShell. Embedding client.APIClient gives default panic behavior on
// unmocked methods, surfacing accidental SDK calls as test failures.
type attachMock struct {
	client.APIClient

	createFn  func(ctx context.Context, containerID string, opts client.ExecCreateOptions) (client.ExecCreateResult, error)
	attachFn  func(ctx context.Context, execID string, opts client.ExecAttachOptions) (client.HijackedResponse, error)
	resizeFn  func(ctx context.Context, execID string, opts client.ExecResizeOptions) error
	inspectFn func(ctx context.Context, id string) (container.InspectResponse, error)
}

// ContainerInspect feeds diagnoseExecFailure. The default reports a running
// container, so exec-error tests that don't set inspectFn see origErr unchanged.
func (a *attachMock) ContainerInspect(ctx context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	if a.inspectFn != nil {
		inspect, err := a.inspectFn(ctx, id)
		return client.ContainerInspectResult{Container: inspect}, err
	}
	return client.ContainerInspectResult{Container: container.InspectResponse{State: &container.State{Running: true}}}, nil
}

func (a *attachMock) ExecCreate(ctx context.Context, id string, opts client.ExecCreateOptions) (client.ExecCreateResult, error) {
	return a.createFn(ctx, id, opts)
}

func (a *attachMock) ExecAttach(ctx context.Context, id string, opts client.ExecAttachOptions) (client.ExecAttachResult, error) {
	resp, err := a.attachFn(ctx, id, opts)
	return client.ExecAttachResult{HijackedResponse: resp}, err
}

func (a *attachMock) ExecResize(ctx context.Context, id string, opts client.ExecResizeOptions) (client.ExecResizeResult, error) {
	if a.resizeFn != nil {
		return client.ExecResizeResult{}, a.resizeFn(ctx, id, opts)
	}
	return client.ExecResizeResult{}, nil
}

func TestExecShell_ExecCreateError(t *testing.T) {
	wantErr := errors.New("create boom")
	cli := &attachMock{
		createFn: func(_ context.Context, id string, opts client.ExecCreateOptions) (client.ExecCreateResult, error) {
			if id != "cid-123" {
				t.Errorf("ExecCreate id = %q, want cid-123", id)
			}
			if !opts.AttachStdin || !opts.AttachStdout || !opts.AttachStderr || !opts.TTY {
				t.Errorf("exec opts missing flags: %+v", opts)
			}
			if got, want := opts.Cmd, []string{"/bin/zsh"}; !reflect.DeepEqual(got, want) {
				t.Errorf("exec Cmd = %v, want %v", got, want)
			}
			return client.ExecCreateResult{}, wantErr
		},
		attachFn: func(context.Context, string, client.ExecAttachOptions) (client.HijackedResponse, error) {
			t.Fatal("ExecAttach should not be called when ExecCreate fails")
			return client.HijackedResponse{}, nil
		},
	}

	err := execShell(context.Background(), cli, "cid-123", []string{"/bin/zsh"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("execShell err = %v, want %v", err, wantErr)
	}
}

func TestExecShell_ExecAttachError(t *testing.T) {
	wantErr := errors.New("attach boom")
	cli := &attachMock{
		createFn: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
			return client.ExecCreateResult{ID: "exec-1"}, nil
		},
		attachFn: func(_ context.Context, id string, _ client.ExecAttachOptions) (client.HijackedResponse, error) {
			if id != "exec-1" {
				t.Errorf("ExecAttach id = %q, want exec-1", id)
			}
			return client.HijackedResponse{}, wantErr
		},
	}

	err := execShell(context.Background(), cli, "cid", []string{"/bin/zsh"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("execShell err = %v, want %v", err, wantErr)
	}
}

// TestExecShell_ExitedContainerDiagnostic covers the disk-full failure mode:
// the entrypoint dies at startup, the container is exited, and ExecCreate fails
// with an opaque runc error. execShell must replace it with a message naming the
// likely cause (out of disk) while still wrapping the original error.
func TestExecShell_ExitedContainerDiagnostic(t *testing.T) {
	origErr := errors.New("write init-p: broken pipe")
	cli := &attachMock{
		createFn: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
			return client.ExecCreateResult{}, origErr
		},
		inspectFn: func(context.Context, string) (container.InspectResponse, error) {
			return container.InspectResponse{State: &container.State{Running: false, ExitCode: 1}}, nil
		},
	}

	err := execShell(context.Background(), cli, "cid", []string{"/bin/zsh"})
	if err == nil || !strings.Contains(err.Error(), "disk space") {
		t.Fatalf("execShell err = %v, want disk-space diagnostic", err)
	}
	if !errors.Is(err, origErr) {
		t.Fatalf("execShell err = %v, want wrapped origErr %v", err, origErr)
	}
}

// TestExecShell_ExitedContainerReaped covers the same failure once AutoRemove
// has already deleted the dead container: ContainerInspect fails, and execShell
// still surfaces the disk-space hint rather than the raw runc error.
func TestExecShell_ExitedContainerReaped(t *testing.T) {
	origErr := errors.New("write init-p: broken pipe")
	cli := &attachMock{
		createFn: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
			return client.ExecCreateResult{}, origErr
		},
		inspectFn: func(context.Context, string) (container.InspectResponse, error) {
			return container.InspectResponse{}, errors.New("no such container")
		},
	}

	err := execShell(context.Background(), cli, "cid", []string{"/bin/zsh"})
	if err == nil || !strings.Contains(err.Error(), "disk space") {
		t.Fatalf("execShell err = %v, want disk-space diagnostic", err)
	}
	if !errors.Is(err, origErr) {
		t.Fatalf("execShell err = %v, want wrapped origErr %v", err, origErr)
	}
}

// TestExecShell_NonTTYStdin exercises the happy path when stdin is not a
// terminal (piped input). execShell must skip term.MakeRaw and return cleanly
// once the hijacked conn yields EOF.
func TestExecShell_NonTTYStdin(t *testing.T) {
	// Replace os.Stdin with a pipe whose write end is closed → EOF.
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = origStdin
		_ = r.Close()
	})

	// Suppress any bytes the SDK's bufio.Reader might emit (none, in our
	// case) by redirecting os.Stdout to a sink. Avoids polluting test output
	// and makes the test self-contained.
	origStdout := os.Stdout
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	os.Stdout = devNull
	t.Cleanup(func() {
		os.Stdout = origStdout
		_ = devNull.Close()
	})

	// net.Pipe gives us two connected net.Conn ends. Closing the server
	// side immediately makes the client side return EOF on the first read,
	// which unblocks the io.Copy(os.Stdout, resp.Reader) in execShell.
	serverConn, clientConn := net.Pipe()
	if err := serverConn.Close(); err != nil {
		t.Fatalf("close server conn: %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })

	cli := &attachMock{
		createFn: func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error) {
			return client.ExecCreateResult{ID: "exec-x"}, nil
		},
		attachFn: func(context.Context, string, client.ExecAttachOptions) (client.HijackedResponse, error) {
			return client.NewHijackedResponse(clientConn, ""), nil
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- execShell(context.Background(), cli, "cid", []string{"/bin/zsh"})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execShell err = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("execShell did not return within 2s on EOF")
	}
}

func TestShellExecEnv(t *testing.T) {
	cases := []struct {
		name    string
		setVars map[string]string
		want    []string
	}{
		{
			name:    "both vars forwarded",
			setVars: map[string]string{"TERM": "xterm-ghostty", "TERM_PROGRAM": "ghostty"},
			want:    []string{"TERM=xterm-ghostty", "TERM_PROGRAM=ghostty"},
		},
		{
			name:    "only TERM set",
			setVars: map[string]string{"TERM": "xterm-256color"},
			want:    []string{"TERM=xterm-256color"},
		},
		{
			name:    "neither set",
			setVars: map[string]string{},
			want:    nil,
		},
		{
			name:    "empty TERM value forwarded as empty string",
			setVars: map[string]string{"TERM": ""},
			want:    []string{"TERM="},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM", "")
			t.Setenv("TERM_PROGRAM", "")
			if err := os.Unsetenv("TERM"); err != nil {
				t.Fatalf("unset TERM: %v", err)
			}
			if err := os.Unsetenv("TERM_PROGRAM"); err != nil {
				t.Fatalf("unset TERM_PROGRAM: %v", err)
			}
			for k, v := range tc.setVars {
				t.Setenv(k, v)
			}
			if got := shellExecEnv(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("shellExecEnv() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExecShell_ForwardsTermEnv(t *testing.T) {
	t.Setenv("TERM", "xterm-ghostty")
	t.Setenv("TERM_PROGRAM", "ghostty")

	var gotEnv []string
	wantErr := errors.New("stop after capture")
	cli := &attachMock{
		createFn: func(_ context.Context, _ string, opts client.ExecCreateOptions) (client.ExecCreateResult, error) {
			gotEnv = opts.Env
			return client.ExecCreateResult{}, wantErr
		},
		attachFn: func(context.Context, string, client.ExecAttachOptions) (client.HijackedResponse, error) {
			t.Fatal("attach should not be called")
			return client.HijackedResponse{}, nil
		},
	}
	if err := execShell(context.Background(), cli, "cid", []string{"/bin/zsh"}); !errors.Is(err, wantErr) {
		t.Fatalf("execShell err = %v, want %v", err, wantErr)
	}
	want := []string{"TERM=xterm-ghostty", "TERM_PROGRAM=ghostty"}
	if !reflect.DeepEqual(gotEnv, want) {
		t.Errorf("ExecCreateOptions.Env = %v, want %v", gotEnv, want)
	}
}
