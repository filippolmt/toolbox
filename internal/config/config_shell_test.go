package config

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
)

// TestValidateShellAcceptsSupported is a direct smoke check of the helper.
func TestValidateShellAcceptsSupported(t *testing.T) {
	for _, s := range SupportedShells {
		if err := ValidateShell(s); err != nil {
			t.Errorf("ValidateShell(%q) = %v, want nil", s, err)
		}
	}
}

// TestValidateShellRejectsUnknown covers the error-shape contract.
func TestValidateShellRejectsUnknown(t *testing.T) {
	err := ValidateShell("powershell")
	if err == nil {
		t.Fatal("ValidateShell(\"powershell\") should have errored")
	}
	if !strings.Contains(err.Error(), "powershell") {
		t.Errorf("error should name the offender, got %q", err)
	}
	if !strings.Contains(err.Error(), "zsh") || !strings.Contains(err.Error(), "bash") {
		t.Errorf("error should list the supported shells, got %q", err)
	}
}

// TestKnownToolsIncludesZsh verifies zsh is in the catalog.
func TestKnownToolsIncludesZsh(t *testing.T) {
	found := false
	for _, k := range catalog.Keys() {
		if k == "zsh" {
			found = true
			break
		}
	}
	if !found {
		t.Error("catalog.Keys() should contain \"zsh\"")
	}
}
