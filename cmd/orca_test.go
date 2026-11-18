package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/runplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// orcaDaemon answers the one call `toolbox orca` makes before the attach: is
// an Orca Shell already there. None is.
type orcaDaemon struct{ stubDaemon }

func (orcaDaemon) ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return client.ContainerInspectResult{}, cerrdefs.ErrNotFound
}

// fakeDesktop is an Orca desktop app reporting version ("" is no app) whose
// pairing store holds envs. Changes to the store land in log when set.
type fakeDesktop struct {
	version string
	envs    []string
	log     *[]string
}

func (d fakeDesktop) Version() (string, error)        { return d.version, nil }
func (d fakeDesktop) Environments() ([]string, error) { return d.envs, nil }
func (d fakeDesktop) OpenURL(url string) error        { return d.note("open " + url) }
func (d fakeDesktop) AddEnvironment(name, url string) error {
	return d.note("add " + name + " " + url)
}
func (d fakeDesktop) RemoveEnvironment(name string) error { return d.note("rm " + name) }

func (d fakeDesktop) note(call string) error {
	if d.log != nil {
		*d.log = append(*d.log, call)
	}
	return nil
}

// fakeOffer makes the Orca Shell's server offer orca://pair?code=<container>
// and returns the containers asked.
func fakeOffer(t *testing.T) *[]string {
	t.Helper()
	orig := orcaOffer
	t.Cleanup(func() { orcaOffer = orig })
	var asked []string
	orcaOffer = func(_ context.Context, _ client.APIClient, container string) (string, error) {
		asked = append(asked, container)
		return "orca://pair?code=" + container, nil
	}
	return &asked
}

// orcaHarness sets the orca flag globals and cfg, and returns the plan the
// attach was handed plus whether the Docker client was ever built. The fake
// attach writes record into the plan's server log, as the server would.
func orcaHarness(t *testing.T, version, record string) (**sessionplan.SessionPlan, *bool) {
	t.Helper()
	sessionHarness(t)
	origCfg, origVersion, origProfile, origDesktop := cfg, orcaVersion, orcaProfile, orcaDesktop
	t.Cleanup(func() { cfg, orcaVersion, orcaProfile, orcaDesktop = origCfg, origVersion, origProfile, origDesktop })
	cfg, orcaVersion, orcaProfile, orcaDesktop = &config.Config{Shell: "zsh"}, version, "", fakeDesktop{}
	fakeOffer(t)

	built := false
	newDockerClient = func() (client.APIClient, error) {
		built = true
		return orcaDaemon{}, nil
	}
	var attached *sessionplan.SessionPlan
	shellFn = func(_ context.Context, _ client.APIClient, p *sessionplan.SessionPlan) (*reload.From, error) {
		attached = p
		log := p.OrcaLogPath()
		if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(log, []byte(record), 0o600)
	}
	return &attached, &built
}

func TestOrcaRefusesAMalformedVersionBeforePlanning(t *testing.T) {
	attached, built := orcaHarness(t, "latest", "")
	err := runOrca(orcaCmd, []string{sessionWorkspace(t, "src")})
	if err == nil || !strings.Contains(err.Error(), "--version") {
		t.Fatalf("runOrca = %v, want a --version refusal", err)
	}
	if *built || *attached != nil {
		t.Error("a malformed version reached the daemon or the attach")
	}
}

func TestOrcaStartsTheOrcaShellAndAddsItsEnvironmentToTheDesktopApp(t *testing.T) {
	attached, _ := orcaHarness(t, "1.4.262",
		`{"type":"orca_server_ready","pairing":{"available":true,"url":"orca://pair#fresh"}}`+"\n")
	var calls []string
	orcaDesktop = fakeDesktop{envs: []string{"laptop"}, log: &calls}
	asked := fakeOffer(t)
	orcaCmd.SetOut(&bytes.Buffer{})
	t.Cleanup(func() { orcaCmd.SetOut(nil) })

	ws := sessionWorkspace(t, "src")
	if err := runOrca(orcaCmd, []string{ws}); err != nil {
		t.Fatalf("runOrca: %v", err)
	}
	p := *attached
	if p == nil || p.ContainerName != "toolbox-orca" || !p.Detached {
		t.Fatalf("attached plan = %+v, want the detached toolbox-orca", p)
	}
	if got := sessionplan.EnvValue(p.Env, sessionplan.OrcaVersionEnv); got != "v1.4.262" {
		t.Errorf("%s = %q, want the normalised tag", sessionplan.OrcaVersionEnv, got)
	}
	if got := sessionplan.EnvValue(p.Env, "TOOLBOX_HOST_WORKSPACE"); got != ws {
		t.Errorf("workspace = %q, want the dir argument %q", got, ws)
	}
	if !slices.Equal(*asked, []string{"toolbox-orca"}) || !slices.Equal(calls, []string{"add toolbox orca://pair?code=toolbox-orca"}) {
		t.Errorf("offers from %q, desktop calls %q; want toolbox added from the Orca Shell's offer", *asked, calls)
	}
}

