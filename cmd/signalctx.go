package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// signalCtx returns a context that cancels on SIGINT/SIGTERM and the stop
// function to release the handler. Every command that talks to Docker uses
// this so Ctrl+C during pull/build/stop unwinds cleanly.
func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
