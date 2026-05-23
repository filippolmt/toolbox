package sessionplan_test

import (
	"testing"

	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// TestResolveShellCmdZsh: the default path returns the zsh binary.
func TestResolveShellCmdZsh(t *testing.T) {
	cmd, err := sessionplan.ResolveShellCmd(testConfig())
	if err != nil {
		t.Fatalf("ResolveShellCmd err = %v, want nil", err)
	}
	if len(cmd) != 1 || cmd[0] != "/bin/zsh" {
		t.Errorf("cmd = %v, want [/bin/zsh]", cmd)
	}
}

// TestNestedSandboxSecurityOpt: codex is always installed → always
// returns seccomp=unconfined.
func TestNestedSandboxSecurityOpt(t *testing.T) {
	got := sessionplan.NestedSandboxSecurityOpt(testConfig())
	if len(got) != 1 || got[0] != "seccomp=unconfined" {
		t.Errorf("NestedSandboxSecurityOpt = %v, want [seccomp=unconfined]", got)
	}
}
