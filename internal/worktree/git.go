// Package worktree owns the git + filesystem side of the per-branch worktree
// subsystem behind `toolbox worktree` (create / open / list / rm / prune /
// sync). Almost every git invocation the orchestration makes crosses a single
// seam, Git, so the create-prepare, rm, prune, sync and list flows are
// exercisable in tests with a fake git and no real repository — the one
// exception is deleteRemoteBranches, which shells out directly for a bounded
// context and a scrubbed env the read/write seam does not carry. The
// interactive session launch for create/open (image resolution + sessionplan +
// the TTY attach in container.Shell) deliberately stays at the cmd Docker edge —
// see the Worktree entry in CONTEXT.md.
package worktree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Git is the seam over the git binary used by the worktree orchestration:
// Output for read commands (returns trimmed stdout, wrapping failures with the
// captured stderr), Run for mutating commands whose progress output the user
// should see (stdout/stderr wired through). Faked in tests so the orchestration
// runs without a git binary or a real repo.
type Git interface {
	Output(args ...string) (string, error)
	Run(args ...string) error
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
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitError(args []string, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
	}
	return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
}
