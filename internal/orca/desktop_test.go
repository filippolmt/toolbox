package orca

import (
	"errors"
	"strings"
	"testing"
)

// fakeMac answers plutil for the Info.plist of the apps in versions and mdfind
// with found, the way macOS prints them.
func fakeMac(versions map[string]string, found string) MacDesktop {
	return MacDesktop{Run: func(name string, args ...string) ([]byte, error) {
		switch name {
		case "plutil":
			plist := args[len(args)-1]
			if v, ok := versions[strings.TrimSuffix(plist, "/Contents/Info.plist")]; ok && args[1] == "CFBundleShortVersionString" {
				return []byte(v + "\n"), nil
			}
			return nil, errors.New("plutil: no such file")
		case "mdfind":
			if args[0] == "kMDItemCFBundleIdentifier == 'com.stablyai.orca'" {
				return []byte(found), nil
			}
		}
		return nil, errors.New("unexpected command " + name)
	}}
}

func TestMacDesktopReadsTheAppInApplications(t *testing.T) {
	got, err := fakeMac(map[string]string{"/Applications/Orca.app": "1.4.262"}, "").Version()
	if err != nil || got != "1.4.262" {
		t.Errorf("Version = %q, %v; want 1.4.262", got, err)
	}
}

func TestMacDesktopFindsTheAppElsewhereByBundleID(t *testing.T) {
	d := fakeMac(map[string]string{"/Users/me/Apps/Orca.app": "1.4.300"}, "/Users/me/Apps/Orca.app\n")
	got, err := d.Version()
	if err != nil || got != "1.4.300" {
		t.Errorf("Version = %q, %v; want the version of the app Spotlight found", got, err)
	}
}

func TestMacDesktopWithoutAppReportsNone(t *testing.T) {
	got, err := fakeMac(nil, "").Version()
	if err != nil || got != "" {
		t.Errorf("Version = %q, %v; want no app", got, err)
	}
}

type staticDesktop string

func (d staticDesktop) Version() (string, error) { return string(d), nil }

func TestResolveVersionRefusesAnRCDesktopWithItsVersionAndTheFlag(t *testing.T) {
	// The orca@rc cask ships versions like this; their releases carry no server.
	_, err := ResolveVersion("", staticDesktop("1.4.36-rc.3"))
	if err == nil || !strings.Contains(err.Error(), "1.4.36-rc.3") || !strings.Contains(err.Error(), "--version") {
		t.Errorf("ResolveVersion = %v, want the app's version and --version named", err)
	}
}
