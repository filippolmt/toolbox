package container

import (
	"context"
	"errors"
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// attachMock is a minimal Docker SDK mock for the three exec methods used
// by execShell. Embedding client.APIClient gives default panic behavior on
// unmocked methods, surfacing accidental SDK calls as test failures.
type attachMock struct {
	client.APIClient

	createFn func(ctx context.Context, containerID string, opts container.ExecOptions) (container.ExecCreateResponse, error)
	attachFn func(ctx context.Context, execID string, opts container.ExecAttachOptions) (types.HijackedResponse, error)
	resizeFn func(ctx context.Context, execID string, opts container.ResizeOptions) error
}

func (a *attachMock) ContainerExecCreate(ctx context.Context, id string, opts container.ExecOptions) (container.ExecCreateResponse, error) {
	return a.createFn(ctx, id, opts)
}

func (a *attachMock) ContainerExecAttach(ctx context.Context, id string, opts container.ExecAttachOptions) (types.HijackedResponse, error) {
	return a.attachFn(ctx, id, opts)
}

func (a *attachMock) ContainerExecResize(ctx context.Context, id string, opts container.ResizeOptions) error {
	if a.resizeFn != nil {
		return a.resizeFn(ctx, id, opts)
	}
	return nil
}

func TestExecShell_ContainerExecCreateError(t *testing.T) {
	wantErr := errors.New("create boom")
	cli := &attachMock{
		createFn: func(_ context.Context, id string, opts container.ExecOptions) (container.ExecCreateResponse, error) {
			if id != "cid-123" {
				t.Errorf("ContainerExecCreate id = %q, want cid-123", id)
			}
			if !opts.AttachStdin || !opts.AttachStdout || !opts.AttachStderr || !opts.Tty {
				t.Errorf("exec opts missing flags: %+v", opts)
			}
			if got, want := opts.Cmd, []string{"/bin/zsh"}; !reflect.DeepEqual(got, want) {
				t.Errorf("exec Cmd = %v, want %v", got, want)
			}
			return container.ExecCreateResponse{}, wantErr
		},
		attachFn: func(context.Context, string, container.ExecAttachOptions) (types.HijackedResponse, error) {
			t.Fatal("ContainerExecAttach should not be called when ExecCreate fails")
			return types.HijackedResponse{}, nil
		},
	}

	err := execShell(context.Background(), cli, "cid-123", []string{"/bin/zsh"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("execShell err = %v, want %v", err, wantErr)
	}
}

func TestExecShell_ContainerExecAttachError(t *testing.T) {
	wantErr := errors.New("attach boom")
	cli := &attachMock{
		createFn: func(context.Context, string, container.ExecOptions) (container.ExecCreateResponse, error) {
			return container.ExecCreateResponse{ID: "exec-1"}, nil
		},
		attachFn: func(_ context.Context, id string, _ container.ExecAttachOptions) (types.HijackedResponse, error) {
			if id != "exec-1" {
				t.Errorf("ContainerExecAttach id = %q, want exec-1", id)
			}
			return types.HijackedResponse{}, wantErr
		},
	}

	err := execShell(context.Background(), cli, "cid", []string{"/bin/bash"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("execShell err = %v, want %v", err, wantErr)
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
		createFn: func(context.Context, string, container.ExecOptions) (container.ExecCreateResponse, error) {
			return container.ExecCreateResponse{ID: "exec-x"}, nil
		},
		attachFn: func(context.Context, string, container.ExecAttachOptions) (types.HijackedResponse, error) {
			return types.NewHijackedResponse(clientConn, ""), nil
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- execShell(context.Background(), cli, "cid", []string{"/bin/bash"})
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
