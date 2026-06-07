//go:build !darwin && !linux

package browserbridge

import (
	"context"
	"fmt"
	"runtime"
)

func launchEditor(_ context.Context, _, _ string) error {
	return fmt.Errorf("browser-bridge: unsupported host OS %q", runtime.GOOS)
}
