//go:build linux

package bridge

import (
	"context"
	"fmt"
	"os/exec"
)

// launchEditor opens path in the requested editor from PATH; the editor
// name has already passed the daemon allowlist.
func launchEditor(ctx context.Context, editor, path string) error {
	if _, err := exec.LookPath(editor); err != nil {
		return fmt.Errorf("bridge: editor %q not on PATH: %w", editor, err)
	}
	return runQuiet(ctx, editor, path)
}
