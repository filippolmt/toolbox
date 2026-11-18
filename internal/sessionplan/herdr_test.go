package sessionplan_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// TestPlanHerdrCoreShell pins what makes a plan the Herdr Core Shell: the
// fixed name per Profile, herdr-core's state on the Shell's own named volume
// at herdr-core's state home (its sockets cannot live on a Host Share bind),
// and the detached create.
func TestPlanHerdrCoreShell(t *testing.T) {
	planHost, workspace := planWorkspace(t)

	for _, tc := range []struct {
		label   string
		profile *mountplan.Profile
		name    string
		volume  string
	}{
		{"no profile", nil, "toolbox-herdr", "toolbox-herdr-data"},
		{"profile", &mountplan.Profile{Name: "Work"}, "toolbox-herdr-work", "toolbox-herdr-data-work"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(),
				Workspace: workspace, Profile: tc.profile, HerdrCore: true})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if plan.ContainerName != tc.name {
				t.Errorf("ContainerName = %q, want %q", plan.ContainerName, tc.name)
			}
			if !plan.Detached {
				t.Error("a Herdr Core Shell plan is not detached")
			}
			if plan.HerdrDataVolume != tc.volume || plan.HerdrDataInit != tc.volume+"_init" {
				t.Errorf("HerdrDataVolume, HerdrDataInit = %q, %q; want %q and its _init", plan.HerdrDataVolume, plan.HerdrDataInit, tc.volume)
			}
			wantBind := mountplan.Bind{Source: tc.volume, Target: "/home/toolbox/.local/state/herdr-core", Mode: "rw"}
			if !slices.Contains(plan.Binds, wantBind) {
				t.Errorf("Binds = %v, want the state volume bind %v", plan.Binds, wantBind)
			}
			if got := indexEnv(plan.Env)["TOOLBOX_HOST_WORKSPACE"]; got != workspace {
				t.Errorf("workspace = %q, want the dir argument %q", got, workspace)
			}
		})
	}
}

// TestPlanOrdinaryShellCarriesNoHerdrCore: an ordinary session has no
// herdr-core state volume and is not detached.
func TestPlanOrdinaryShellCarriesNoHerdrCore(t *testing.T) {
	planHost, workspace := planWorkspace(t)
	plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Detached || plan.HerdrDataVolume != "" || plan.HerdrDataInit != "" {
		t.Errorf("ordinary plan: detached %v, herdr-core volume %q, init %q", plan.Detached, plan.HerdrDataVolume, plan.HerdrDataInit)
	}
	for _, b := range plan.Binds {
		if strings.Contains(b.Source, "herdr-core") || strings.Contains(b.Target, "herdr-core") || strings.HasPrefix(b.Source, "toolbox-herdr") {
			t.Errorf("ordinary plan carries herdr-core bind %v", b)
		}
	}
}

// TestPlanHerdrCoreRefusesHashShapedProfile: toolbox-herdr-<8hex> is the
// workspace format of a directory named "herdr", so that suffix is refused.
func TestPlanHerdrCoreRefusesHashShapedProfile(t *testing.T) {
	planHost, workspace := planWorkspace(t)
	_, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: workspace,
		Profile: &mountplan.Profile{Name: "deadbeef"}, HerdrCore: true})
	if err == nil || !strings.Contains(err.Error(), "deadbeef") || !strings.Contains(err.Error(), "Herdr Core Shell") {
		t.Fatalf("Plan with a hash-shaped profile = %v, want a refusal naming it and the Herdr Core Shell", err)
	}
}

// TestPlanHerdrCoreWorkspaceRefusals: the dir argument passes the same check
// as the Orca Shell's, and no dir at all is refused naming it.
func TestPlanHerdrCoreWorkspaceRefusals(t *testing.T) {
	planHost, _ := planWorkspace(t)
	file := planHost.Join("file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ label, value, reason string }{
		{"none", "", "dir argument"},
		{"colon", planHost.Join("a:b"), "contains ':'"},
		{"relative", "src", "is not absolute"},
		{"missing", planHost.Join("missing"), "no such file or directory"},
		{"not a directory", file, "is not a directory"},
		{"home", planHost.Home, "is the home directory"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			_, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: tc.value, HerdrCore: true})
			if err == nil || !strings.Contains(err.Error(), tc.reason) || !strings.Contains(err.Error(), "toolbox herdr") {
				t.Errorf("Plan(%q) = %v, want a toolbox herdr refusal containing %q", tc.value, err, tc.reason)
			}
		})
	}
}
