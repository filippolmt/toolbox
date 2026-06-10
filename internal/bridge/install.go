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
// dir (both the current and the pre-rename location).
func Uninstall(a Agent) error {
	s, err := ResolveHostState()
	if err != nil {
		return err
	}
	if err := a.Uninstall(); err != nil {
		return err
	}
	home, err := fsx.Home()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(home, LegacyHostDir)); err != nil {
		return err
	}
	return os.RemoveAll(s.Dir)
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
