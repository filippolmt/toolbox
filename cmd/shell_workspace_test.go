package cmd

import (
	"testing"
)

func TestValidateWorkspacePathRejectsColon(t *testing.T) {
	err := validateWorkspacePath("/Users/alice/foo:bar/project")
	if err == nil {
		t.Fatal("paths with ':' must be rejected to avoid bind-format mis-parsing")
	}
	if _, ok := any(err).(interface{ Error() string }); !ok {
		t.Error("expected a conventional error value")
	}
}

func TestValidateWorkspacePathAcceptsCommonPaths(t *testing.T) {
	cases := []string{
		"/Users/alice/project",
		"/home/bob/code-with-dashes",
		"/mnt/data/dir.with.dots",
		"/tmp/a_b_c",
		"/",
	}
	for _, p := range cases {
		if err := validateWorkspacePath(p); err != nil {
			t.Errorf("validateWorkspacePath(%q) = %v, want nil", p, err)
		}
	}
}

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
	// After stop, the context should eventually deliver on Done via the
	// underlying WithCancel. Confirm deadline/err propagation.
	select {
	case <-ctx.Done():
	default:
		// signal.NotifyContext's cancel cancels synchronously.
		t.Error("stop() did not cancel the context")
	}
}
