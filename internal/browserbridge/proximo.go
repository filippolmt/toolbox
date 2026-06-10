package browserbridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// proximoAllowlist is the fixed set of proximo subcommands /proximo may
// execute. A client-supplied command never reaches exec without passing this
// gate. Deliberately excludes install/uninstall (interactive sudo on the
// host) and accepts bare subcommands only — no argument passthrough.
var proximoAllowlist = map[string]struct{}{
	"up":     {},
	"down":   {},
	"status": {},
}

// proximoTimeout bounds a /proximo execution. Far above the shared 5s
// requestTimeout because the first `proximo up` pulls/builds the stack
// images; status/down complete in seconds.
const proximoTimeout = 120 * time.Second

// ErrProximoNotInstalled is returned when no proximo binary resolves on the
// host; the daemon surfaces it verbatim to the in-container shim.
var ErrProximoNotInstalled = errors.New("proximo not installed on host — see https://github.com/filippolmt/proximo")

// proximoFallbackCandidates lists well-known proximo install locations probed
// when PATH lookup fails: the LaunchAgent / systemd user unit running the
// daemon typically has a minimal PATH without /opt/homebrew/bin (brew on
// Apple Silicon) or ~/go/bin (`go install`). An empty home yields no go/bin
// candidate rather than a bogus relative path.
func proximoFallbackCandidates() []string {
	candidates := []string{
		"/opt/homebrew/bin/proximo",
		"/usr/local/bin/proximo",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, "go", "bin", "proximo"))
	}
	return candidates
}

// resolveProximoBinary returns the proximo binary to exec: PATH lookup first,
// then the given fallback candidates in order.
func resolveProximoBinary(candidates []string) (string, error) {
	if p, err := exec.LookPath("proximo"); err == nil {
		return p, nil
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}
	return "", ErrProximoNotInstalled
}

// launchProximo executes one allowlisted proximo subcommand on the host and
// returns its combined output and exit code. A non-zero exit is NOT an error
// (the shim propagates it); err is reserved for infrastructure failures
// (binary missing, context deadline). Direct exec, no shell.
func launchProximo(ctx context.Context, command string) (output []byte, exit int, err error) {
	bin, err := resolveProximoBinary(proximoFallbackCandidates())
	if err != nil {
		return nil, 0, err
	}
	cmd := exec.CommandContext(ctx, bin, command)
	cmd.Stdin = nil
	// Stop waiting on the stdout/stderr pipes shortly after a deadline kill:
	// proximo's docker-compose children inherit the pipes and would otherwise
	// hold CombinedOutput open past proximoTimeout.
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		// A deadline kill also surfaces as *exec.ExitError — classify it as an
		// infrastructure failure, not a command exit.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, 0, fmt.Errorf("run %s %s: %w", bin, command, ctxErr)
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return out, exitErr.ExitCode(), nil
		}
		return out, 0, fmt.Errorf("run %s %s: %w", bin, command, err)
	}
	return out, 0, nil
}
