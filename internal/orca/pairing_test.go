package orca

import (
	"bytes"
	"errors"
	"io/fs"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// fakeApp is an Orca desktop app whose pairing store is envs. noCLI makes the
// environment calls fail as with no Orca CLI; openErr fails OpenPairingLink.
type fakeApp struct {
	envs    []string
	noCLI   bool
	openErr error
	calls   []string
}

func (a *fakeApp) Version() (string, error) { return "", nil }

func (a *fakeApp) Environments() ([]string, error) {
	a.calls = append(a.calls, "list")
	if a.noCLI {
		return nil, ErrNoCLI
	}
	return a.envs, nil
}

func (a *fakeApp) AddEnvironment(name, url string) error {
	a.calls = append(a.calls, "add "+name+" "+url)
	a.envs = append(a.envs, name)
	return nil
}

func (a *fakeApp) RemoveEnvironment(name string) error {
	a.calls = append(a.calls, "rm "+name)
	a.envs = slices.DeleteFunc(a.envs, func(e string) bool { return e == name })
	return nil
}

func (a *fakeApp) OpenPairingLink(url string) error {
	a.calls = append(a.calls, "open "+url)
	return a.openErr
}

// offerOf counts the times the Orca Shell is asked for an offer.
func offerOf(url string, asked *int) func() (string, error) {
	return func() (string, error) { *asked++; return url, nil }
}

func TestPairAddsTheEnvironmentFromTheOrcaShellsOffer(t *testing.T) {
	app := &fakeApp{envs: []string{"laptop"}}
	asked := 0
	if err := Pair(app, "toolbox", false, offerOf("orca://pair?code=a", &asked), &bytes.Buffer{}); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if !slices.Equal(app.calls, []string{"list", "add toolbox orca://pair?code=a"}) {
		t.Errorf("desktop calls = %q, want a list then an add from the offer", app.calls)
	}
}

func TestPairSkipsAnEnvironmentTheAppAlreadyHas(t *testing.T) {
	app := &fakeApp{envs: []string{"toolbox"}}
	asked := 0
	if err := Pair(app, "toolbox", false, offerOf("orca://pair?code=a", &asked), &bytes.Buffer{}); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if asked != 0 || !slices.Equal(app.calls, []string{"list"}) {
		t.Errorf("offers = %d, desktop calls = %q; want no pairing call", asked, app.calls)
	}
}

func TestPairForcedRemovesAndReAddsTheEnvironment(t *testing.T) {
	app := &fakeApp{envs: []string{"toolbox-work"}}
	asked := 0
	if err := Pair(app, "toolbox-work", true, offerOf("orca://pair?code=b", &asked), &bytes.Buffer{}); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	want := []string{"list", "rm toolbox-work", "add toolbox-work orca://pair?code=b"}
	if !slices.Equal(app.calls, want) {
		t.Errorf("desktop calls = %q, want %q", app.calls, want)
	}
}

func TestPairWithoutTheCLIOpensTheLink(t *testing.T) {
	app := &fakeApp{noCLI: true}
	asked := 0
	var out bytes.Buffer
	if err := Pair(app, "toolbox", false, offerOf("orca://pair?code=c", &asked), &out); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if !slices.Equal(app.calls, []string{"list", "open orca://pair?code=c"}) {
		t.Errorf("desktop calls = %q, want the link opened", app.calls)
	}
}

func TestPairPrintsTheLinkWhenOpeningItFails(t *testing.T) {
	app := &fakeApp{noCLI: true, openErr: errors.New("no open command")}
	asked := 0
	var out bytes.Buffer
	if err := Pair(app, "toolbox", false, offerOf("orca://pair?code=d", &asked), &out); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("orca://pair?code=d")) {
		t.Errorf("output %q lacks the link", out.String())
	}
}

