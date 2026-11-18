package cmd

import (
	"bytes"
	"context"
	"errors"
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
	"github.com/filippolmt/toolbox/internal/orca"
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

func (d fakeDesktop) Version() (string, error)         { return d.version, nil }
func (d fakeDesktop) Environments() ([]string, error)  { return d.envs, nil }
func (d fakeDesktop) OpenPairingLink(url string) error { return d.note("open " + url) }
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
	orcaProfile = "work"
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
	attached, _ := orcaHarness(t, "", readyRecord)
	// Cobra's own required-flag check runs before RunE: it must let this through.
	if err := orcaCmd.ValidateRequiredFlags(); err != nil {
		t.Fatalf("cobra refuses toolbox orca without --version: %v", err)
	}
	err := runOrca(orcaCmd, []string{sessionWorkspace(t, "src")})
	if err == nil || !strings.Contains(err.Error(), "install Orca") || !strings.Contains(err.Error(), "--version") {
		t.Fatalf("runOrca = %v, want an error naming the desktop app and --version", err)
	}
	// The daemon is asked only whether an Orca Shell runs (none does here).
	if *attached != nil {
		t.Error("the missing version reached the attach")
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

// stoppingOrcaDaemon is orcaDaemon recording the containers stopped and
// removed.
type stoppingOrcaDaemon struct {
	orcaDaemon
	stopped *[]string
}

func (d stoppingOrcaDaemon) ContainerStop(_ context.Context, name string, _ client.ContainerStopOptions) (client.ContainerStopResult, error) {
	*d.stopped = append(*d.stopped, "stop "+name)
	return client.ContainerStopResult{}, nil
}

func (d stoppingOrcaDaemon) ContainerRemove(_ context.Context, name string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	*d.stopped = append(*d.stopped, "remove "+name)
	return client.ContainerRemoveResult{}, nil
}

// An installer that starts no server (offline, nothing cached) fails the
// command with its reason and removes the Orca Shell: none is left running
// empty.
func TestOrcaStopsTheOrcaShellWhenNoServerStarts(t *testing.T) {
	orcaHarness(t, "1.4.262", "toolbox: orca v1.4.262: start x\n"+
		"toolbox: orca: fatal: could not download Orca server v1.4.262 and no server is cached\n")
	var stopped []string
	newDockerClient = func() (client.APIClient, error) {
		return stoppingOrcaDaemon{stopped: &stopped}, nil
	}

	err := runOrca(orcaCmd, []string{sessionWorkspace(t, "src")})
	if !errors.Is(err, orca.ErrNotStarted) || !strings.Contains(err.Error(), "no server is cached") {
		t.Fatalf("runOrca = %v, want ErrNotStarted with the reason", err)
	}
	if got := strings.Join(stopped, ", "); got != "stop toolbox-orca, remove toolbox-orca" {
		t.Errorf("daemon calls %q, want toolbox-orca stopped and removed", got)
	}
}

// The offline fallback's warning reaches the user beside the pairing link.
func TestOrcaRelaysTheOfflineFallbackWarning(t *testing.T) {
	orcaHarness(t, "1.4.262", "toolbox: orca v1.4.262: start x\n"+
		"toolbox: orca: warning: could not download Orca server v1.4.262; starting the cached v1.4.200 instead\n"+
		`{"type":"orca_server_ready","pairing":{"available":true,"url":"orca://pair#fresh"}}`+"\n")
	orcaCmd.SetOut(&bytes.Buffer{})
	t.Cleanup(func() { orcaCmd.SetOut(nil) })

	var err error
	stderr := captureCmdStderr(t, func() { err = runOrca(orcaCmd, []string{sessionWorkspace(t, "src")}) })
	if err != nil {
		t.Fatalf("runOrca: %v", err)
	}
	if !strings.Contains(stderr, "v1.4.262") || !strings.Contains(stderr, "v1.4.200") {
		t.Errorf("stderr %q lacks the warning naming both versions", stderr)
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
			Env:    []string{sessionplan.OrcaVersionEnv + "=" + d.version, sessionplan.HostWorkspaceEnv + "=" + d.workspace},
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
	orcaShellCmd.InheritedFlags() // merges the persistent --profile, as cobra's parse does
	if err := orcaShellCmd.Flags().Set("profile", "work"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { orcaShellCmd.Flags().Lookup("profile").Changed = false })

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

// A second `toolbox orca` with no dir and no orca_workspace joins the running
// Orca Shell on its own workspace, whatever the version resolves from: with no
// desktop app and no --version its version stands in too, and only a version
// asked for that differs from the running one carries the mismatch note.
func TestOrcaJoinWithoutADirReportsTheRunningOrcaShell(t *testing.T) {
	for _, tc := range []struct {
		name, flag, app string
		mismatch        bool
	}{
		{name: "no app, no --version"},
		{name: "desktop app installed", app: "1.4.262"},
		{name: "--version differs", flag: "1.5.0", mismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orcaHarness(t, tc.flag, "")
			orcaDesktop = fakeDesktop{version: tc.app}
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

			if err := runOrca(orcaCmd, nil); err != nil {
				t.Fatalf("runOrca: %v", err)
			}
			for _, want := range []string{"already running Orca v1.4.262", "127.0.0.1:" + sessionplan.OrcaPort} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output %q lacks %q", out.String(), want)
				}
			}
			if got := strings.Contains(out.String(), "to run v1.5.0, toolbox orca stop first"); got != tc.mismatch {
				t.Errorf("output %q: version-mismatch note = %v, want %v", out.String(), got, tc.mismatch)
			}
		})
	}
}

// A join serves the running Orca Shell's tree: a workspace asked for, by the
// dir argument or orca_workspace, that differs from it is not applied, and the
// report says so the way it does for a version.
func TestOrcaJoinReportsAWorkspaceMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, arg, key string
		mismatch       bool
	}{
		{name: "same dir argument", arg: "src"},
		{name: "other dir argument", arg: "other", mismatch: true},
		{name: "other orca_workspace", key: "other", mismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orcaHarness(t, "v1.4.262", "")
			running, other := sessionWorkspace(t, "src"), sessionWorkspace(t, "other")
			var args []string
			if tc.arg != "" {
				args = []string{sessionWorkspace(t, tc.arg)}
			}
			if tc.key != "" {
				cfg.OrcaWorkspace = sessionWorkspace(t, tc.key)
			}
			newDockerClient = func() (client.APIClient, error) {
				return runningOrcaDaemon{version: "v1.4.262", workspace: running}, nil
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

			if err := runOrca(orcaCmd, args); err != nil {
				t.Fatalf("runOrca: %v", err)
			}
			note := "it keeps serving " + running + "; to serve " + other + ", toolbox orca stop first"
			if got := strings.Contains(out.String(), note); got != tc.mismatch {
				t.Errorf("output %q: workspace-mismatch note = %v, want %v", out.String(), got, tc.mismatch)
			}
			if strings.Contains(out.String(), "to run ") {
				t.Errorf("output %q carries a version-mismatch note", out.String())
			}
		})
	}
}

// `toolbox orca stop` with no Orca Shell succeeds quietly, safe in scripts
// (#1502 story 5); `toolbox stop <name>` keeps its "not found" warning.
func TestOrcaStopWithoutAnOrcaShellIsQuiet(t *testing.T) {
	orcaHarness(t, "", "")
	var err error
	stderr := captureCmdStderr(t, func() { err = runOrcaStop(orcaStopCmd, nil) })
	if err != nil {
		t.Fatalf("runOrcaStop = %v, want nil", err)
	}
	if strings.Contains(strings.ToLower(stderr), "not found") {
		t.Errorf("stderr %q warns about a missing Orca Shell", stderr)
	}
}
