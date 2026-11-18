package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// orcaDaemon answers the one call `toolbox orca` makes before the attach: is
// an Orca Shell already there. None is.
type orcaDaemon struct{ stubDaemon }

func (orcaDaemon) ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return client.ContainerInspectResult{}, cerrdefs.ErrNotFound
}

// fakeDesktop is an Orca desktop app reporting version; "" is no app.
type fakeDesktop struct{ version string }

func (d fakeDesktop) Version() (string, error) { return d.version, nil }

// orcaHarness sets the orca flag globals and cfg, and returns the plan the
// attach was handed plus whether the Docker client was ever built. The fake
// attach writes record into the plan's server log, as the server would.
func orcaHarness(t *testing.T, version, record string) (**sessionplan.SessionPlan, *bool) {
	t.Helper()
	sessionHarness(t)
	origCfg, origVersion, origProfile, origDesktop := cfg, orcaVersion, orcaProfile, orcaDesktop
	t.Cleanup(func() { cfg, orcaVersion, orcaProfile, orcaDesktop = origCfg, origVersion, origProfile, origDesktop })
	cfg, orcaVersion, orcaProfile, orcaDesktop = &config.Config{Shell: "zsh"}, version, "", fakeDesktop{}

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
