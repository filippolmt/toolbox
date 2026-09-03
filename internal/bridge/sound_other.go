//go:build !darwin && !linux

package bridge

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func hostSoundCommand(_ context.Context, _ string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("bridge: unsupported host OS %q", runtime.GOOS)
}
