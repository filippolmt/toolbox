package cmd

import (
	"os/exec"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// hostBestEffort returns the current host, or — when the home cannot be
// resolved — a host with no home but the real PATH.
//
// For the read-only surfaces whose predecessors discarded the same error:
// `mounts list`, `mounts disable`'s name validation and `config doctor` all
// used to reach mountplan.Merge's `home, _ := os.UserHomeDir()` and degrade —
// an unresolvable home left ~/ paths unexpanded and the inherit_host_auth
// pre-stat reporting them missing, which is a report, not a refusal. Refusing
// instead would hide the mount set a broken $HOME is exactly the reason to go
// looking at.
//
// Keeping exec.LookPath on the fallback is the other half of degrading the way
// they used to: proximo.CAPath asks the binary for the CA path before it falls
// back to ~/.proximo, and that exec never depended on the home. A bare
// fsx.Host{} would resolve no binaries, so `mounts list` on a host with a
// broken $HOME would silently lose the proximo CA mount it used to show.
//
// Every command that must have a home — shell, worktree, bridge — calls
// fsx.CurrentHost directly and surfaces the error.
func hostBestEffort() fsx.Host {
	host, err := fsx.CurrentHost()
	if err != nil {
		return fsx.Host{LookPath: exec.LookPath}
	}
	return host
}