func TestOrcaMakesNoPairingCallWhenTheAppHasTheEnvironment(t *testing.T) {
	orcaHarness(t, "1.4.262", readyRecord)
	var calls []string
	orcaDesktop = fakeDesktop{envs: []string{"toolbox"}, log: &calls}
	asked := fakeOffer(t)
	orcaCmd.SetOut(&bytes.Buffer{})
	t.Cleanup(func() { orcaCmd.SetOut(nil) })

	if err := runOrca(orcaCmd, []string{sessionWorkspace(t, "src")}); err != nil {
		t.Fatalf("runOrca: %v", err)
	}
	if len(*asked) != 0 || len(calls) != 0 {
		t.Errorf("offers from %q, desktop calls %q; want none", *asked, calls)
	}
}

func TestOrcaUnderAProfileAddsTheProfileNamedEnvironment(t *testing.T) {
	orcaHarness(t, "1.4.262", readyRecord)
	orcaProfile = "work"
	var calls []string
	orcaDesktop = fakeDesktop{envs: []string{"toolbox"}, log: &calls}
	orcaCmd.SetOut(&bytes.Buffer{})
	t.Cleanup(func() { orcaCmd.SetOut(nil) })

	if err := runOrca(orcaCmd, []string{sessionWorkspace(t, "src")}); err != nil {
		t.Fatalf("runOrca: %v", err)
	}
	if !slices.Equal(calls, []string{"add toolbox-work orca://pair?code=toolbox-orca-work"}) {
		t.Errorf("desktop calls %q, want toolbox-work added from toolbox-orca-work", calls)
	}
}

func TestOrcaPairRemovesAndReAddsTheEnvironment(t *testing.T) {
	orcaHarness(t, "", "")
	orcaPairProfile = "work"
	t.Cleanup(func() { orcaPairProfile = "" })
	var calls []string
	orcaDesktop = fakeDesktop{envs: []string{"toolbox", "toolbox-work"}, log: &calls}
	orcaPairCmd.SetOut(&bytes.Buffer{})
	t.Cleanup(func() { orcaPairCmd.SetOut(nil) })

	if err := runOrcaPair(orcaPairCmd, nil); err != nil {
		t.Fatalf("runOrcaPair: %v", err)
	}
	want := []string{"rm toolbox-work", "add toolbox-work orca://pair?code=toolbox-orca-work"}
	if !slices.Equal(calls, want) {
		t.Errorf("desktop calls %q, want %q", calls, want)
	}
}

const readyRecord = `{"type":"orca_server_ready","pairing":{"available":true,"url":"orca://pair#fresh"}}` + "\n"

func TestOrcaWithoutVersionRunsTheDesktopAppsVersion(t *testing.T) {
	attached, _ := orcaHarness(t, "", readyRecord)
	orcaDesktop = fakeDesktop{version: "1.4.300"}
	orcaCmd.SetOut(&bytes.Buffer{})
	t.Cleanup(func() { orcaCmd.SetOut(nil) })

	if err := runOrca(orcaCmd, []string{sessionWorkspace(t, "src")}); err != nil {
		t.Fatalf("runOrca: %v", err)
	}
	if got := sessionplan.EnvValue((*attached).Env, sessionplan.OrcaVersionEnv); got != "v1.4.300" {
		t.Errorf("%s = %q, want the desktop app's version", sessionplan.OrcaVersionEnv, got)
	}
}

func TestOrcaWithoutVersionOrDesktopAppNamesBothRemediesAndCreatesNoContainer(t *testing.T) {
	attached, built := orcaHarness(t, "", readyRecord)
	// Cobra's own required-flag check runs before RunE: it must let this through.
	if err := orcaCmd.ValidateRequiredFlags(); err != nil {
		t.Fatalf("cobra refuses toolbox orca without --version: %v", err)
	}
	err := runOrca(orcaCmd, []string{sessionWorkspace(t, "src")})
	if err == nil || !strings.Contains(err.Error(), "install Orca") || !strings.Contains(err.Error(), "--version") {
		t.Fatalf("runOrca = %v, want an error naming the desktop app and --version", err)
	}
	if *built || *attached != nil {
		t.Error("the missing version reached the daemon or the attach")
	}
}

func TestOrcaVersionFlagWinsOverTheDesktopApp(t *testing.T) {
	attached, _ := orcaHarness(t, "1.4.262", readyRecord)
	orcaDesktop = fakeDesktop{version: "1.4.300"}
	orcaCmd.SetOut(&bytes.Buffer{})
	t.Cleanup(func() { orcaCmd.SetOut(nil) })

	if err := runOrca(orcaCmd, []string{sessionWorkspace(t, "src")}); err != nil {
		t.Fatalf("runOrca: %v", err)
	}
	if got := sessionplan.EnvValue((*attached).Env, sessionplan.OrcaVersionEnv); got != "v1.4.262" {
		t.Errorf("%s = %q, want the --version tag", sessionplan.OrcaVersionEnv, got)
	}
}

