package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// proximoChildPathDirs returns bin dirs appended to the child proximo
// process's PATH so its own lookups (docker, docker compose) survive the
// minimal LaunchAgent / systemd-user PATH — the same problem
// resolveProximoBinary solves for the proximo binary itself. binDir (dir of
// the resolved binary) leads; home-relative entries are skipped when home is
// empty.
func proximoChildPathDirs(binDir string) []string {
	dirs := []string{
		binDir,
		"/opt/homebrew/bin",
		"/usr/local/bin",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs,
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".docker", "bin"),
			filepath.Join(home, ".orbstack", "bin"),
		)
	}
	return dirs
}

// appendPathDirs returns env with its PATH entry extended by dirs not already
// present; a PATH entry is added when none exists. Existing entries keep
// priority — dirs are fallbacks, mirroring resolveProximoBinary's "PATH
// first, well-known locations second" order.
func appendPathDirs(env []string, dirs []string) []string {
	if len(dirs) == 0 {
		return env
	}
	pathIdx := -1
	var entries []string
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathIdx = i
			entries = filepath.SplitList(strings.TrimPrefix(kv, "PATH="))
			break
		}
	}
	seen := make(map[string]struct{}, len(entries)+len(dirs))
	for _, e := range entries {
		seen[e] = struct{}{}
	}
	for _, d := range dirs {
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		entries = append(entries, d)
	}
	kv := "PATH=" + strings.Join(entries, string(os.PathListSeparator))
	if pathIdx < 0 {
		return append(append([]string{}, env...), kv)
	}
	out := append([]string{}, env...)
	out[pathIdx] = kv
	return out
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
	// Child PATH augmented so proximo's own lookups (docker, compose) survive
	// the minimal service PATH — see proximoChildPathDirs.
	cmd.Env = appendPathDirs(os.Environ(), proximoChildPathDirs(filepath.Dir(bin)))
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
