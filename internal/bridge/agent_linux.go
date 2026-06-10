//go:build linux

package bridge

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/filippolmt/toolbox/internal/fsx"
)

const unitName = "toolbox-bridge.service"

// legacyUnitName is the pre-rename systemd unit. Install/Uninstall disable
// and remove it so one daemon never runs twice.
const legacyUnitName = "toolbox-browser.service"

const unitTemplate = `[Unit]
Description=Toolbox Bridge
After=network.target

[Service]
ExecStart={{.Exec}} bridge daemon
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`

type linuxAgent struct {
	unitPath       string
	legacyUnitPath string
}

func NewAgent() (Agent, error) {
	home, err := fsx.Home()
	if err != nil {
		return nil, err
	}
	units := filepath.Join(home, ".config", "systemd", "user")
	return &linuxAgent{
		unitPath:       filepath.Join(units, unitName),
		legacyUnitPath: filepath.Join(units, legacyUnitName),
	}, nil
}

// removeLegacy stops and deletes the pre-rename unit. The systemctl calls
// are best-effort (already gone is fine); the file removal only fails on
// real fs errors.
func (a *linuxAgent) removeLegacy() error {
	_ = exec.Command("systemctl", "--user", "disable", "--now", legacyUnitName).Run()
	if err := os.Remove(a.legacyUnitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func renderUnit(execPath string) (string, error) {
	return renderTemplate("unit", unitTemplate, map[string]string{"Exec": execPath})
}

func (a *linuxAgent) Install(execPath string) error {
	body, err := renderUnit(execPath)
	if err != nil {
		return err
	}
	if err := writeServiceFile(a.unitPath, []byte(body)); err != nil {
		return err
	}
	if err := a.removeLegacy(); err != nil {
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
	if err := a.removeLegacy(); err != nil {
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
