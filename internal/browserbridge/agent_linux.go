//go:build linux

package browserbridge

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const unitName = "toolbox-browser.service"

const unitTemplate = `[Unit]
Description=Toolbox Browser Bridge
After=network.target

[Service]
ExecStart={{.Exec}} browser-bridge daemon
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`

type linuxAgent struct {
	unitPath string
}

func NewAgent() (Agent, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &linuxAgent{
		unitPath: filepath.Join(home, ".config", "systemd", "user", unitName),
	}, nil
}

func renderUnit(execPath string) (string, error) {
	tpl, err := template.New("unit").Parse(unitTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = tpl.Execute(&buf, map[string]string{"Exec": execPath})
	return buf.String(), err
}

func (a *linuxAgent) Install(execPath string) error {
	body, err := renderUnit(execPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.unitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(a.unitPath, []byte(body), 0o644); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %v: %s", err, bytes.TrimSpace(out))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", unitName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable --now: %v: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (a *linuxAgent) Uninstall() error {
	_ = exec.Command("systemctl", "--user", "disable", "--now", unitName).Run()
	if err := os.Remove(a.unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func (a *linuxAgent) IsInstalled() bool {
	_, err := os.Stat(a.unitPath)
	return err == nil
}

func (a *linuxAgent) Status() (AgentStatus, error) {
	st := AgentStatus{Installed: a.IsInstalled()}
	out, _ := exec.Command("systemctl", "--user", "is-active", unitName).Output()
	active := strings.TrimSpace(string(out))
	if active == "active" {
		st.Running = true
		st.Detail = "active"
	} else if active != "" {
		st.Detail = active
	} else {
		st.Detail = "not loaded"
	}
	return st, nil
}
