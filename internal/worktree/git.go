// Package worktree owns the git + filesystem side of the per-branch worktree
// subsystem behind `toolbox worktree` (create / open / list / rm / prune /
// sync). Every git invocation the orchestration makes crosses a single seam,
// Git, so the create-prepare, open, rm, prune, sync and list flows are
// exercisable in tests with a fake git and no real repository. The
// interactive session launch for create/open (image resolution + sessionplan +
// the TTY attach in container.Shell) deliberately stays at the cmd Docker edge —
// see the Worktree entry in CONTEXT.md.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// gitErrFmt wraps a git invocation failure with the full argument list, so the
// error names the exact command that failed.
const gitErrFmt = "git %s: %w"

// Git is the seam over the git binary used by the worktree orchestration:
// Output for read commands (returns trimmed stdout, wrapping failures with the
// captured stderr), Run for mutating commands whose progress output the user
// should see (stdout/stderr wired through), and PushDelete for the one command
// that needs more than an arg list. Faked in tests so the orchestration runs
// without a git binary or a real repo.
type Git interface {
	Output(args ...string) (string, error)
	Run(args ...string) error
	// PushDelete deletes branches on origin in a single push. A named domain
	// method rather than a Run because it needs a cancellable, bounded context
	// and a scrubbed env that the generic read/write pair does not carry —
	// promoting those to Run's signature would make every fake replicate them.
	PushDelete(ctx context.Context, root string, branches []string) error
}

// RealGit is the production Git: it shells out to the git binary.
type RealGit struct{}

// Output runs git and returns its trimmed stdout, wrapping failures with the
// captured stderr for a useful message.
func (RealGit) Output(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", gitError(args, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Run runs git with stdout/stderr wired through, for mutating commands whose
// progress output the user should see.
func (RealGit) Run(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(gitErrFmt, strings.Join(args, " "), err)
	}
	return nil
}

// remoteDeleteTimeout bounds the origin round-trip so a hung or credential-
// prompting remote cannot freeze `rm` or block prune's remaining branches.
const remoteDeleteTimeout = 60 * time.Second

// PushDelete deletes branches on origin in one push — one round-trip whatever
// the count, so prune scales to any number of merged branches.
// GIT_TERMINAL_PROMPT=0 makes a missing credential fail fast rather than block
// on a prompt; remoteDeleteTimeout is the backstop on top of the caller's ctx.
func (RealGit) PushDelete(ctx context.Context, root string, branches []string) error {
	ctx, cancel := context.WithTimeout(ctx, remoteDeleteTimeout)
	defer cancel()
	args := append([]string{"-C", root, "push", "origin", "--delete"}, branches...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(gitErrFmt, strings.Join(args, " "), err)
	}
	return nil
}

func gitError(args []string, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
	}
	return fmt.Errorf(gitErrFmt, strings.Join(args, " "), err)
}
