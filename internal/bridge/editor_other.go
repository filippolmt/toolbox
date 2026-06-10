//go:build !darwin && !linux

package bridge

import (
	"context"
	"fmt"
	"runtime"
)

func launchEditor(_ context.Context, _, _ string) error {
	return fmt.Errorf("bridge: unsupported host OS %q", runtime.GOOS)
}
