package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// resolveHostBinary returns the host binary name to exec: the daemon's PATH
// first, then the candidates in order — the service PATH of the LaunchAgent
// or systemd user unit misses the user's own bin dirs. missing is the error
// when none resolves.
func resolveHostBinary(host fsx.Host, name string, candidates []string, missing error) (string, error) {
	if p, err := host.Look(name); err == nil {
		return p, nil
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", missing
}

// runHostCommand runs cmd and returns its combined output and exit code. A
// non-zero exit is the command's answer, which the shim propagates, not an
// error; err is reserved for infrastructure failures — a binary that would not
// run, a deadline — wrapped as "run <label>: …".
func runHostCommand(ctx context.Context, cmd *exec.Cmd, label string) ([]byte, int, error) {
	// Stop waiting on the stdout/stderr pipes shortly after a deadline kill:
	// children (proximo's docker-compose) inherit the pipes and would otherwise
	// hold CombinedOutput open past the route's budget.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, 0, nil
	}
	// A deadline kill also surfaces as *exec.ExitError — classify it as an
	// infrastructure failure, not a command exit.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return out, 0, fmt.Errorf("run %s: %w", label, ctxErr)
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return out, exitErr.ExitCode(), nil
	}
	return out, 0, fmt.Errorf("run %s: %w", label, err)
}
