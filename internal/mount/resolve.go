package mount

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/filippolmt/toolbox/internal/config"
)

// ResolveMounts expands ~ in source paths and verifies they exist.
// Missing paths produce a warning and are skipped, not errored (D-09).
// T-02-01: filepath.Clean() is applied to every path after expansion.
func ResolveMounts(mounts []config.Mount) (resolved []string, warnings []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		warnings = append(warnings, "unable to resolve home directory: "+err.Error())
		return nil, warnings
	}

	for _, m := range mounts {
		src := expandHome(m.Source, home)
		src = filepath.Clean(src) // T-02-01: path sanitization

		if _, err := os.Stat(src); os.IsNotExist(err) {
			warnings = append(warnings, "path not found, mount skipped: "+m.Source)
			continue
		}

		mode := "rw"
		if m.ReadOnly {
			mode = "ro"
		}
		resolved = append(resolved, src+":"+m.Target+":"+mode)
	}

	return resolved, warnings
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
