package cmd

import (
	"reflect"
	"strings"
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

// TestShellOAuthFlagRegistered locks the --oauth flag contract: registered,
// no shorthand, empty default (no preset expansion unless asked).
func TestShellOAuthFlagRegistered(t *testing.T) {
	flag := shellCmd.Flags().Lookup("oauth")
	if flag == nil {
		t.Fatal("--oauth flag not registered on shellCmd")
	}
	if flag.Shorthand != "" {
		t.Errorf("--oauth shorthand = %q, want none", flag.Shorthand)
	}
	if flag.DefValue != "[]" {
		t.Errorf("--oauth default = %q, want \"[]\"", flag.DefValue)
	}
}

// TestShellOAuthOCIEqualsExplicitFlags asserts the preset equivalence the
// spec promises: --oauth oci yields the same (publish, bridge) inputs to
// sessionplan.Plan as an explicit -p 8181:8181 — no -B, because oci binds
// 0.0.0.0:8181 and a socat on eth0:8181 would break its bind (EADDRINUSE).
func TestShellOAuthOCIEqualsExplicitFlags(t *testing.T) {
	oauthPublish, oauthBridge, err := expandShellOAuth(nil, false, []string{"oci"})
	if err != nil {
		t.Fatalf("expandShellOAuth(oci): %v", err)
	}

	explicitPublish, explicitBridge := []string{"8181:8181"}, false
	if !reflect.DeepEqual(oauthPublish, explicitPublish) {
		t.Errorf("publish = %v, want %v (the -p 8181:8181 spelling)", oauthPublish, explicitPublish)
	}
	if oauthBridge != explicitBridge {
		t.Errorf("bridge = %v, want %v (oci must not enable -B)", oauthBridge, explicitBridge)
	}
}

// TestShellOAuthComposesWithExplicitFlags: expansion only adds — explicit
// -p specs stay first and untouched, an explicit -B is never cleared.
func TestShellOAuthComposesWithExplicitFlags(t *testing.T) {
	publish, bridge, err := expandShellOAuth([]string{"3000:3000"}, true, []string{"cf"})
	if err != nil {
		t.Fatalf("expandShellOAuth(cf with explicit flags): %v", err)
	}
	if want := []string{"3000:3000", "8877-8886:8877-8886"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false; explicit -B must never be cleared by expansion")
	}
}

// TestShellOAuthUnknownToolFails: device-code CLIs (gh) are intentionally
// absent — the error lists the sorted supported tools so the user can
// self-correct before any container is created.
func TestShellOAuthUnknownToolFails(t *testing.T) {
	_, _, err := expandShellOAuth(nil, false, []string{"gh"})
	if err == nil {
		t.Fatal("expandShellOAuth(gh) err = nil, want error")
	}
	if !strings.Contains(err.Error(), "cf, codex, glab, oci, sonar, wrangler") {
		t.Errorf("error %q does not list supported tools", err)
	}
}
