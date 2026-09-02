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
		// No payload means no re-entry form either, so this one line is the
		// only place the fallback is spelled rather than carried.
		return nil, fmt.Errorf("%w — re-enter the session with: toolbox shell", err)
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
func runSession(ctx context.Context, cli client.APIClient, plan *sessionplan.SessionPlan, reentry []string) error {
	rl, err := shellFn(ctx, cli, plan)
	if err != nil {
		return withReEntry(err, plan.ReloadFrom)
	}
	if rl == nil {
		return nil
	}
	rl.Reentry = reentry
	return execReload(rl)
}

// withReEntry appends the exact command that gets the developer back, but only
// on a session this process reached by reloading. The shell that would normally
// print it has already exited, and by the time anything downstream of the
// teardown fails — a port conflict, a `:local` overlay that will not build —
// the old container is gone too. A failure *before* the teardown still needs
// the line: the old container survives, but nothing is attached to it.
func withReEntry(err error, from *reload.From) error {
	if err == nil || from == nil {
		return err
	}
	return fmt.Errorf("%w\n\nre-enter the session with: %s", err, from.ReentryCommand())
}

// execReload replaces this process with a fresh `toolbox`, carrying the
// handover in the environment. It returns only on failure — a successful exec
// never comes back — and a failure here has destroyed nothing, because the
// teardown belongs to the process that would have replaced this one.
func execReload(from *reload.From) error {
	bin, err := executablePath()
	if err != nil {
		return withReEntry(fmt.Errorf("reload: cannot resolve the toolbox binary: %w", err), from)
	}
	payload, err := reload.Encode(*from)
	if err != nil {
		return withReEntry(fmt.Errorf("reload: %w", err), from)
	}
	return execSelf(bin, reloadArgv(bin, from), append(os.Environ(), reload.FromEnv+"="+payload))
}

// reloadArgv rebuilds argv for the tail call from the **normalised** re-entry
// form, never from os.Args: replaying the invocation as typed would re-run a
// `worktree create` against a branch that now exists and re-send a prompt the
// agent has already completed. argv[0] is the resolved binary rather than the
// name this process was invoked under, so the re-exec cannot follow a symlink
// or a shell alias somewhere else.
func reloadArgv(bin string, from *reload.From) []string {
	argv := []string{bin}
	if len(from.Reentry) == 0 {
		// A session that carried no form is a plain shell: the bare command is
		// what re-enters it, and it is also what the fallback advice names.
		return append(argv, "shell")
	}
	return append(argv, from.Reentry...)
}
