// Package bridge implements the host-side daemon and container-side
// wrappers that let CLIs inside a toolbox shell open URLs in the host's
// real browser, open files in the host editor, and drive the host proximo
// stack. Security boundary: 127.0.0.1 bind, bearer token, allowlists, rate
// limit.
package bridge

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// HostDir is the directory under the user's home that holds all bridge
// state on the host. Lives under ~/.toolbox/toolbox (the toolbox-own
// namespace, next to state/) rather than ~/.toolbox root, which is reserved
// for per-app config/credential dirs. Deliberately a SIBLING of
// ~/.toolbox/toolbox/state, not inside it: state/ is rw-mounted wholesale
// into the container, and the bridge token must stay read-only there.
// Mounted RO into the container at ContainerDir.
const HostDir = ".toolbox/toolbox/bridge"

// ContainerDir is the in-container path that mirrors HostDir read-only.
// Must match the mount Target declared in mountplan.defaults.
const ContainerDir = "/home/toolbox/.toolbox/bridge"

// LegacyHostDir is the pre-rename (`browser-bridge` era) state location.
// Install migrates it to HostDir; Uninstall removes both.
const LegacyHostDir = ".toolbox/browser"

// LegacyContainerDir is the pre-rename in-container mirror. mountplan keeps
// binding the host state here too (same source as ContainerDir) so an image
// older than the rename — its shims hardcode this path — works against a
// new host CLI. bridge-lib.sh in a NEW image falls back to this path for
// the inverse skew (old host CLI still writing the legacy host dir).
const LegacyContainerDir = "/home/toolbox/.toolbox/browser"

// File names inside HostDir / ContainerDir. Kept in one place so the daemon
// (host) and the wrapper script (container) cannot drift apart.
const (
	tokenFile = "token"
	portFile  = "port"
	logFile   = "log"
	pidFile   = "pid"
)

// HostState is the resolved set of absolute paths under HostDir for the
// current user. Daemon and CLI subcommands consume this; the wrapper script
// reads the container-side mirror directly.
type HostState struct {
	Dir   string // ~/<HostDir>
	Token string // <Dir>/token
	Port  string // <Dir>/port
	Log   string // <Dir>/log
	PID   string // <Dir>/pid
}

// ResolveHostState returns the absolute host paths for bridge state.
// It does NOT create any files — callers that need the dir to exist must
// call EnsureHostDir.
func ResolveHostState() (HostState, error) {
	home, err := fsx.Home()
	if err != nil {
		return HostState{}, err
	}
	dir := filepath.Join(home, HostDir)
	return HostState{
		Dir:   dir,
		Token: filepath.Join(dir, tokenFile),
		Port:  filepath.Join(dir, portFile),
		Log:   filepath.Join(dir, logFile),
		PID:   filepath.Join(dir, pidFile),
	}, nil
}

// EnsureHostDir creates the host state directory with mode 0700 if it does
// not already exist. Idempotent.
func EnsureHostDir(s HostState) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", s.Dir, err)
	}
	return nil
}
