package sessionplan_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// TestExpandOAuthSingleTool: one bridge-backed tool yields its publish spec
// and the bridge bit.
func TestExpandOAuthSingleTool(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"wrangler"})
	if err != nil {
		t.Fatalf("ExpandOAuth(wrangler) err = %v, want nil", err)
	}
	if want := []string{"8976:8976"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false, want true")
	}
}

// TestExpandOAuthOciNoBridge: oci binds 0.0.0.0:8181 (wildcard), so the
// eth0 forward reaches it directly — and a socat on eth0:8181 would make
// its bind fail EADDRINUSE. Publish only, no bridge.
func TestExpandOAuthOciNoBridge(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"oci"})
	if err != nil {
		t.Fatalf("ExpandOAuth(oci) err = %v, want nil", err)
	}
	if want := []string{"8181:8181"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if bridge {
		t.Error("bridge = true, want false (socat would collide with oci's wildcard bind)")
	}
}

// TestExpandOAuthGlabNoBridge: glab binds 0.0.0.0:7171 (wildcard), so the
// eth0 forward reaches it directly — and a socat on eth0:7171 would make
// its bind fail EADDRINUSE. Publish only, no bridge.
func TestExpandOAuthGlabNoBridge(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"glab"})
	if err != nil {
		t.Fatalf("ExpandOAuth(glab) err = %v, want nil", err)
	}
	if want := []string{"7171:7171"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if bridge {
		t.Error("bridge = true, want false (socat would collide with glab's wildcard bind)")
	}
}

// TestExpandOAuthMultipleTools: repeatable flag composes — both publish
// specs in input order, bridge true when any tool needs it.
func TestExpandOAuthMultipleTools(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"codex", "wrangler"})
	if err != nil {
		t.Fatalf("ExpandOAuth(codex, wrangler) err = %v, want nil", err)
	}
	if want := []string{"1455:1455", "8976:8976"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false, want true")
	}
}

// TestExpandOAuthCfRangeBridge: cf binds 127.0.0.1 on a port from its
// callback range (startPort 8877, maxPortAttempts 10) and advertises a
// localhost redirect_uri, so the recipe publishes the whole range AND
// bridges it — same shape as sonar, no dist patch.
func TestExpandOAuthCfRangeBridge(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"cf"})
	if err != nil {
		t.Fatalf("ExpandOAuth(cf) err = %v, want nil", err)
	}
	if want := []string{"8877-8886:8877-8886"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false, want true (cf binds container loopback)")
	}
}

// TestExpandOAuthSonarRangeBridge: sonar binds 127.0.0.1 on the first free
// port in the fixed 64120-64130 range (server rejects out-of-range callback
// ports), so the recipe publishes the whole range AND bridges it.
func TestExpandOAuthSonarRangeBridge(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"sonar"})
	if err != nil {
		t.Fatalf("ExpandOAuth(sonar) err = %v, want nil", err)
	}
	if want := []string{"64120-64130:64120-64130"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false, want true (sonar binds container loopback)")
	}
}

// TestExpandOAuthUnknownTool: hard error naming the offender and listing
// the sorted supported tools (device-code CLIs intentionally absent).
func TestExpandOAuthUnknownTool(t *testing.T) {
	_, _, err := sessionplan.ExpandOAuth([]string{"gh"})
	if err == nil {
		t.Fatal("ExpandOAuth(gh) err = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"gh"`) {
		t.Errorf("error %q does not name the unknown tool", msg)
	}
	if !strings.Contains(msg, "cf, codex, glab, oci, sonar, wrangler") {
		t.Errorf("error %q does not list sorted supported tools", msg)
	}
}

// TestExpandOAuthEmptyInput: no tools → no specs, no bridge, no error.
func TestExpandOAuthEmptyInput(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth(nil)
	if err != nil {
		t.Fatalf("ExpandOAuth(nil) err = %v, want nil", err)
	}
	if len(publish) != 0 {
		t.Errorf("publish = %v, want empty", publish)
	}
	if bridge {
		t.Error("bridge = true, want false")
	}
}

// TestExpandOAuthMixedCfWrangler: union of publish specs; bridge true —
// both cf and wrangler bind container loopback.
func TestExpandOAuthMixedCfWrangler(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"cf", "wrangler"})
	if err != nil {
		t.Fatalf("ExpandOAuth(cf, wrangler) err = %v, want nil", err)
	}
	if want := []string{"8877-8886:8877-8886", "8976:8976"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false, want true")
	}
}

// TestExpandOAuthMixedCfOci: cf now bridges (binds container loopback), so
// pairing it with the wildcard-bind oci flips the bridge bit — which would
// put socat on eth0:8181 and break oci's 0.0.0.0:8181 bind (EADDRINUSE).
// ExpandOAuth is pure (it only ORs the bits); the union being a footgun is
// documented in docs/commands.md — cf must be authenticated in its own
// session, like wrangler+oci.
func TestExpandOAuthMixedCfOci(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"cf", "oci"})
	if err != nil {
		t.Fatalf("ExpandOAuth(cf, oci) err = %v, want nil", err)
	}
	if want := []string{"8877-8886:8877-8886", "8181:8181"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false, want true (cf now binds container loopback)")
	}
}
