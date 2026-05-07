package shellcmd_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/shellcmd"
)

// TestResolveShellCmdZshEnabled: the default path returns the zsh binary
// when tools.zsh is enabled.
func TestResolveShellCmdZshEnabled(t *testing.T) {
	cfg := &config.Config{
		Shell: "zsh",
		Tools: config.DefaultTools(),
	}
	cmd, err := shellcmd.ResolveShellCmd(cfg)
	if err != nil {
		t.Fatalf("ResolveShellCmd err = %v, want nil", err)
	}
	if len(cmd) != 1 || cmd[0] != "/bin/zsh" {
		t.Errorf("cmd = %v, want [/bin/zsh]", cmd)
	}
}

// TestResolveShellCmdBash: bash selection returns /bin/bash regardless of
// tools.zsh (bash is always available).
func TestResolveShellCmdBash(t *testing.T) {
	cfg := &config.Config{
		Shell: "bash",
		Tools: config.DefaultTools(),
	}
	cmd, err := shellcmd.ResolveShellCmd(cfg)
	if err != nil {
		t.Fatalf("ResolveShellCmd err = %v, want nil", err)
	}
	if len(cmd) != 1 || cmd[0] != "/bin/bash" {
		t.Errorf("cmd = %v, want [/bin/bash]", cmd)
	}
}

// TestResolveShellCmdZshDisabledError: the incoherent shell+tools
// combination fails with a typed error whose message contains the two
// canonical substrings.
func TestResolveShellCmdZshDisabledError(t *testing.T) {
	cfg := &config.Config{
		Shell: "zsh",
		Tools: map[string]bool{"zsh": false},
	}
	cmd, err := shellcmd.ResolveShellCmd(cfg)
	if err == nil {
		t.Fatalf("ResolveShellCmd should have errored, got cmd=%v", cmd)
	}
	if cmd != nil {
		t.Errorf("cmd should be nil on error, got %v", cmd)
	}
	var mismatch *shellcmd.ShellMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("err should be *ShellMismatchError, got %T: %v", err, err)
	}
	if mismatch.Shell != "zsh" {
		t.Errorf("ShellMismatchError.Shell = %q, want %q", mismatch.Shell, "zsh")
	}
	msg := err.Error()
	for _, want := range []string{"shell: zsh", "tools.zsh: false"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should contain %q", msg, want)
		}
	}
}

// TestNestedSandboxSecurityOptDefault: codex enabled (or absent) → seccomp=unconfined.
func TestNestedSandboxSecurityOptDefault(t *testing.T) {
	cfg := &config.Config{Shell: "zsh", Tools: config.DefaultTools()}
	got := shellcmd.NestedSandboxSecurityOpt(cfg)
	if len(got) != 1 || got[0] != "seccomp=unconfined" {
		t.Errorf("NestedSandboxSecurityOpt = %v, want [seccomp=unconfined]", got)
	}
}

// TestNestedSandboxSecurityOptCodexDisabled: tools.codex=false → nil.
func TestNestedSandboxSecurityOptCodexDisabled(t *testing.T) {
	cfg := &config.Config{
		Shell: "zsh",
		Tools: map[string]bool{"codex": false},
	}
	got := shellcmd.NestedSandboxSecurityOpt(cfg)
	if got != nil {
		t.Errorf("NestedSandboxSecurityOpt = %v, want nil", got)
	}
}
