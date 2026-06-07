//go:build darwin

package browserbridge

import (
	"context"
	"fmt"
	"os/exec"
)

// launchEditor opens path in the requested editor (CLI from PATH, else the
// `open -a` fallback via editorApps); the editor name has already passed
// the daemon allowlist.
func launchEditor(ctx context.Context, editor, path string) error {
	if _, err := exec.LookPath(editor); err == nil {
		return runQuiet(ctx, editor, path)
	}
	app, ok := editorApps[editor]
	if !ok {
		return fmt.Errorf("browser-bridge: no app mapping for editor %q", editor)
	}
	return runQuiet(ctx, "/usr/bin/open", "-a", app, path)
}
