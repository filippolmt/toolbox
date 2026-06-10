// Package fsx holds the small set of host-filesystem primitives that more
// than one toolbox package needs: strict home-directory resolution, tilde
// expansion, and crash-safe atomic file writes. It imports only the stdlib
// so every other internal package can depend on it without cycle risk.
//
// Semantics are deliberately strict — callers that want best-effort
// behaviour (tolerating an empty or unresolvable home) keep using
// os.UserHomeDir directly rather than routing through Home.
package fsx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Home resolves the current user's home directory, failing loudly when it
// cannot be determined. The empty-$HOME case is treated as an error (rather
// than returning "") so callers never silently filepath.Join onto an empty
// base and bind/stat a wrong path. Used by every hard-failing home-resolution
// site (config write path, mount plan, bridge state + agents, image
// pull cache).
func Home() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("resolve home directory: empty $HOME")
	}
	return home, nil
}

// ExpandTilde replaces a leading ~ or ~/ in p with home. A bare "~" maps to
// home; "~/sub" maps to home/sub. Any other path (absolute or relative) is
// returned unchanged.
func ExpandTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// AtomicWriteFile writes data to dest by creating a temp file in the same
// directory, then renaming it over dest. POSIX guarantees rename(2) is
// atomic within a single filesystem, so a concurrent reader or a crash
// mid-write observes either the prior content or the new content — never a
// truncated/partially-written file. fsync is intentionally omitted: the
// files written through here (user config, regenerable tokens) are
// rewritable, so durability after a power failure is not worth the extra IO
// syscall on every write.
func AtomicWriteFile(dest string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(dest)
	base := filepath.Base(dest)
	tmp, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", dest, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp for %s: %w", dest, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp for %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp for %s: %w", dest, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		cleanup()
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, dest, err)
	}
	return nil
}
