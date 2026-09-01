package cmd

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// execSelf is the syscall.Exec seam. It is the one line on the reload path no
// automated gate can exercise — the call replaces the test binary, so a unit
// test, the docker gate and the smoke test all fail at it equally. Named here
// rather than called inline so the tests can assert what *would* be exec'd
// (the resolved binary, the argv, the payload), which is everything the call
// can get wrong; the branchless syscall beneath stays permanently uncovered,
// and that gap is accepted rather than overlooked.
var execSelf = syscall.Exec

// shellFn attaches the session. A var so the tail-call branch can be tested
// without a Docker daemon: the decision this function makes — exec or return —
// is the one thing between a reload the developer asked for and a session that
// simply ended.
var shellFn = container.Shell

// executablePath resolves this process's own binary. A var for the same
// reason as execSelf: os.Executable's failure branch is otherwise unreachable.
var executablePath = os.Executable

// reEntryHint is what a reload prints when it fails after the old container is
// already gone. The shell that would normally tell the developer how to get
// back is no longer there to print it.
const reEntryHint = "re-enter the session with: toolbox shell"

// takeReloadHandover consumes TOOLBOX_RELOAD_FROM before anything builds a
// container env, so the host-to-host variable never reaches a container.
//
// A payload that cannot be read is a hard error, not a degrade: every field is
// optional with a safe zero value except the container name, and losing that
// leaves the old container alive under the name the next `toolbox shell`
// resolves to — which reuses it, and lands the developer silently back on the
// old image.
func takeReloadHandover() (*reload.From, error) {
	from, err := reload.Take()
	if err != nil {
		return nil, fmt.Errorf("%w — %s", err, reEntryHint)
	}
	return from, nil
}

// runSession attaches the planned session and, when the shell asked for a
// reload on its way out, tail-calls the on-disk CLI in place of this process.
// Both interactive entry points (`shell`, `worktree`) route through here so
// neither owns the exec.
//
// The re-exec is what makes a `brew upgrade` landed meanwhile take effect: the
// new binary owns the verify, the teardown and the create, so its fixes reach
// the next container. Toolbox never runs the upgrade itself.
func runSession(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan) error {
	rl, err := shellFn(ctx, cli, plan)
	if err != nil || rl == nil {
		return err
	}
	return execReload(rl)
}

// execReload replaces this process with a fresh `toolbox`, carrying the
// handover in the environment. It returns only on failure — a successful exec
// never comes back — and a failure here has destroyed nothing, because the
// teardown belongs to the process that would have replaced this one.
func execReload(from *reload.From) error {
	bin, err := executablePath()
	if err != nil {
		return fmt.Errorf("reload: cannot resolve the toolbox binary: %w — %s", err, reEntryHint)
	}
	payload, err := reload.Encode(*from)
	if err != nil {
		return fmt.Errorf("reload: %w — %s", err, reEntryHint)
	}
	// The invocation is replayed as typed. Normalising it — a `worktree create`
	// that must come back as `worktree open <branch>`, an agent that should
	// resume rather than restart — is the session-continuity half of the map
	// and rides in the payload once it lands.
	return execSelf(bin, reloadArgv(bin), append(os.Environ(), reload.FromEnv+"="+payload))
}

// reloadArgv rebuilds argv for the tail call. argv[0] is the resolved binary
// rather than whatever name this process was invoked under, so the re-exec
// cannot follow a symlink or a shell alias somewhere else.
func reloadArgv(bin string) []string {
	argv := make([]string, 0, len(os.Args))
	argv = append(argv, bin)
	if len(os.Args) > 1 {
		argv = append(argv, os.Args[1:]...)
	}
	return argv
}
