package bridge

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"text/template"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// ErrUnsupported is returned by Agent constructors on hosts other than darwin
// and linux. The installer surfaces this to the user instead of attempting a
// platform-specific path that cannot work.
var ErrUnsupported = errors.New("bridge: unsupported host OS")

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

// renderTemplate executes tpl with data into a string. Shared by the linux
// (systemd unit) and darwin (launchd plist) supervisors, whose render paths
// were otherwise identical parse-then-execute boilerplate.
func renderTemplate(name, tpl string, data map[string]string) (string, error) {
	t, err := template.New(name).Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// writeServiceFile writes a rendered unit/plist to path, creating its parent
// directory. Shared mkdir-then-write skeleton for both platform Install paths.
func writeServiceFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, body, 0o644)
}
