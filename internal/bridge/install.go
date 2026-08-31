package bridge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// StatusReport is the aggregated install state shown by `toolbox
// bridge status`.
type StatusReport struct {
	StateDir       string
	TokenPresent   bool
	Port           int
	SocketPath     string // empty on hosts without the unix transport (macOS)
	SocketPresent  bool
	AgentInstalled bool
	AgentRunning   bool
	AgentDetail    string
}

// Install bootstraps the daemon: migrates pre-rename state, ensures host
// state dir, generates token, writes the platform service file, and starts
// it.
func Install(a Agent, execPath string) error {
	s, err := ResolveHostState()
	if err != nil {
		return err
	}
	if err := migrateLegacyHostDir(s); err != nil {
		return err
	}
	if err := EnsureHostDir(s); err != nil {
		return err
	}
	if _, err := LoadOrCreateToken(s); err != nil {
		return err
	}
	return a.Install(execPath)
}

// migrateLegacyHostDir renames the pre-rename state dir (~/LegacyHostDir)
// onto s.Dir, preserving the existing token so in-flight containers keep
// authenticating. No-op when there is nothing to migrate; when both dirs
// exist the new one wins and the stale legacy dir (recreated by an old
// binary's CreateIfMissing mount or `browser-bridge install`) is removed.
func migrateLegacyHostDir(s HostState) error {
	home, err := fsx.Home()
	if err != nil {
		return err
	}
	legacy := filepath.Join(home, LegacyHostDir)
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}
	if _, err := os.Stat(s.Dir); err == nil {
		return os.RemoveAll(legacy)
	}
	// Parent (~/.toolbox/toolbox) may not exist yet on a host that never ran
	// `toolbox shell` after the rename.
	if err := os.MkdirAll(filepath.Dir(s.Dir), 0o700); err != nil {
		return err
	}
	if err := os.Rename(legacy, s.Dir); err != nil {
		return fmt.Errorf("migrate %s to %s: %w", legacy, s.Dir, err)
	}
	return nil
}

// Uninstall stops the daemon, removes the service file, and wipes the state
// dir (both the current and the pre-rename location). Wiping the current state
// dir is best-effort — see stateDirOutcome and the Bridge Run Mount glossary
// entry; the pre-rename dir is never a mount source and keeps failing hard.
func Uninstall(a Agent) (warning string, err error) {
	s, err := ResolveHostState()
	if err != nil {
		return "", err
	}
	if err := a.Uninstall(); err != nil {
		return "", err
	}
	home, err := fsx.Home()
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(filepath.Join(home, LegacyHostDir)); err != nil {
		return "", err
	}
	return stateDirOutcome(s, os.RemoveAll(s.Dir))
}

// stateDirOutcome decides what a failed state-dir removal means: leftovers the
// daemon no longer reads are a warning the caller prints (exit 0), a surviving
// token is a hard error — uninstall+install is the documented way to rotate it
// (see LoadOrCreateToken), so exiting 0 would hand the next install the old
// token back. It fails closed: the stat can be defeated by the same EACCES
// that just defeated the removal, so only a token that is provably gone counts
// as gone. The remedy line is fixed text — the errno carries no signal
// (virtiofs answers EACCES where a blocked unlink answers EBUSY) and this
// package cannot see which shell holds the mount.
func stateDirOutcome(s HostState, err error) (string, error) {
	if err == nil {
		return "", nil
	}
	if _, statErr := os.Stat(s.Token); !errors.Is(statErr, fs.ErrNotExist) {
		return "", fmt.Errorf("bridge state dir %s not removed and its token is still live: %w", s.Dir, err)
	}
	return fmt.Sprintf("bridge state dir %s not removed: %v\n"+
		"close any open toolbox shells (they bind-mount the state dir) and re-run", s.Dir, err), nil
}

// Status reports the current bridge + agent state.
func Status(a Agent) (StatusReport, error) {
	s, err := ResolveHostState()
	if err != nil {
		return StatusReport{}, err
	}
	rep := StatusReport{StateDir: s.Dir}
	if _, err := os.Stat(s.Token); err == nil {
		rep.TokenPresent = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return rep, err
	}
	if p, err := LoadPort(s); err == nil {
		rep.Port = p
	}
	rep.SocketPath, rep.SocketPresent = socketStatus(s)
	as, err := a.Status()
	if err != nil {
		return rep, err
	}
	rep.AgentInstalled = as.Installed
	rep.AgentRunning = as.Running
	rep.AgentDetail = as.Detail
	return rep, nil
}
