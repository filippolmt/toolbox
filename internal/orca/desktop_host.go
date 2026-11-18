package orca

import (
	"fmt"
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
