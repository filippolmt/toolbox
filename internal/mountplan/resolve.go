package mountplan

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
)

// resolveAll expands ~ in source paths, creates or symlinks missing
// toolbox-managed sources, and returns Docker bind specs as typed Binds.
// Missing paths without a create/symlink rule produce a warning and are
// skipped (D-09). T-02-01: filepath.Clean() is applied to every path.
//
// home is the resolved user home directory; Plan handles the lookup so a
// failing UserHomeDir() can hard-fail before this point instead of silently
// dropping every ~/.toolbox/* default.
func resolveAll(mounts []config.Mount, home string) (binds []Bind, warnings []string) {
	for _, m := range mounts {
		// An empty source would resolve to CWD via filepath.Abs("") and
		// silently bind the project dir. Defend at the resolver too — the
		// validation in mergeMounts only covers config-file paths, not
		// programmatic Mount{} construction by callers.
		if m.Source == "" {
			warnings = append(warnings, "mount with empty source skipped (target "+m.Target+")")
			continue
		}

		src := fsx.ExpandTilde(m.Source, home)
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
			ready, ensureErr := ensureSource(m, src, home)
			if ensureErr != nil {
				warnings = append(warnings, ensureErr.Error())
			}
			if !ready {
				continue
			}
		} else if err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to stat mount source %s: %s", m.Source, err.Error()))
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
		binds = append(binds, Bind{Source: src, Target: m.Target, Mode: mode})
	}

	return binds, warnings
}

// ensureSource creates the mount source according to the Mount spec.
// Returns whether the source is now ready and an error to surface as a
// warning when not. OS-level failures are wrapped with %w so callers can
// errors.Is/errors.As against fs errors (e.g. fs.ErrPermission) instead of
// matching message substrings.
func ensureSource(m config.Mount, src, home string) (ready bool, err error) {
	switch {
	case m.SymlinkFrom != "":
		target := filepath.Clean(fsx.ExpandTilde(m.SymlinkFrom, home))
		if _, statErr := os.Stat(target); statErr != nil {
			return false, fmt.Errorf("symlink target missing, mount skipped: %s: %w", m.SymlinkFrom, statErr)
		}
		if mkErr := os.MkdirAll(filepath.Dir(src), 0o700); mkErr != nil {
			return false, fmt.Errorf("failed to create parent dir for %s: %w", m.Source, mkErr)
		}
		if linkErr := os.Symlink(target, src); linkErr != nil {
			return false, fmt.Errorf("failed to symlink %s -> %s: %w", m.Source, m.SymlinkFrom, linkErr)
		}
		return true, nil

	case m.CreateIfMissing:
		if mkErr := os.MkdirAll(src, 0o700); mkErr != nil {
			return false, fmt.Errorf("failed to create mount source %s: %w", m.Source, mkErr)
		}
		return true, nil

	default:
		return false, fmt.Errorf("path not found, mount skipped: %s", m.Source)
	}
}
