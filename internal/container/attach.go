package container

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"golang.org/x/term"

	"github.com/filippolmt/toolbox/internal/ui"
)

// shellExecEnv returns the host env vars forwarded into the exec session.
//
// Docker exec does NOT inherit the client's environment: ExecCreate
// only sees what we put in ExecCreateOptions.Env, on top of the container's
// Config.Env (set once at ContainerCreate). Without an explicit pass-through
// the shell sees the Docker default TERM=xterm regardless of what the host
// terminal actually is, which on Ghostty + multi-line Starship breaks the
// backspace redraw (plain xterm terminfo lacks the capabilities ZLE/readline
// need). The shell rcs then upgrade TERM to xterm-ghostty when TERM_PROGRAM
// confirms Ghostty is the host — that gate only works if both vars arrive
// here, so forward both.
func shellExecEnv() []string {
	var env []string
	for _, k := range []string{"TERM", "TERM_PROGRAM"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// execShell attaches an interactive shell session to the container using the
// caller-supplied cmd (already resolved upstream by sessionplan.Plan via
// sessionplan.ResolveShellCmd). Handles TTY raw mode, signal forwarding
// (SIGINT/SIGTERM), terminal resize (SIGWINCH), and bidirectional I/O.
func execShell(ctx context.Context, cli client.APIClient, containerID string, cmd []string) error {
	execResp, err := cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		TTY:          true,
		Cmd:          cmd,
		Env:          shellExecEnv(),
	})
	if err != nil {
		return diagnoseExecFailure(ctx, cli, containerID, fmt.Errorf("create exec for container %s: %w", containerID, err))
	}

	resp, err := cli.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		return diagnoseExecFailure(ctx, cli, containerID, fmt.Errorf("attach exec %s: %w", execResp.ID, err))
	}
	defer resp.Close()

	fd := int(os.Stdin.Fd())
	restoreTerm, isTTY, err := rawTerminal(fd)
	if err != nil {
		return err
	}
	defer restoreTerm()

	// Cancellable child context scopes the signal and resize goroutines so
	// they exit cleanly when execShell returns.
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// External SIGINT/SIGTERM: in raw mode the user's ctrl+c is a byte
	// delivered via stdin, so this only catches OS-level kills. Restore the
	// TTY and cancel the session; do NOT os.Exit here — that would skip the
	// caller's container-teardown defers in Shell().
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			restoreTerm()
			cancel()
			// Unblock the main io.Copy on resp.Reader; ctx alone doesn't.
			_ = resp.Conn.Close()
		case <-sessionCtx.Done():
		}
	}()

	// Forward terminal resize (SIGWINCH) to the container exec session.
	if isTTY {
		defer forwardResize(sessionCtx, cli, execResp.ID, fd)()
	}

	// Bidirectional I/O; the stdout copy drives lifecycle. The stdin
	// goroutine leaks until process exit (portable stdin interruption
	// isn't available on Linux/macOS), which is fine for a CLI.
	//
	// Tee the daemon's output through a bounded tail buffer: when the exec
	// dies inside the stream (e.g. runc "cannot exec in a stopped container"
	// because the container ran out of disk), the failure reason arrives here,
	// not as an ExecCreate/ExecAttach error, so capturing the closing bytes is
	// the only way to recognize the cause after the copy returns.
	tail := newTailBuffer(4096)
	go func() { _, _ = io.Copy(resp.Conn, os.Stdin) }()
	_, _ = io.Copy(io.MultiWriter(os.Stdout, tail), resp.Reader)

	return diagnoseSessionExit(ctx, cli, containerID, tail.String())
}

// rawTerminal puts stdin in raw mode so every keypress is captured and
// forwarded to the container, and returns the func that restores it. If stdin
// is not a terminal (e.g. piped), isTTY is false and restore is a no-op.
// Restore runs at most once, so the caller's defer and the signal handler can
// both call it.
func rawTerminal(fd int) (restore func(), isTTY bool, err error) {
	if !term.IsTerminal(fd) {
		noop := func() {
			// Stdin was never put in raw mode, so there is nothing to restore —
			// callers defer this unconditionally.
		}
		return noop, false, nil
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, false, fmt.Errorf("set stdin to raw mode: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if rerr := term.Restore(fd, oldState); rerr != nil {
				ui.Warning("terminal restore failed: " + rerr.Error())
			}
		})
	}, true, nil
}

