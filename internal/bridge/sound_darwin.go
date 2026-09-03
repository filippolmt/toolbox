//go:build darwin

package bridge

import (
	"context"
	"os/exec"
)

// hostSoundCommand builds the command that plays path on a macOS host. afplay ships
// with the OS and needs no flags, so there is nothing to probe: the chain in
// sound.go is the Linux host's business.
func hostSoundCommand(ctx context.Context, path string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "/usr/bin/afplay", path), nil
}
