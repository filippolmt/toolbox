package bridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// credentialSubcommand maps a git credential helper operation (the argument
// git passes to the helper) to the `git credential` subcommand that services
// it on the host. `get` fills from the host's configured helpers (osxkeychain
// on macOS, libsecret/gnome-keyring on Linux); `store`/`erase` persist and
// forget. Any op outside this map never reaches exec — the daemon rejects it,
// mirroring ProximoAllowlist.
var credentialSubcommand = map[string]string{
	"get":   "fill",
	"store": "approve",
	"erase": "reject",
}

// runHostCredential forwards one git credential operation to the host git,
// which delegates to whatever credential.helper the host is configured with.
// input is the raw credential request git wrote on the shim's stdin (the
// `key=value` lines terminated by a blank line); the returned output is what
// git wants written back to it (for `get`: the username/password lines).
//
// A non-zero git exit is NOT an infrastructure error — `git credential fill`
// exits non-zero when no helper can satisfy the request, and the shim maps
// that to "no credential" so git falls back to prompting. err is reserved for
// a missing op, git not being found, or a context deadline. GIT_TERMINAL_PROMPT
// is forced off: the daemon has no TTY and must never block waiting on one.
func runHostCredential(ctx context.Context, op string, input []byte) (output []byte, exit int, err error) {
	sub, ok := credentialSubcommand[op]
	if !ok {
		return nil, 0, fmt.Errorf("bridge: unsupported credential op %q", op)
	}
	cmd := exec.CommandContext(ctx, "git", "credential", sub)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// git credential's diagnostics go to stderr; keep them off the response so
	// only the protocol lines reach git. Discard by leaving cmd.Stderr nil.
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stdout.Bytes(), 0, fmt.Errorf("run git credential %s: %w", sub, ctxErr)
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return stdout.Bytes(), exitErr.ExitCode(), nil
		}
		return stdout.Bytes(), 0, fmt.Errorf("run git credential %s: %w", sub, err)
	}
	return stdout.Bytes(), 0, nil
}
