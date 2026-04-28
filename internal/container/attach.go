package container

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"golang.org/x/term"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/ui"
)

// execShell attaches an interactive shell session (zsh or bash per cfg.Shell)
// to the container. Handles TTY raw mode, signal forwarding (SIGINT/SIGTERM),
// terminal resize (SIGWINCH), and bidirectional I/O.
func execShell(ctx context.Context, cli client.APIClient, cfg *config.Config, containerID string) error {
	execResp, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          []string{"/bin/" + cfg.Shell},
	})
	if err != nil {
		return fmt.Errorf("create exec for container %s: %w", containerID, err)
	}

	resp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return fmt.Errorf("attach exec %s: %w", execResp.ID, err)
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
					_ = cli.ContainerExecResize(sessionCtx, execResp.ID, container.ResizeOptions{
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
			_ = cli.ContainerExecResize(sessionCtx, execResp.ID, container.ResizeOptions{
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
