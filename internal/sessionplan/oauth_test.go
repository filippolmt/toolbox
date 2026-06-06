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
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"oci"})
	if err != nil {
		t.Fatalf("ExpandOAuth(oci) err = %v, want nil", err)
	}
	if want := []string{"8181:8181"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false, want true")
	}
}

// TestExpandOAuthMultipleTools: repeatable flag composes — both publish
// specs in input order, bridge true when any tool needs it.
func TestExpandOAuthMultipleTools(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"oci", "wrangler"})
	if err != nil {
		t.Fatalf("ExpandOAuth(oci, wrangler) err = %v, want nil", err)
	}
	if want := []string{"8181:8181", "8976:8976"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false, want true")
	}
}

// TestExpandOAuthCfNoBridge: cf is the dynamic-port carve-out — published
// range, no loopback bridge (build-time sed patch handles the callback).
func TestExpandOAuthCfNoBridge(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"cf"})
	if err != nil {
		t.Fatalf("ExpandOAuth(cf) err = %v, want nil", err)
	}
	if want := []string{"8877-8886:8877-8886"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if bridge {
		t.Error("bridge = true, want false")
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
	if !strings.Contains(msg, "cf, codex, oci, shopify, wrangler") {
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

// TestExpandOAuthMixedCfOci: union of publish specs; bridge true because
// oci needs it even though cf does not.
func TestExpandOAuthMixedCfOci(t *testing.T) {
	publish, bridge, err := sessionplan.ExpandOAuth([]string{"cf", "oci"})
	if err != nil {
		t.Fatalf("ExpandOAuth(cf, oci) err = %v, want nil", err)
	}
	if want := []string{"8877-8886:8877-8886", "8181:8181"}; !reflect.DeepEqual(publish, want) {
		t.Errorf("publish = %v, want %v", publish, want)
	}
	if !bridge {
		t.Error("bridge = false, want true")
	}
}
