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
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
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

// orcaHarness sets the orca flag globals and cfg, and returns the plan the
// attach was handed plus whether the Docker client was ever built. The fake
// attach writes record into the plan's server log, as the server would.
func orcaHarness(t *testing.T, version, record string) (**sessionplan.SessionPlan, *bool) {
	t.Helper()
	sessionHarness(t)
	origCfg, origVersion, origProfile := cfg, orcaVersion, orcaProfile
	t.Cleanup(func() { cfg, orcaVersion, orcaProfile = origCfg, origVersion, origProfile })
	cfg, orcaVersion, orcaProfile = &config.Config{Shell: "zsh"}, version, ""

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

func TestOrcaStartsTheOrcaShellAndPrintsThePairingLink(t *testing.T) {
	attached, _ := orcaHarness(t, "1.4.262",
		`{"type":"orca_server_ready","pairing":{"available":true,"url":"orca://pair#fresh"}}`+"\n")
	var out bytes.Buffer
	orcaCmd.SetOut(&out)
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
	if !strings.Contains(out.String(), "orca://pair#fresh") {
		t.Errorf("output %q lacks the pairing link", out.String())
	}
}

func TestOrcaShellWithoutAnOrcaShellPointsAtToolboxOrca(t *testing.T) {
	attached, _ := orcaHarness(t, "", "")
	err := runOrcaShell(orcaShellCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "toolbox orca --version") {
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
	return client.ContainerInspectResult{Container: container.InspectResponse{
		State: &container.State{Running: true},
		Config: &container.Config{
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
