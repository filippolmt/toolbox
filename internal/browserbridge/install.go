package browserbridge

import (
	"errors"
	"io/fs"
	"os"
)

// StatusReport is the aggregated install state shown by `toolbox
// browser-bridge status`.
type StatusReport struct {
	StateDir       string
	TokenPresent   bool
	Port           int
	AgentInstalled bool
	AgentRunning   bool
	AgentDetail    string
}

// Install bootstraps the daemon: ensures host state dir, generates token,
// writes the platform service file, and starts it.
func Install(a Agent, execPath string) error {
	s, err := ResolveHostState()
	if err != nil {
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

// Uninstall stops the daemon, removes the service file, and wipes ~/.toolbox/browser.
func Uninstall(a Agent) error {
	s, err := ResolveHostState()
	if err != nil {
		return err
	}
	if err := a.Uninstall(); err != nil {
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
	as, err := a.Status()
	if err != nil {
		return rep, err
	}
	rep.AgentInstalled = as.Installed
	rep.AgentRunning = as.Running
	rep.AgentDetail = as.Detail
	return rep, nil
}
