package sessionplan_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/network"

	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// TestPlanOrcaShell pins what makes a plan the Orca Shell: the fixed name per
// Profile, Orca's port on host loopback, the env gate and Orca's data directory
// on the state mount, and the detached create.
func TestPlanOrcaShell(t *testing.T) {
	planHost, workspace := planWorkspace(t)

	for _, tc := range []struct {
		label   string
		profile *mountplan.Profile
		name    string
	}{
		{"no profile", nil, "toolbox-orca"},
		{"profile", &mountplan.Profile{Name: "Work"}, "toolbox-orca-work"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(),
				Workspace: workspace, Profile: tc.profile, OrcaVersion: "v1.4.262"})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if plan.ContainerName != tc.name {
				t.Errorf("ContainerName = %q, want %q", plan.ContainerName, tc.name)
			}
			if !plan.Detached {
				t.Error("an Orca Shell plan is not detached")
			}
			bindings := plan.PortBindings[network.MustParsePort("6768/tcp")]
			if len(bindings) != 1 || bindings[0].HostIP.String() != "127.0.0.1" || bindings[0].HostPort != "6768" {
				t.Errorf("6768/tcp bindings = %v, want 127.0.0.1:6768", bindings)
			}
			env := indexEnv(plan.Env)
			if got := env["TOOLBOX_ORCA_VERSION"]; got != "v1.4.262" {
				t.Errorf("TOOLBOX_ORCA_VERSION = %q, want v1.4.262", got)
			}
			if got := env["ORCA_USER_DATA_PATH"]; got != "/home/toolbox/.toolbox-state/orca" {
				t.Errorf("ORCA_USER_DATA_PATH = %q, want the orca dir on the state mount", got)
			}
			if want := filepath.Join(plan.StateDir, "orca", "serve.log"); plan.OrcaLogPath() != want {
				t.Errorf("OrcaLogPath = %q, want %q", plan.OrcaLogPath(), want)
			}
		})
	}
}

// TestPlanOrdinaryShellCarriesNoOrca: adopting Orca changes nothing for an
// ordinary session — no Orca env, no Orca port, not detached.
func TestPlanOrdinaryShellCarriesNoOrca(t *testing.T) {
	planHost, workspace := planWorkspace(t)
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Detached {
		t.Error("an ordinary plan is detached")
	}
	for _, e := range plan.Env {
		if strings.HasPrefix(e, "TOOLBOX_ORCA_") || strings.HasPrefix(e, "ORCA_") {
			t.Errorf("ordinary plan carries Orca env %q", e)
		}
	}
	if len(plan.PortBindings) != 0 {
		t.Errorf("ordinary plan publishes %v", plan.PortBindings)
	}
}

// TestPlanOrcaWorkspace: the dir argument wins, orca_workspace (tilde
// expanded against the declared host) fills in, and with neither the error
// names both.
func TestPlanOrcaWorkspace(t *testing.T) {
	planHost, argWorkspace := planWorkspace(t)
	keyWorkspace := planHost.Join("src")
	if err := mkdirAll(t, keyWorkspace); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.OrcaWorkspace = "~/src"

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: cfg, Workspace: argWorkspace, OrcaVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got := indexEnv(plan.Env)["TOOLBOX_HOST_WORKSPACE"]; got != argWorkspace {
		t.Errorf("with a dir argument, workspace = %q, want %q", got, argWorkspace)
	}

	plan, err = sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: cfg, OrcaVersion: "v1.0.0"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got := indexEnv(plan.Env)["TOOLBOX_HOST_WORKSPACE"]; got != keyWorkspace {
		t.Errorf("from orca_workspace, workspace = %q, want %q", got, keyWorkspace)
	}

	_, err = sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), OrcaVersion: "v1.0.0"})
	if err == nil {
		t.Fatal("Plan accepted an Orca Shell with no workspace")
	}
	for _, want := range []string{"orca_workspace", "dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestPlanOrcaRefusesHashShapedProfile: toolbox-orca-<8hex> is the workspace
// format of a directory named "orca", so that profile suffix is refused.
func TestPlanOrcaRefusesHashShapedProfile(t *testing.T) {
	planHost, workspace := planWorkspace(t)
	_, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: workspace,
		Profile: &mountplan.Profile{Name: "deadbeef"}, OrcaVersion: "v1.0.0"})
	if err == nil || !strings.Contains(err.Error(), "deadbeef") {
		t.Fatalf("Plan with a hash-shaped profile = %v, want a refusal naming it", err)
	}
}