// forwardResize keeps the container exec's terminal size in sync with the local
// one: an initial resize to sync dimensions, then one per SIGWINCH until ctx is
// done. The returned func detaches the signal handler.
func forwardResize(ctx context.Context, cli client.APIClient, execID string, fd int) (stop func()) {
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)

	resize := func() {
		w, h, err := term.GetSize(fd)
		if err != nil {
			return
		}
		_, _ = cli.ExecResize(ctx, execID, client.ExecResizeOptions{Height: uint(h), Width: uint(w)})
	}
	go func() {
		for {
			select {
			case <-winchCh:
				resize()
			case <-ctx.Done():
				return
			}
		}
	}()
	resize()

	return func() { signal.Stop(winchCh) }
}

// tailBuffer is an io.Writer that retains only the last max bytes written. It
// captures the end of the exec stream (where the daemon writes its failure
// reason) without buffering an entire interactive session in memory.
type tailBuffer struct {
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

// diagnoseExecFailure enriches an exec failure with a startup post-mortem.
// When the exec fails because the container has already exited — the entrypoint
// died before the shell could attach — the raw daemon error is an opaque runc
// "write init-p: broken pipe" that hides the real cause. The most common cause
// is Docker running out of disk space (the entrypoint can't create its state
// dirs), so unless the container is confirmed still running we return a message
// that names that failure mode and the command to confirm it. Only a
// confirmed-running container means the failure is something else, so origErr is
// returned unchanged; an inspect error (AutoRemove already reaped it) or a
// missing/exited state falls through to the diagnostic. origErr is always
// wrapped so callers can still errors.Is it.
//
// This reports the likely cause, not the container's exact log lines: reading
// ContainerLogs races AutoRemove reaping the dead container. Add a log tail here
// if the exact entrypoint error is ever needed.
func diagnoseExecFailure(ctx context.Context, cli client.APIClient, containerID string, origErr error) error {
	inspect, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err == nil && inspect.Container.State != nil && inspect.Container.State.Running {
		// Container is up, so the exec failure is something else — leave it be.
		return origErr
	}
	// Otherwise the container is gone (AutoRemove reaped it) or died at startup.
	// classifyStartupFailure names disk exhaustion outright when origErr carries
	// a known signature, else reports the exit with disk as the likely cause.
	var state *container.State
	if err == nil {
		state = inspect.Container.State
	}
	return fmt.Errorf("%s: %w", classifyStartupFailure(state, origErr.Error()), origErr)
}

// diagnoseSessionExit inspects the container after the exec stream closes.
// A still-running container means the shell exited normally — the user's shell
// is an exec session beside the container's own idle shell (Config.Cmd), so the
// container outlives any shell exit code — and yields nil. A gone or exited container means the session died with it, most often
// because the daemon ran out of disk mid-session; streamTail carries the last
// bytes the daemon wrote so a disk signature is recognized even once AutoRemove
// has reaped the container.
//
// A NotFound inspect means the dead container was already reaped — still a
// diagnostic, driven off the captured stream tail. Any other inspect error is
// a transient daemon hiccup on an otherwise-fine session: return nil rather
// than cry wolf, since the shell had already attached and streamed.
func diagnoseSessionExit(ctx context.Context, cli client.APIClient, containerID, streamTail string) error {
	inspect, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	switch {
	case err == nil:
		if inspect.Container.State != nil && inspect.Container.State.Running {
			return nil
		}
		return fmt.Errorf("shell session ended: %s", classifyStartupFailure(inspect.Container.State, streamTail))
	case cerrdefs.IsNotFound(err):
		return fmt.Errorf("shell session ended: %s", classifyStartupFailure(nil, streamTail))
	default:
		return nil
	}
}
