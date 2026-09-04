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
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

// MarkerFresh reports whether the empty marker file at path was last written
// within ttl. Any error — missing marker, unreadable directory, no home —
// reports false, so a caller that gates work on freshness does the work
// rather than skipping it on uncertainty.
//
// Marker contents are never read: the modification time *is* the record. Two
// packages keep TTL-gated markers on the state mount with different meanings
// (the pull cache stamps only successful pulls, imageprefetch stamps every
// probe
// attempt); the mechanism is shared here, the semantics stay theirs.
func MarkerFresh(path string, ttl time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < ttl
}

// MarkerOlderThan reports whether the marker at path exists and predates ttl.
// Deliberately not the negation of MarkerFresh: an absent marker is neither
// fresh nor old, and the two callers ask opposite questions of it — "may I
// skip?" versus "has this condition persisted?".
func MarkerOlderThan(path string, ttl time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) >= ttl
}

// TouchMarker stamps an empty marker at path, creating its directory. Errors
// are wrapped with the step that failed, because the two causes call for
// different fixes: a missing directory usually means a mount is not there,
// an un-writable file means ENOSPC, EROFS or permissions.
func TouchMarker(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create marker dir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return fmt.Errorf("write marker %s: %w", path, err)
	}
	return nil
}

// Host is the ambient host state a toolbox run reads, turned into a value a
// caller declares and passes. Both fields are process-wide inputs no
// signature used to mention: the home directory every ~/.toolbox path hangs
// off, and the PATH lookup that finds host binaries. Resolved once at the
// cmd edge by CurrentHost and threaded through the planning seams, so a
// package downstream reads the field instead of the process — which is what
// lets a test construct a Host rather than mutate $HOME for the whole
// binary.
//
// Neither field falls back to the process. Home is a plain string precisely
// so a zero-valued Host is a visible bug (an empty base path) rather than a
// silent read of the real home, and a nil LookPath means this host resolves
// no binaries at all — not "ask the process PATH". A convenience fallback
// there would reinstate the ambient read the type exists to remove, in the
// one place nothing would notice: a caller that declared only a home would
// keep probing the real machine, and a test written against it would pass
// for whatever happens to be installed.
type Host struct {
	Home     string
	LookPath func(name string) (string, error)
}

// CurrentHost resolves the real host: the strict Home above plus the process
// PATH. The single place the ambient read still happens — call it at the cmd
// edge and pass the result down.
func CurrentHost() (Host, error) {
	home, err := Home()
	if err != nil {
		return Host{}, err
	}
	return Host{Home: home, LookPath: exec.LookPath}, nil
}

// Validate rejects a Host with no home. The strictness fsx.Home enforces on
// the ambient read has to be enforced again where the resolved value enters
// a package, or a zero-valued Host would join every ~/.toolbox path onto ""
// and stat, create or bind the wrong tree.
func (h Host) Validate() error {
	if h.Home == "" {
		return fmt.Errorf("resolve home directory: host home not set")
	}
	return nil
}

// Expand is ExpandTilde against this host's home.
func (h Host) Expand(p string) string { return ExpandTilde(p, h.Home) }

// Join builds a path under this host's home. Callers that would have written
// filepath.Join(home, …) after resolving home themselves write h.Join(…).
func (h Host) Join(elem ...string) string {
	return filepath.Join(append([]string{h.Home}, elem...)...)
}

// Look resolves a binary on this host's PATH. A Host that declares no
// resolver has an empty PATH: every lookup fails with exec.ErrNotFound, which
// is what a caller probing for an optional tool already handles.
func (h Host) Look(name string) (string, error) {
	if h.LookPath == nil {
		return "", exec.ErrNotFound
	}
	return h.LookPath(name)
}
