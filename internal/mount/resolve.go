package mount

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/filippolmt/toolbox/internal/config"
)

// ResolveMounts expands ~ in source paths, creates or symlinks missing
// toolbox-managed sources, and returns Docker bind specs.
// Missing paths without a create/symlink rule produce a warning and are
// skipped (D-09). T-02-01: filepath.Clean() is applied to every path.
func ResolveMounts(mounts []config.Mount) (resolved []string, warnings []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		warnings = append(warnings, "unable to resolve home directory: "+err.Error())
		return nil, warnings
	}

	for _, m := range mounts {
		// An empty source would resolve to CWD via filepath.Abs("") and
		// silently bind the project dir. Defend at the resolver too — the
		// validation in MergeMounts only covers config-file paths, not
		// programmatic Mount{} construction by callers.
		if m.Source == "" {
			warnings = append(warnings, "mount with empty source skipped (target "+m.Target+")")
			continue
		}

		src := expandHome(m.Source, home)
		// Relative sources (./test, ../foo, plain "data") are resolved
		// against the CWD at toolbox-shell invocation time — typically the
		// project root. Docker bind mounts require absolute paths.
		if !filepath.IsAbs(src) {
			if abs, err := filepath.Abs(src); err == nil {
				src = abs
			}
		}
		src = filepath.Clean(src)

		// Migration: if a previous run auto-created an empty dir where we now
		// want a symlink, drop it. rmdir is a no-op on non-empty dirs, so
		// existing content is preserved.
		if m.SymlinkFrom != "" {
			if info, err := os.Lstat(src); err == nil && info.IsDir() {
				_ = os.Remove(src)
			}
		}

		if _, err := os.Lstat(src); os.IsNotExist(err) {
			w, created := ensureSource(m, src, home)
			if w != "" {
				warnings = append(warnings, w)
			}
			if !created {
				continue
			}
		} else if err != nil {
			warnings = append(warnings, "failed to stat mount source "+m.Source+": "+err.Error())
			continue
		}

		// Resolve symlinks so the Docker daemon receives the real path.
		if real, err := filepath.EvalSymlinks(src); err == nil {
			src = real
		}

		mode := "rw"
		if m.ReadOnly {
			mode = "ro"
		}
		resolved = append(resolved, src+":"+m.Target+":"+mode)
	}

	return resolved, warnings
}

// ensureSource creates the mount source according to the Mount spec.
// Returns a warning message (empty when none) and whether the source is now
// ready to be mounted.
func ensureSource(m config.Mount, src, home string) (warning string, ready bool) {
	switch {
	case m.SymlinkFrom != "":
		target := filepath.Clean(expandHome(m.SymlinkFrom, home))
		if _, err := os.Stat(target); err != nil {
			return "symlink target missing, mount skipped: " + m.SymlinkFrom, false
		}
		if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
			return "failed to create parent dir for " + m.Source + ": " + err.Error(), false
		}
		if err := os.Symlink(target, src); err != nil {
			return "failed to symlink " + m.Source + " -> " + m.SymlinkFrom + ": " + err.Error(), false
		}
		return "", true

	case m.CreateIfMissing:
		if err := os.MkdirAll(src, 0o700); err != nil {
			return "failed to create mount source " + m.Source + ": " + err.Error(), false
		}
		return "", true

	default:
		return "path not found, mount skipped: " + m.Source, false
	}
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
