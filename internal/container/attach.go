package container

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

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

	// TTY raw mode: capture every keypress and forward it to the container.
	// If stdin is not a terminal (e.g. piped), proceed without raw mode.
	fd := int(os.Stdin.Fd())
	var oldState *term.State
	if term.IsTerminal(fd) {
		oldState, err = term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("set stdin to raw mode: %w", err)
		}
	}

	// Restore TTY exactly once, whether via defer or the signal handler.
	var restoreOnce sync.Once
	restoreTerm := func() {
		if oldState == nil {
			return
		}
		restoreOnce.Do(func() {
			if rerr := term.Restore(fd, oldState); rerr != nil {
				ui.Warning("terminal restore failed: " + rerr.Error())
			}
		})
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
	if oldState != nil {
		winchCh := make(chan os.Signal, 1)
		signal.Notify(winchCh, syscall.SIGWINCH)
		defer signal.Stop(winchCh)

		go func() {
			for {
				select {
				case <-winchCh:
					w, h, sizeErr := term.GetSize(fd)
					if sizeErr != nil {
						continue
					}
					_, _ = cli.ExecResize(sessionCtx, execResp.ID, client.ExecResizeOptions{
						Height: uint(h),
						Width:  uint(w),
					})
				case <-sessionCtx.Done():
					return
				}
			}
		}()

		// Initial resize to sync dimensions.
		if w, h, sizeErr := term.GetSize(fd); sizeErr == nil {
			_, _ = cli.ExecResize(sessionCtx, execResp.ID, client.ExecResizeOptions{
				Height: uint(h),
				Width:  uint(w),
			})
		}
	}

	// Bidirectional I/O; the stdout copy drives lifecycle. The stdin
	// goroutine leaks until process exit (portable stdin interruption
	// isn't available on Linux/macOS), which is fine for a CLI.
	go func() { _, _ = io.Copy(resp.Conn, os.Stdin) }()
	_, _ = io.Copy(os.Stdout, resp.Reader)

	return nil
}

// diagnoseExecFailure enriches an exec failure with a startup post-mortem.
// When the exec fails because the container has already exited — the entrypoint
// died before the shell could attach — the raw daemon error is an opaque runc
// "write init-p: broken pipe" that hides the real cause. The most common cause
// is Docker running out of disk space (the entrypoint can't create its state
// dirs), so when the container is gone or no longer running we return a message
// that names that failure mode and the command to confirm it. When the
// container is still running the failure is something else and origErr is
// returned unchanged. origErr is always wrapped so callers can still errors.Is
// it. ponytail: reports the likely cause, not the container's exact log lines —
// reading ContainerLogs races AutoRemove reaping the dead container; add a log
// tail here if the exact entrypoint error is ever needed.
func diagnoseExecFailure(ctx context.Context, cli client.APIClient, containerID string, origErr error) error {
	inspectResult, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		// AutoRemove likely already reaped the exited container.
		return fmt.Errorf("container exited at startup before the shell could attach — "+
			"Docker may be out of disk space; check with `docker system df`: %w", origErr)
	}
	state := inspectResult.Container.State
	if state == nil || state.Running {
		return origErr
	}
	return fmt.Errorf("container exited at startup (exit %d) before the shell could attach — "+
		"Docker may be out of disk space; check with `docker system df`: %w", state.ExitCode, origErr)
}
