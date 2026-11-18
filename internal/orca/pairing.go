package orca

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// ErrNoCLI is what Desktop's environment calls return when no Orca CLI is
// there to read or write the app's pairing store.
var ErrNoCLI = errors.New("orca: no Orca CLI found (neither inside Orca.app nor on PATH)")

// EnvironmentName is the desktop environment of the Orca Shell named shell:
// `toolbox` for toolbox-orca, `toolbox-<profile>` for toolbox-orca-<profile>.
func EnvironmentName(shell string) string {
	return "toolbox" + strings.TrimPrefix(shell, "toolbox-orca")
}

// Pair registers the Orca Shell's runtime with the desktop app as the
// environment name. An environment already there is kept unless force, which
// replaces it. offer asks the Orca Shell for a pairing link; it runs only when
// a link is needed, and before any removal, so a failed offer leaves the app
// as it was. Without an Orca CLI the link goes to the app's URL scheme, and
// is printed in any case: the scheme is the last automatic path.
func Pair(d Desktop, name string, force bool, offer func() (string, error), out io.Writer) error {
	envs, err := d.Environments()
	if err != nil && !errors.Is(err, ErrNoCLI) {
		return fmt.Errorf("orca: listing the desktop app's environments: %w", err)
	}
	present := slices.Contains(envs, name)
	if err == nil && present && !force {
		_, _ = fmt.Fprintf(out, "Orca already lists the environment %s; 'toolbox orca pair' pairs it again.\n", name)
		return nil
	}
	url, offerErr := offer()
	if offerErr != nil {
		return fmt.Errorf("orca: asking the Orca Shell for a pairing link: %w", offerErr)
	}
	if err != nil {
		return openOrPrint(d, url, out)
	}
	if present {
		if err := d.RemoveEnvironment(name); err != nil {
			return fmt.Errorf("orca: removing the environment %s: %w", name, err)
		}
	}
	if err := d.AddEnvironment(name, url); err != nil {
		return fmt.Errorf("orca: adding the environment %s: %w", name, err)
	}
	_, _ = fmt.Fprintf(out, "Added the environment %s to the Orca desktop app.\n", name)
	return nil
}

// openOrPrint is the fallback without a CLI. The link is printed even after
// it opened: the app takes the scheme, but whether it acts on a pairing link
// depends on its version.
func openOrPrint(d Desktop, url string, out io.Writer) error {
	if err := d.OpenURL(url); err == nil {
		_, _ = fmt.Fprintln(out, "No Orca CLI found: opened the pairing link with Orca.")
	} else {
		_, _ = fmt.Fprintf(out, "No Orca CLI found, and the link did not open (%v).\n", err)
	}
	_, _ = fmt.Fprintf(out, "If Orca does not list the runtime, paste this link in Orca (Settings → Remote Orca Servers → Add Server):\n%s\n", url)
	return nil
}
