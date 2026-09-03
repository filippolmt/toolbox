//go:build linux

package bridge

import (
	"context"
	"os/exec"
)

// hostSoundCommand builds the command that plays path on a Linux host: the
// first entry of soundPlayerChain that is installed. Unlike the container, a
// Linux host running a desktop has one of these — and when it has none, the
// error is what the shim propagates.
func hostSoundCommand(ctx context.Context, path string) (*exec.Cmd, error) {
	name, args, err := pickSoundPlayer(exec.LookPath)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, name, append(args, path)...), nil
}
