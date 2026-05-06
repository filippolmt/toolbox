package config

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/filippolmt/toolbox/internal/catalog"
)

// TestLoadDefaultShellIsZsh verifies SHELL-01: a config without `shell:` key
// resolves to "zsh" (the documented breaking-change default for v1.1).
func TestLoadDefaultShellIsZsh(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Shell != "zsh" {
		t.Errorf("default Shell = %q, want %q", cfg.Shell, "zsh")
	}
}

// TestLoadShellBash verifies SHELL-01: explicit `shell: bash` overrides the
// default and round-trips through Viper.
func TestLoadShellBash(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("shell: bash\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Shell != "bash" {
		t.Errorf("Shell = %q, want %q", cfg.Shell, "bash")
	}
}

// TestLoadShellInvalid verifies SHELL-04: an unsupported value fails Load()
// with an error message that names the offender AND lists the supported set.
func TestLoadShellInvalid(t *testing.T) {
	viper.Reset()
	setToolsDefaults()

	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(bytes.NewBufferString("shell: fish\n")); err != nil {
		t.Fatalf("viper.ReadConfig: %v", err)
	}

	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load() should have failed for shell: fish, got cfg=%+v", cfg)
	}
	msg := err.Error()
	for _, want := range []string{"fish", "zsh", "bash"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should contain %q", msg, want)
		}
	}
}

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

// TestKnownToolsIncludesZsh verifies ZSH-01 (Go-side half): zsh is a tool key
// the build system recognises, wired to INSTALL_ZSH.
func TestKnownToolsIncludesZsh(t *testing.T) {
	found := false
	for _, k := range catalog.Keys() {
		if k == "zsh" {
			found = true
			break
		}
	}
	if !found {
		t.Error("catalog.Keys() should contain \"zsh\" (alphabetic, after \"yq\")")
	}
	if got := catalog.BuildArg("zsh"); got != "INSTALL_ZSH" {
		t.Errorf("catalog.BuildArg(\"zsh\") = %q, want %q", got, "INSTALL_ZSH")
	}
}
