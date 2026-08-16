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
		b, ok, warns := resolveOne(m, home)
		warnings = append(warnings, warns...)
		if ok {
			binds = append(binds, b)
		}
	}
	return binds, warnings
}

// resolveOne resolves a single mount to its Bind. ok is false when the mount
// must be skipped; the warnings to surface are returned either way, since a
// mount can bind successfully and still warn (e.g. unresolvable symlinks).
func resolveOne(m config.Mount, home string) (b Bind, ok bool, warnings []string) {
	// An empty source would resolve to CWD via filepath.Abs("") and
	// silently bind the project dir. Defend at the resolver too — the
	// validation in mergeMounts only covers config-file paths, not
	// programmatic Mount{} construction by callers.
	if m.Source == "" {
		return Bind{}, false, []string{"mount with empty source skipped (target " + m.Target + ")"}
	}

	src := resolveSource(m.Source, home)
	clearStaleSymlinkDir(m, src)

	switch _, err := os.Lstat(src); {
	case os.IsNotExist(err):
		ready, ensureErr := ensureSource(m, src, home)
		if ensureErr != nil {
			warnings = append(warnings, ensureErr.Error())
		}
		if !ready {
			return Bind{}, false, warnings
		}
	case err != nil:
		return Bind{}, false, []string{fmt.Sprintf("failed to stat mount source %s: %s", m.Source, err.Error())}
	}

	// Resolve symlinks so the Docker daemon receives the real path.
	// On failure keep the unresolved cleaned path and warn — resolution
	// runs as the invoking user and can fail (e.g. EACCES on an
	// intermediate dir) where the daemon (root) still mounts fine, so
	// skipping here would break today-working mounts.
	if real, err := filepath.EvalSymlinks(src); err == nil {
		src = real
	} else {
		warnings = append(warnings, fmt.Sprintf("failed to resolve symlinks for mount source %s: %s", m.Source, err.Error()))
	}

	mode := "rw"
	if m.ReadOnly {
		mode = "ro"
	}
	return Bind{Source: src, Target: m.Target, Mode: mode}, true, warnings
}

// resolveSource turns a declared source into the absolute, cleaned host path.
// Relative sources (./test, ../foo, plain "data") are resolved against the CWD
// at toolbox-shell invocation time — typically the project root. Docker bind
// mounts require absolute paths.
func resolveSource(source, home string) string {
	src := fsx.ExpandTilde(source, home)
	if !filepath.IsAbs(src) {
		if abs, err := filepath.Abs(src); err == nil {
			src = abs
		}
	}
	return filepath.Clean(src)
}

// clearStaleSymlinkDir is a migration: if a previous run auto-created an empty
// dir where a symlink now belongs, drop it. rmdir is a no-op on non-empty
// dirs, so existing content is preserved.
func clearStaleSymlinkDir(m config.Mount, src string) {
	if m.SymlinkFrom == "" {
		return
	}
	if info, err := os.Lstat(src); err == nil && info.IsDir() {
		_ = os.Remove(src)
	}
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
