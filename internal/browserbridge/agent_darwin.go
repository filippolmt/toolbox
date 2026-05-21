//go:build darwin

package browserbridge

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

const launchLabel = "com.filippolmt.toolbox.browser"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Exec}}</string>
    <string>browser-bridge</string>
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
	plistPath string
	logPath   string
}

func NewAgent() (Agent, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &darwinAgent{
		plistPath: filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist"),
		logPath:   filepath.Join(home, "Library", "Logs", "toolbox-browser.log"),
	}, nil
}

func renderPlist(execPath, logPath string) (string, error) {
	tpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = tpl.Execute(&buf, map[string]string{
		"Label":   launchLabel,
		"Exec":    execPath,
		"LogPath": logPath,
	})
	return buf.String(), err
}

func (a *darwinAgent) Install(execPath string) error {
	body, err := renderPlist(execPath, a.logPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.plistPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.logPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(a.plistPath, []byte(body), 0o644); err != nil {
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
	return nil
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