// fakeCLI is a Mac whose Orca CLI sits at cli ("" for none) and prints out;
// every command it runs lands in ran.
func fakeCLI(cli, out string, ran *[]string) MacDesktop {
	return MacDesktop{Run: func(name string, args ...string) ([]byte, error) {
		*ran = append(*ran, name+" "+strings.Join(args, " "))
		if name == cli {
			return []byte(out), nil
		}
		switch name {
		case "mdfind":
			return nil, nil
		case "orca":
			return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		return nil, &fs.PathError{Op: "fork/exec", Path: name, Err: fs.ErrNotExist}
	}}
}

const listJSON = `{
  "id": "local",
  "ok": true,
  "result": {"environments": [{"id": "e1", "name": "laptop"}, {"id": "e2", "name": "toolbox"}]},
  "_meta": {"runtimeId": "local"}
}`

func TestMacDesktopListsWithTheCLIInsideTheApp(t *testing.T) {
	var ran []string
	got, err := fakeCLI("/Applications/Orca.app/Contents/Resources/bin/orca", listJSON, &ran).Environments()
	if err != nil || !slices.Equal(got, []string{"laptop", "toolbox"}) {
		t.Fatalf("Environments = %q, %v; want laptop and toolbox", got, err)
	}
	if ran[0] != "/Applications/Orca.app/Contents/Resources/bin/orca environment list --json" {
		t.Errorf("first command = %q, want the bundled CLI", ran[0])
	}
}

func TestMacDesktopFallsBackToTheCLIOnPath(t *testing.T) {
	var ran []string
	d := fakeCLI("orca", "{}", &ran)
	if err := d.AddEnvironment("toolbox", "orca://pair?code=x"); err != nil {
		t.Fatalf("AddEnvironment: %v", err)
	}
	if last := ran[len(ran)-1]; last != "orca environment add --name toolbox --pairing-code orca://pair?code=x --json" {
		t.Errorf("last command = %q, want the add through orca on PATH", last)
	}
	if err := d.RemoveEnvironment("toolbox"); err != nil {
		t.Fatalf("RemoveEnvironment: %v", err)
	}
	if last := ran[len(ran)-1]; last != "orca environment rm --environment toolbox --json" {
		t.Errorf("last command = %q, want the rm through orca on PATH", last)
	}
}

func TestMacDesktopWithoutAnyCLIReportsErrNoCLI(t *testing.T) {
	var ran []string
	if _, err := fakeCLI("", "", &ran).Environments(); !errors.Is(err, ErrNoCLI) {
		t.Errorf("Environments = %v, want ErrNoCLI", err)
	}
}

func TestMacDesktopKeepsThePairingCodeOutOfItsErrors(t *testing.T) {
	d := MacDesktop{Run: func(string, ...string) ([]byte, error) { return nil, errors.New("exit status 1") }}
	err := d.AddEnvironment("toolbox", "orca://pair?code=secret")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("AddEnvironment = %v, want an error without the code", err)
	}
}

func TestMacDesktopOpensTheLinkWithOpen(t *testing.T) {
	var ran []string
	if err := fakeCLI("open", "", &ran).OpenPairingLink("orca://pair?code=y"); err != nil || ran[0] != "open orca://pair?code=y" {
		t.Errorf("OpenPairingLink ran %q, %v; want open with the link", ran, err)
	}
}

// TestMacDesktopRefusesALinkThatIsNotOrcas: the link reaches a host command's
// argv, so anything but an orca:// link — an option, another scheme —
// is refused before any command runs, without echoing it (it may carry a code).
func TestMacDesktopRefusesALinkThatIsNotOrcas(t *testing.T) {
	for _, link := range []string{"", "-secret", "--pairing-code=secret", "https://secret.example", "orca:secret", "ORCA://secret", " orca://secret"} {
		var ran []string
		d := fakeCLI("orca", "{}", &ran)
		for op, call := range map[string]func() error{
			"AddEnvironment":  func() error { return d.AddEnvironment("toolbox", link) },
			"OpenPairingLink": func() error { return d.OpenPairingLink(link) },
		} {
			err := call()
			if err == nil || (link != "" && strings.Contains(err.Error(), "secret")) {
				t.Errorf("%s(%q) = %v, want a refusal that does not echo the link", op, link, err)
			}
		}
		if len(ran) != 0 {
			t.Errorf("link %q ran %q, want nothing run", link, ran)
		}
	}
}

// TestMacDesktopTakesOnlyAbsoluteAppPathsFromSpotlight: a Spotlight result
// becomes an executable path, so only absolute paths ending in .app count.
func TestMacDesktopTakesOnlyAbsoluteAppPathsFromSpotlight(t *testing.T) {
	var ran []string
	d := MacDesktop{Run: func(name string, args ...string) ([]byte, error) {
		ran = append(ran, name)
		if name == "mdfind" {
			return []byte("Orca.app\n-rf.app\n/Users/me/Orca\n/Users/me/Apps/Orca.app\n"), nil
		}
		return nil, &fs.PathError{Op: "fork/exec", Path: name, Err: fs.ErrNotExist}
	}}
	_, _ = d.Environments()
	want := []string{"/Applications/Orca.app/Contents/Resources/bin/orca", "mdfind", "/Users/me/Apps/Orca.app/Contents/Resources/bin/orca", "orca"}
	if !slices.Equal(ran, want) {
		t.Errorf("ran %q, want %q", ran, want)
	}
}
