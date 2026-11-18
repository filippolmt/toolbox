package orca

import (
	"errors"
	"fmt"
)

// Desktop is the Orca desktop app on the host: the seam `toolbox orca` reads
// the app's version through, so the server it starts speaks the same protocol,
// and registers the Orca Shell's runtime with, so nobody pastes a link.
type Desktop interface {
	// Version returns the app's release version, "" when no app is installed.
	Version() (string, error)
	// Environments lists the names in the app's pairing store; ErrNoCLI when
	// no Orca CLI can read it.
	Environments() ([]string, error)
	// AddEnvironment saves a paired runtime under name from an orca://pair link.
	AddEnvironment(name, pairingURL string) error
	// RemoveEnvironment drops the environment called name.
	RemoveEnvironment(name string) error
	// OpenPairingLink hands an orca:// pairing link to the app through the OS
	// URL scheme.
	OpenPairingLink(url string) error
}

// ResolveVersion returns the release tag to run: the --version flag when set,
// else the desktop app's version. Neither is an error naming both remedies.
func ResolveVersion(flag string, d interface{ Version() (string, error) }) (string, error) {
	if flag != "" {
		return NormalizeVersion(flag)
	}
	v, err := d.Version()
	if err != nil {
		return "", fmt.Errorf("orca: reading the Orca desktop app's version: %w; pass --version", err)
	}
	if v == "" {
		return "", errors.New("orca: no Orca desktop app found to take the server version from: install Orca, or pass --version (e.g. --version 1.4.262)")
	}
	tag, err := NormalizeVersion(v)
	if err != nil {
		return "", fmt.Errorf("orca: the Orca desktop app reports version %q, which has no server release; pass --version", v)
	}
	return tag, nil
}
