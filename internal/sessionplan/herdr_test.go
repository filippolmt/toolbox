package sessionplan_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
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
		machine string
	}{
		{"no profile", nil, "toolbox-herdr", "toolbox-herdr-data", "toolbox"},
		{"profile", &mountplan.Profile{Name: "Work"}, "toolbox-herdr-work", "toolbox-herdr-data-work", "toolbox-work"},
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
			if plan.DataVolume.Name != tc.volume || plan.DataVolume.Init != tc.volume+"_init" {
				t.Errorf("DataVolume.Name, .Init = %q, %q; want %q and its _init", plan.DataVolume.Name, plan.DataVolume.Init, tc.volume)
			}
			wantBind := mountplan.Bind{Source: tc.volume, Target: "/home/toolbox/.local/state/herdr-core", Mode: "rw"}
			if !slices.Contains(plan.Binds, wantBind) {
				t.Errorf("Binds = %v, want the state volume bind %v", plan.Binds, wantBind)
			}
			if got := indexEnv(plan.Env)["TOOLBOX_HOST_WORKSPACE"]; got != workspace {
				t.Errorf("workspace = %q, want the dir argument %q", got, workspace)
			}
			// The Host Terminal Relay's return address: hc-host signs a send
			// with the machine the host's hc reaches this Shell as.
			if got := indexEnv(plan.Env)[sessionplan.HerdrMachineEnv]; got != tc.machine {
				t.Errorf("%s = %q, want %q", sessionplan.HerdrMachineEnv, got, tc.machine)
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
	if plan.Detached || plan.DataVolume.Name != "" || plan.DataVolume.Init != "" {
		t.Errorf("ordinary plan: detached %v, herdr-core volume %q, init %q", plan.Detached, plan.DataVolume.Name, plan.DataVolume.Init)
	}
	if v, ok := indexEnv(plan.Env)[sessionplan.HerdrMachineEnv]; ok {
		t.Errorf("ordinary plan sets %s=%q: hc-host would sign sends no reply can reach", sessionplan.HerdrMachineEnv, v)
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

// TestPlanHerdrCoreWorkspace: the dir argument wins, herdr_workspace (tilde
// expanded against the declared host) fills in, and with neither the error
// names both remedies. The running Shell's own workspace reaches the plan as
// the dir argument (cmd: TestHerdrJoinsARunningShellOnItsWorkspace).
func TestPlanHerdrCoreWorkspace(t *testing.T) {
	planHost, argWorkspace := planWorkspace(t)
	keyWorkspace := planHost.Join("src")
	if err := mkdirAll(t, keyWorkspace); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.HerdrWorkspace = "~/src"

	for _, tc := range []struct{ label, dir, want string }{
		{"dir argument", argWorkspace, argWorkspace},
		{"herdr_workspace", "", keyWorkspace},
	} {
		plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: cfg, Workspace: tc.dir, HerdrCore: true})
		if err != nil {
			t.Fatalf("%s: Plan: %v", tc.label, err)
		}
		if got := indexEnv(plan.Env)["TOOLBOX_HOST_WORKSPACE"]; got != tc.want {
			t.Errorf("%s: workspace = %q, want %q", tc.label, got, tc.want)
		}
	}

	_, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), HerdrCore: true})
	if err == nil {
		t.Fatal("Plan accepted a Herdr Core Shell with no workspace")
	}
	for _, want := range []string{"herdr_workspace", "dir argument"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestPlanHerdrCoreWorkspaceRefusals runs every workspace exception through
// both routes to the Herdr Core Shell's workspace, the dir argument and
// herdr_workspace, so the routes share their exceptions.
func TestPlanHerdrCoreWorkspaceRefusals(t *testing.T) {
	planHost, _ := planWorkspace(t)
	file := planHost.Join("file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		colon    = "contains ':'"
		relative = "is not absolute"
		missing  = "no such file or directory"
		notDir   = "is not a directory"
		home     = "is the home directory"
	)
	for _, tc := range []struct{ label, value, dirReason, keyReason string }{
		{"colon", planHost.Join("a:b"), colon, colon},
		{"relative", "src", relative, relative},
		{"missing", planHost.Join("missing"), missing, missing},
		{"not a directory", file, notDir, notDir},
		{"bare tilde", "~", relative, home},
		{"tilde slash", "~/", relative, home},
		{"home", planHost.Home, home, home},
		{"home with trailing slash", planHost.Home + "/", home, home},
	} {
		routes := map[string]struct {
			in     sessionplan.PlanInput
			reason string
		}{
			"dir argument":    {sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: tc.value, HerdrCore: true}, tc.dirReason},
			"herdr_workspace": {sessionplan.PlanInput{Host: planHost, Cfg: &config.Config{Shell: "zsh", HerdrWorkspace: tc.value}, HerdrCore: true}, tc.keyReason},
		}
		for route, r := range routes {
			t.Run(tc.label+"/"+route, func(t *testing.T) {
				_, err := sessionplan.Plan(r.in)
				if err == nil || !strings.Contains(err.Error(), r.reason) || !strings.Contains(err.Error(), "toolbox herdr") {
					t.Errorf("Plan(%q) = %v, want a toolbox herdr refusal containing %q", tc.value, err, r.reason)
				}
			})
		}
	}
}

func TestHerdrMachineNameFollowsTheProfile(t *testing.T) {
	for container, want := range map[string]string{"toolbox-herdr": "toolbox", "toolbox-herdr-work": "toolbox-work"} {
		if got := sessionplan.HerdrMachineName(container); got != want {
			t.Errorf("HerdrMachineName(%q) = %q, want %q", container, got, want)
		}
	}
}
