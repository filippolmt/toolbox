package bridge

import (
	"context"
	"testing"
)

// An op outside credentialSubcommand must fail before any exec — the daemon
// already gates on the same map, so this is defense in depth.
func TestRunHostCredential_UnknownOp(t *testing.T) {
	out, exit, err := runHostCredential(context.Background(), "delete", nil)
	if err == nil {
		t.Fatalf("want error for unknown op, got out=%q exit=%d", out, exit)
	}
	if out != nil || exit != 0 {
		t.Errorf("unknown op should not exec: out=%q exit=%d", out, exit)
	}
}
