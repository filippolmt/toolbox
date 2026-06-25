package config

import (
	"strings"
	"testing"
)

// TestValidateAgentAcceptsSupported covers the happy path plus the empty
// (unset) case the validation tail relies on — empty is resolved to
// DefaultAgent later in the cmd layer, so it must not error here.
func TestValidateAgentAcceptsSupported(t *testing.T) {
	for _, s := range append([]string{""}, SupportedAgents...) {
		if err := ValidateAgent(s); err != nil {
			t.Errorf("ValidateAgent(%q) = %v, want nil", s, err)
		}
	}
}

// TestValidateAgentRejectsUnknown covers the error-shape contract.
func TestValidateAgentRejectsUnknown(t *testing.T) {
	err := ValidateAgent("gemini")
	if err == nil {
		t.Fatal("ValidateAgent(\"gemini\") should have errored")
	}
	if !strings.Contains(err.Error(), "gemini") {
		t.Errorf("error should name the offender, got %q", err)
	}
	if !strings.Contains(err.Error(), "claude") || !strings.Contains(err.Error(), "codex") {
		t.Errorf("error should list the supported agents, got %q", err)
	}
}
