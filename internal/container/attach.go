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
)

// execShell attacca una sessione bash interattiva al container.
// Gestisce TTY raw mode, signal forwarding (SIGINT/SIGTERM),
// resize terminale (SIGWINCH), e I/O bidirezionale.
func execShell(ctx context.Context, cli client.APIClient, containerID string) error {
	execResp, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          []string{"/bin/bash"},
	})
	if err != nil {
		return err
	}

	resp, err := cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return err
	}
	defer resp.Close()

	// TTY raw mode: cattura tutti i keypress e li invia al container.
	// Se non siamo in un terminale (es. pipe), procediamo senza raw mode.
	fd := int(os.Stdin.Fd())
	var oldState *term.State
	if term.IsTerminal(fd) {
		oldState, err = term.MakeRaw(fd)
		if err == nil {
			// Pitfall 1: defer Restore IMMEDIATAMENTE dopo MakeRaw
			defer term.Restore(fd, oldState)
		}
	}

	// Signal handler per restore terminale su crash/kill.
	// In raw mode, ctrl+c viene inviato come byte al container via stdin (io.Copy),
	// non come segnale al processo Go. Questo handler cattura SIGTERM da kill esterni.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if oldState != nil {
			term.Restore(fd, oldState)
		}
		os.Exit(0)
	}()

	// Goroutine per resize terminale (SIGWINCH).
	// Inoltra le dimensioni del terminale host al container exec.
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

		// Resize iniziale per sincronizzare dimensioni.
		w, h, sizeErr := term.GetSize(fd)
		if sizeErr == nil {
			_ = cli.ContainerExecResize(ctx, execResp.ID, container.ResizeOptions{
				Height: uint(h),
				Width:  uint(w),
			})
		}
	}

	// I/O bidirezionale: stdin -> container, container -> stdout.
	// Quando l'utente fa exit/ctrl+d, la copia da Reader termina e la funzione ritorna.
	go func() { _, _ = io.Copy(resp.Conn, os.Stdin) }()
	_, _ = io.Copy(os.Stdout, resp.Reader)

	return nil
}
