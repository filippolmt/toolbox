package orca

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"runtime"
	"strings"
)

// BundleID is the Orca desktop app's macOS bundle identifier (appId in
// stablyai/orca config/electron-builder.config.cjs).
const BundleID = "com.stablyai.orca"

// defaultApp is where the dmg and the Homebrew cask install the app.
const defaultApp = "/Applications/Orca.app"

// HostDesktop returns the Orca desktop app of the machine the CLI runs on.
// shortcut: only macOS is looked up; Linux desktop builds read as no app, so
// --version is needed there. Add a Linux lookup when someone asks for it.
func HostDesktop() Desktop {
	if runtime.GOOS != "darwin" {
		return noDesktop{}
	}
	return MacDesktop{Run: func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output() //nolint:gosec // name and args are package-controlled
	}}
}

type noDesktop struct{}

func (noDesktop) Version() (string, error) { return "", nil }

// MacDesktop finds Orca.app in /Applications, else anywhere Spotlight indexed
// it by bundle identifier, and reads its CFBundleShortVersionString (the
// release version electron-builder stamps; CFBundleVersion may be a CI build
// number).
type MacDesktop struct {
	// Run runs a host command and returns its stdout.
	Run func(name string, args ...string) ([]byte, error)
}

func (d MacDesktop) Version() (string, error) {
	if v, err := d.bundleVersion(defaultApp); err == nil {
		return v, nil
	}
	out, err := d.Run("mdfind", "kMDItemCFBundleIdentifier == '"+BundleID+"'")
	if err != nil {
		return "", fmt.Errorf("mdfind %s: %w", BundleID, err)
	}
	for _, app := range strings.Split(string(out), "\n") {
		if app == "" {
			continue
		}
		if v, err := d.bundleVersion(app); err == nil {
			return v, nil
		}
	}
	return "", nil
}

func (d MacDesktop) bundleVersion(app string) (string, error) {
	out, err := d.Run("plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", app+"/Contents/Info.plist")
	return strings.TrimSpace(string(out)), err
}

func (noDesktop) Environments() ([]string, error)     { return nil, ErrNoCLI }
func (noDesktop) AddEnvironment(string, string) error { return ErrNoCLI }
func (noDesktop) RemoveEnvironment(string) error      { return ErrNoCLI }
func (noDesktop) OpenURL(string) error                { return errNoDesktop }

var errNoDesktop = errors.New("toolbox looks for the Orca desktop app on macOS only")

// bundledCLI is the CLI inside Orca.app: electron-builder's mac
// extraResources put resources/darwin/bin/orca at Contents/Resources/bin/orca.
const bundledCLI = "/Contents/Resources/bin/orca"

// orca runs the Orca CLI with args: the one bundled in each Orca.app found
// (/Applications, then Spotlight), else a user-installed `orca` on PATH.
// ErrNoCLI when none runs. Errors name only the subcommand, never the args:
// a pairing code is one.
func (d MacDesktop) orca(args ...string) ([]byte, error) {
	if out, ok, err := d.runCLI(defaultApp+bundledCLI, args); ok {
		return out, err
	}
	if found, err := d.Run("mdfind", "kMDItemCFBundleIdentifier == '"+BundleID+"'"); err == nil {
		for _, app := range strings.Split(string(found), "\n") {
			if app == "" || app == defaultApp {
				continue
			}
			if out, ok, err := d.runCLI(app+bundledCLI, args); ok {
				return out, err
			}
		}
	}
	if out, ok, err := d.runCLI("orca", args); ok {
		return out, err
	}
	return nil, ErrNoCLI
}

// runCLI runs one candidate CLI; ok is false when it is not there.
func (d MacDesktop) runCLI(cli string, args []string) (out []byte, ok bool, err error) {
	out, err = d.Run(cli, args...)
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err == nil {
		return out, true, nil
	}
	sub := strings.Join(args[:2], " ")
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return nil, true, fmt.Errorf("%s %s: %w: %s", cli, sub, err, strings.TrimSpace(string(exit.Stderr)))
	}
	return nil, true, fmt.Errorf("%s %s: %w", cli, sub, err)
}

// Environments reads `orca environment list --json`: the CLI's success
// envelope, {"result":{"environments":[{"name":…},…]}}.
func (d MacDesktop) Environments() ([]string, error) {
	out, err := d.orca("environment", "list", "--json")
	if err != nil {
		return nil, err
	}
	var list struct {
		Result struct {
			Environments []struct {
				Name string `json:"name"`
			} `json:"environments"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("orca environment list: unreadable output: %w", err)
	}
	names := make([]string, 0, len(list.Result.Environments))
	for _, e := range list.Result.Environments {
		names = append(names, e.Name)
	}
	return names, nil
}

// AddEnvironment puts the pairing link on the CLI's command line for the
// duration of the call, the way Orca documents `environment add`.
func (d MacDesktop) AddEnvironment(name, pairingURL string) error {
	_, err := d.orca("environment", "add", "--name", name, "--pairing-code", pairingURL, "--json")
	return err
}

func (d MacDesktop) RemoveEnvironment(name string) error {
	_, err := d.orca("environment", "rm", "--environment", name, "--json")
	return err
}

// OpenURL uses macOS `open`; Orca.app registers the orca:// scheme.
func (d MacDesktop) OpenURL(url string) error {
	_, err := d.Run("open", url)
	return err
}
