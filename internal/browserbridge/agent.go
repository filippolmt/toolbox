package browserbridge

import "errors"

// ErrUnsupported is returned by Agent constructors on hosts other than darwin
// and linux. The installer surfaces this to the user instead of attempting a
// platform-specific path that cannot work.
var ErrUnsupported = errors.New("browser-bridge: unsupported host OS")

// AgentStatus is the snapshot Agent.Status reports.
type AgentStatus struct {
	Installed bool   // service file exists on disk
	Running   bool   // service is loaded and the daemon process is alive
	Detail    string // free-form one-liner ("loaded, pid 12345" / "not loaded")
}

// Agent abstracts the per-user service supervisor for the host (LaunchAgent on
// darwin, systemd --user on linux). Install writes the unit/plist and starts
// the daemon; Uninstall stops it and removes the file; Status reports full
// state (may exec launchctl/systemctl); IsInstalled is the cheap stat-only
// check used by shell-start hot paths.
type Agent interface {
	Install(execPath string) error
	Uninstall() error
	Status() (AgentStatus, error)
	IsInstalled() bool
}
