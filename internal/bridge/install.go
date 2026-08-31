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
// dir is best-effort: on a Docker Desktop host every open toolbox shell
// bind-mounts the Bridge Run Mount inside it and the unlink comes back EACCES,
// while the irreversible half — the daemon and its service file — is already
// gone, so the failure is a warning for the caller to print, not an error. The
// pre-rename dir is never a mount source and keeps failing hard.
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
	rmErr := os.RemoveAll(s.Dir)
	_, statErr := os.Stat(s.Token)
	return stateDirOutcome(s.Dir, statErr == nil, rmErr)
}

// stateDirOutcome decides what a failed state-dir removal means. Leftovers the
// daemon no longer reads are a warning and exit 0; a surviving token is a hard
// error, because uninstall+install is the documented way to rotate it (see
// LoadOrCreateToken) and exiting 0 would hand the next install the old one
// back. The remedy line is fixed text: the errno carries no signal and this
// package knows nothing about mounts.
func stateDirOutcome(dir string, tokenLive bool, err error) (string, error) {
	switch {
	case err == nil:
		return "", nil
	case tokenLive:
		return "", fmt.Errorf("bridge state dir %s not removed and its token is still live: %w", dir, err)
	}
	return fmt.Sprintf("warning: bridge state dir %s not removed: %v\n"+
		"close any open toolbox shells (they bind-mount the state dir) and re-run", dir, err), nil
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
