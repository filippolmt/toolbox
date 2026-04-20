package container

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"golang.org/x/term"

	"github.com/filippolmt/toolbox/internal/config"
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
		return err
	}

	resp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return err
	}
	defer resp.Close()

	// TTY raw mode: capture every keypress and forward it to the container.
	// If stdin is not a terminal (e.g. piped), proceed without raw mode.
	fd := int(os.Stdin.Fd())
	var oldState *term.State
	if term.IsTerminal(fd) {
		oldState, err = term.MakeRaw(fd)
		if err == nil {
			// Pitfall 1: defer Restore IMMEDIATELY after MakeRaw.
			defer term.Restore(fd, oldState)
		}
	}

	// Signal handler that restores the terminal on crash/kill.
	// In raw mode, ctrl+c is sent as a byte to the container via stdin (io.Copy),
	// not as a signal to the Go process. This handler catches external SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if oldState != nil {
			term.Restore(fd, oldState)
		}
		os.Exit(0)
	}()

	// Forward terminal resize (SIGWINCH) to the container exec session.
	if oldState != nil {
		go func() {
			winchCh := make(chan os.Signal, 1)
			signal.Notify(winchCh, syscall.SIGWINCH)
			for range winchCh {
				w, h, sizeErr := term.GetSize(fd)
				if sizeErr == nil {
					_ = cli.ContainerExecResize(ctx, execResp.ID, container.ResizeOptions{
						Height: uint(h),
						Width:  uint(w),
					})
				}
			}
		}()

		// Initial resize to sync dimensions.
		w, h, sizeErr := term.GetSize(fd)
		if sizeErr == nil {
			_ = cli.ContainerExecResize(ctx, execResp.ID, container.ResizeOptions{
				Height: uint(h),
				Width:  uint(w),
			})
		}
	}

	// Bidirectional I/O: stdin -> container, container -> stdout.
	// When the user exits (ctrl+d), the reader copy terminates and we return.
	go func() { _, _ = io.Copy(resp.Conn, os.Stdin) }()
	_, _ = io.Copy(os.Stdout, resp.Reader)

	return nil
}
