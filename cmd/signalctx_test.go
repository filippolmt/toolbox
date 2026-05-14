package cmd

import "testing"

// TestSignalCtxReturnsCancellableContext — signalCtx must return a non-nil
// context that the caller can cancel via the returned stop function. Smoke
// check that the Done channel is wired up.
func TestSignalCtxReturnsCancellableContext(t *testing.T) {
	ctx, stop := signalCtx()
	if ctx == nil {
		t.Fatal("signalCtx returned nil context")
	}
	if ctx.Done() == nil {
		t.Fatal("signalCtx context has no Done channel")
	}
	stop()
	select {
	case <-ctx.Done():
	default:
		t.Error("stop() did not cancel the context")
	}
}
