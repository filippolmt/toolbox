package config

import (
	"slices"
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
	if !strings.Contains(err.Error(), "zsh") {
		t.Errorf("error should list the supported shells, got %q", err)
	}
}

// TestValidateShellRejectsBashWithMigrationHint covers the breaking-change
// path: existing ~/.toolbox.yaml files with `shell: bash` must surface a
// dedicated error that points at the migration, not the generic "unsupported
// shell" message.
func TestValidateShellRejectsBashWithMigrationHint(t *testing.T) {
	err := ValidateShell("bash")
	if err == nil {
		t.Fatal("ValidateShell(\"bash\") should have errored")
	}
	if !strings.Contains(err.Error(), "no longer supported") {
		t.Errorf("error should flag the removal, got %q", err)
	}
	if !strings.Contains(err.Error(), "zsh") {
		t.Errorf("error should point at the replacement, got %q", err)
	}
}

// TestKnownToolsIncludesZsh verifies zsh is in the catalog.
func TestKnownToolsIncludesZsh(t *testing.T) {
	if !slices.Contains(catalog.Keys(), "zsh") {
		t.Error("catalog.Keys() should contain \"zsh\"")
	}
}
