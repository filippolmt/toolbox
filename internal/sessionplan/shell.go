package sessionplan

import (
	"github.com/filippolmt/toolbox/internal/config"
)

// ResolveShellCmd returns the container command for the configured shell.
// Re-validates cfg.Shell defensively: Plan() already rejects unsupported
// values, but callers that bypass Plan() (tests, future entry points) must
// not be able to smuggle an arbitrary string into /bin/<x>.
func ResolveShellCmd(cfg *config.Config) ([]string, error) {
	if err := config.ValidateShell(cfg.Shell); err != nil {
		return nil, err
	}
	return []string{"/bin/" + cfg.Shell}, nil
}

// NestedSandboxSecurityOpt returns Docker security options needed by tools
// that create their own Linux sandbox inside toolbox. Codex's built-in
// sandbox uses bubblewrap, which needs user namespaces; Docker's default
// seccomp profile blocks the required clone/unshare calls. Always enabled
// since codex is unconditionally installed.
func NestedSandboxSecurityOpt(_ *config.Config) []string {
	return []string{"seccomp=unconfined"}
}
