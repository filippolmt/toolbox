package cmd

import (
	"testing"
)

// TestShellBridgeLoopbackFlagDefaultsFalse locks the opt-in contract: the
// loopback bridge is only ever enabled when the user explicitly passes -B
// / --bridge-loopback. Default invocations never plumb the env into the
// container.
func TestShellBridgeLoopbackFlagDefaultsFalse(t *testing.T) {
	shellBridgeLoopback = false // reset in case a prior test toggled it
	t.Cleanup(func() { shellBridgeLoopback = false })

	flag := shellCmd.Flags().Lookup("bridge-loopback")
	if flag == nil {
		t.Fatal("--bridge-loopback flag not registered on shellCmd")
	}
	if flag.DefValue != "false" {
		t.Errorf("--bridge-loopback default = %q, want \"false\"", flag.DefValue)
	}
	if flag.Shorthand != "B" {
		t.Errorf("--bridge-loopback shorthand = %q, want \"B\"", flag.Shorthand)
	}
}

// TestShellBridgeLoopbackFlagParsesShort exercises the cobra flag parser
// against -B and confirms the variable lands true. Paired with the long-
// form test below to lock both surfaces.
func TestShellBridgeLoopbackFlagParsesShort(t *testing.T) {
	shellBridgeLoopback = false
	t.Cleanup(func() {
		shellBridgeLoopback = false
		_ = shellCmd.Flags().Set("bridge-loopback", "false")
	})

	if err := shellCmd.Flags().Parse([]string{"-B"}); err != nil {
		t.Fatalf("Parse(-B): %v", err)
	}
	if !shellBridgeLoopback {
		t.Error("shellBridgeLoopback = false after -B; want true")
	}
}

// TestShellBridgeLoopbackFlagParsesLong is the long-form counterpart.
func TestShellBridgeLoopbackFlagParsesLong(t *testing.T) {
	shellBridgeLoopback = false
	t.Cleanup(func() {
		shellBridgeLoopback = false
		_ = shellCmd.Flags().Set("bridge-loopback", "false")
	})

	if err := shellCmd.Flags().Parse([]string{"--bridge-loopback"}); err != nil {
		t.Fatalf("Parse(--bridge-loopback): %v", err)
	}
	if !shellBridgeLoopback {
		t.Error("shellBridgeLoopback = false after --bridge-loopback; want true")
	}
}
