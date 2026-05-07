// Package shellcmd holds the shell-command + security-opt resolution
// helpers shared by internal/sessionplan and internal/container.
//
// Why this package exists: internal/sessionplan composes the resolved
// shell command and security options into a SessionPlan; internal/container
// imports internal/sessionplan to consume that plan. If the shell helpers
// lived in internal/container, sessionplan would need to import container
// to compose them — creating a cycle. shellcmd is the cycle-breaker.
package shellcmd

import (
	"fmt"

	"github.com/filippolmt/toolbox/internal/config"
)

// ShellMismatchError is returned when the requested shell cannot be
// launched because the corresponding tools entry is disabled. Callers
// pattern-match on this type to print a remediation message and exit
// non-zero. The Error() message MUST include both the
// `shell: <name>` and `tools.<name>: false` substrings — a smoke
// assertion greps for them.
type ShellMismatchError struct {
	Shell string
}

func (e *ShellMismatchError) Error() string {
	return fmt.Sprintf(
		"shell %q requested but tools.%s is disabled.\n"+
			"  shell: %s\n"+
			"  tools.%s: false\n"+
			"  • set `tools.%s: true` in ~/.toolbox.yaml, OR\n"+
			"  • set `shell: bash` to use bash instead",
		e.Shell, e.Shell, e.Shell, e.Shell, e.Shell)
}

// ResolveShellCmd returns the container command for the configured shell,
// or a typed *ShellMismatchError when the combination is incoherent.
// Re-validates cfg.Shell defensively: Load() already rejects unsupported
// values, but callers that bypass Load() (tests, future entry points)
// must not be able to smuggle an arbitrary string into /bin/<x>.
func ResolveShellCmd(cfg *config.Config) ([]string, error) {
	if err := config.ValidateShell(cfg.Shell); err != nil {
		return nil, err
	}
	if cfg.Shell == "zsh" {
		if enabled, ok := cfg.Tools["zsh"]; ok && !enabled {
			return nil, &ShellMismatchError{Shell: "zsh"}
		}
	}
	return []string{"/bin/" + cfg.Shell}, nil
}

// NestedSandboxSecurityOpt returns Docker security options needed by tools
// that create their own Linux sandbox inside toolbox. Codex's built-in
// sandbox uses bubblewrap, which needs user namespaces; Docker's default
// seccomp profile blocks the required clone/unshare calls.
func NestedSandboxSecurityOpt(cfg *config.Config) []string {
	if enabled, ok := cfg.Tools["codex"]; ok && !enabled {
		return nil
	}
	return []string{"seccomp=unconfined"}
}
