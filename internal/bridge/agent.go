package bridge

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"text/template"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// ErrUnsupported is returned by Agent constructors on hosts other than darwin
// and linux. The installer surfaces this to the user instead of attempting a
// platform-specific path that cannot work.
var ErrUnsupported = errors.New("bridge: unsupported host OS")

// ErrRootService is returned when a per-user daemon operation runs as root.
var ErrRootService = errors.New("bridge: refusing to manage the per-user daemon as root")

// EnsureUserContext rejects `sudo toolbox bridge install|uninstall` before any
// state is written. Both supervisors are per-user and root has neither domain,
// so the command could only fail — deep inside launchctl ("Bootstrap failed:
// 125: Domain does not support specified action") or systemctl ("Failed to
// connect to bus"), and only *after* the plist/unit and the token had been
// written into whatever HOME sudo handed us, root-owned, where every later
// non-sudo run trips over them.
func EnsureUserContext() error {
	return checkNotRoot(os.Geteuid(), runtime.GOOS, os.Getenv("SUDO_USER"))
}

// checkNotRoot is the pure decision core of EnsureUserContext; euid, GOOS and
// SUDO_USER are injected so it is testable whichever uid the suite runs under.
func checkNotRoot(euid int, goos, sudoUser string) error {
	if euid != 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrRootService, rootServiceAdvice(goos, sudoUser))
}

// rootServiceAdvice explains why root cannot own the daemon, per host, and
// what to do instead. A set SUDO_USER names the account to go back to; an
// empty one means a genuine root login, where there is no such name to give.
func rootServiceAdvice(goos, sudoUser string) string {
	var why string
	switch goos {
	case "darwin":
		why = "the daemon is a per-user LaunchAgent and root has no GUI domain for launchctl to bootstrap into"
	case "linux":
		why = "the daemon is a per-user systemd unit and root has no user bus for systemctl --user to reach"
	default:
		why = "the daemon is a per-user service and root owns no per-user supervisor domain"
	}
	if sudoUser != "" {
		return why + "; re-run without sudo, as " + sudoUser
	}
	return why + "; run it as the desktop user who will use toolbox, not as root"
}

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