func TestOrcaShellWithoutAnOrcaShellPointsAtToolboxOrca(t *testing.T) {
	attached, _ := orcaHarness(t, "", "")
	err := runOrcaShell(orcaShellCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "start it with toolbox orca") {
		t.Fatalf("runOrcaShell = %v, want an error pointing at toolbox orca", err)
	}
	if *attached != nil {
		t.Error("`toolbox orca shell` with no Orca Shell reached the attach")
	}
}

// runningOrcaDaemon answers that the Orca Shell is up, detached, and was
// started at version on workspace.
type runningOrcaDaemon struct {
	stubDaemon
	version, workspace string
}

func (d runningOrcaDaemon) ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return client.ContainerInspectResult{Container: dockercontainer.InspectResponse{
		ID:         "orca1",
		HostConfig: &dockercontainer.HostConfig{AutoRemove: true},
		State:      &dockercontainer.State{Running: true},
		Config: &dockercontainer.Config{
			Labels: runplan.DetachedLabels(),
			Env:    []string{sessionplan.OrcaVersionEnv + "=" + d.version, "TOOLBOX_HOST_WORKSPACE=" + d.workspace},
		},
	}}, nil
}

// The terminal is planned as the Orca Shell it joins — its version and
// workspace, not a detached start — and a Session Reload from it re-enters
// `toolbox orca shell` with the profile, so the recreate restarts the server.
func TestOrcaShellPlansTheRunningOrcaShellAndReentersIt(t *testing.T) {
	attached, _ := orcaHarness(t, "", "")
	ws := sessionWorkspace(t, "src")
	newDockerClient = func() (client.APIClient, error) {
		return runningOrcaDaemon{version: "v1.4.262", workspace: ws}, nil
	}
	shellFn = func(_ context.Context, _ client.APIClient, p *sessionplan.SessionPlan) (*reload.From, error) {
		*attached = p
		return &reload.From{Container: p.ContainerName}, nil
	}
	exec := stubExecSelf(t, "/usr/local/bin/toolbox")
	if err := orcaShellCmd.Flags().Set("profile", "work"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		orcaShellProfile = ""
		orcaShellCmd.Flags().Lookup("profile").Changed = false
	})

	_ = runOrcaShell(orcaShellCmd, nil) // the exec stub reports the reload as an error

	p := *attached
	if p == nil || p.ContainerName != "toolbox-orca-work" || p.Detached {
		t.Fatalf("attached plan = %+v, want an attached toolbox-orca-work", p)
	}
	if got := sessionplan.EnvValue(p.Env, sessionplan.OrcaVersionEnv); got != "v1.4.262" {
		t.Errorf("%s = %q, want the running Orca Shell's", sessionplan.OrcaVersionEnv, got)
	}
	if got := sessionplan.EnvValue(p.Env, "TOOLBOX_HOST_WORKSPACE"); got != ws {
		t.Errorf("workspace = %q, want the running Orca Shell's %q", got, ws)
	}
	want := []string{"/usr/local/bin/toolbox", "orca", "shell", "--profile", "work"}
	if !slices.Equal(exec.argv, want) {
		t.Errorf("reload argv = %q, want %q", exec.argv, want)
	}
}

// A second `toolbox orca` joins the running Orca Shell: the real attach with
// a daemon that knows no create (the stub panics on any call it does not
// answer), and a report of what is running and where, not a fresh start.
func TestOrcaJoinMakesNoCreateAndPrintsStateAndEndpoint(t *testing.T) {
	orcaHarness(t, "1.5.0", "")
	ws := sessionWorkspace(t, "src")
	newDockerClient = func() (client.APIClient, error) {
		return runningOrcaDaemon{version: "v1.4.262", workspace: ws}, nil
	}
	shellFn = func(ctx context.Context, cli client.APIClient, p *sessionplan.SessionPlan) (*reload.From, error) {
		log := p.OrcaLogPath()
		if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(log, []byte("toolbox: orca v1.4.262: start x\n"+
			`{"type":"orca_server_ready","pairing":{"available":true,"url":"orca://pair#running"}}`+"\n"), 0o600); err != nil {
			return nil, err
		}
		return container.Shell(ctx, cli, p)
	}
	var out bytes.Buffer
	orcaCmd.SetOut(&out)
	t.Cleanup(func() { orcaCmd.SetOut(nil) })

	if err := runOrca(orcaCmd, []string{ws}); err != nil {
		t.Fatalf("runOrca: %v", err)
	}
	for _, want := range []string{"already running", "v1.4.262", "to run v1.5.0, toolbox orca stop first", "127.0.0.1:" + sessionplan.OrcaPort} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q lacks %q", out.String(), want)
		}
	}
}
