//go:build darwin

package bridge

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/filippolmt/toolbox/internal/fsx"
)

const launchLabel = "com.filippolmt.toolbox.bridge"

// legacyLaunchLabel is the pre-rename LaunchAgent label. Install/Uninstall
// boot it out and remove its plist so one daemon never runs twice.
const legacyLaunchLabel = "com.filippolmt.toolbox.browser"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Exec}}</string>
    <string>bridge</string>
    <string>daemon</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>{{.LogPath}}</string>
  <key>StandardErrorPath</key>
  <string>{{.LogPath}}</string>
</dict>
</plist>
`

type darwinAgent struct {
	plistPath       string
	legacyPlistPath string
	logPath         string
	legacyLogPath   string
}

func NewAgent() (Agent, error) {
	home, err := fsx.Home()
	if err != nil {
		return nil, err
	}
	agents := filepath.Join(home, "Library", "LaunchAgents")
	logs := filepath.Join(home, "Library", "Logs")
	return &darwinAgent{
		plistPath:       filepath.Join(agents, launchLabel+".plist"),
		legacyPlistPath: filepath.Join(agents, legacyLaunchLabel+".plist"),
		logPath:         filepath.Join(logs, "toolbox-bridge.log"),
		legacyLogPath:   filepath.Join(logs, "toolbox-browser.log"),
	}, nil
}

// removeLegacy boots out the pre-rename LaunchAgent and deletes its plist
// and log file. The bootout is best-effort (already gone is fine); the file
// removals only fail on real fs errors.
func (a *darwinAgent) removeLegacy() error {
	uid := strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid, a.legacyPlistPath).Run()
	if err := os.Remove(a.legacyPlistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(a.legacyLogPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func renderPlist(execPath, logPath string) (string, error) {
	return renderTemplate("plist", plistTemplate, map[string]string{
		"Label":   launchLabel,
		"Exec":    execPath,
		"LogPath": logPath,
	})
}

func (a *darwinAgent) Install(execPath string) error {
	body, err := renderPlist(execPath, a.logPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.logPath), 0o755); err != nil {
		return err
	}
	if err := writeServiceFile(a.plistPath, []byte(body)); err != nil {
		return err
	}
	if err := a.removeLegacy(); err != nil {
		return err
	}
	uid := strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid, a.plistPath).Run()
	out, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, a.plistPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (a *darwinAgent) Uninstall() error {
	uid := strconv.Itoa(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid, a.plistPath).Run()
	if err := os.Remove(a.plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return a.removeLegacy()
}

func (a *darwinAgent) IsInstalled() bool {
	_, err := os.Stat(a.plistPath)
	return err == nil
}

func (a *darwinAgent) Status() (AgentStatus, error) {
	st := AgentStatus{Installed: a.IsInstalled()}
	uid := strconv.Itoa(os.Getuid())
	out, _ := exec.Command("launchctl", "print", "gui/"+uid+"/"+launchLabel).CombinedOutput()
	text := string(out)
	switch {
	case strings.Contains(text, "state = running"):
		st.Running = true
		st.Detail = "loaded, running"
	case st.Installed:
		st.Detail = "loaded, not running"
	default:
		st.Detail = "not installed"
	}
	return st, nil
}
