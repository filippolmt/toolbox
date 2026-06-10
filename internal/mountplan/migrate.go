package mountplan

import (
	"fmt"
	"os"
	"path/filepath"
)

// MigrateLegacyToolboxState renames the pre-namespace toolbox-own state dir
// (~/.toolbox/state) onto ~/.toolbox/toolbox/state, preserving the pull
// cache. ~/.toolbox root is reserved for per-app config/credential dirs;
// everything toolbox-own lives under ~/.toolbox/toolbox so a future app
// named "state" can never collide. No-op when there is nothing to migrate;
// when both dirs exist the new one wins and the stale legacy dir is left
// alone. Callers treat failures as a warning: CreateIfMissing rebuilds an
// empty state dir and the pull cache regenerates on the next pull.
func MigrateLegacyToolboxState(home string) error {
	legacy := filepath.Join(home, ".toolbox", "state")
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}
	dest := filepath.Join(home, ".toolbox", "toolbox", "state")
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Rename(legacy, dest); err != nil {
		return fmt.Errorf("migrate %s to %s: %w", legacy, dest, err)
	}
	return nil
}
