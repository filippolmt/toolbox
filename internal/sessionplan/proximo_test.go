package sessionplan_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/proximo"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// TestPlanWiresProximo asserts that a gate resolved from proximo: true on a
// host whose CA is present sets the SessionPlan.Proximo flag (the create-edge
// discovery signal), emits the CA-trust env, and binds the CA file — the whole
// chain from config to plan, through the one proximo.Resolve a session pays.
func TestPlanWiresProximo(t *testing.T) {
	tmp := t.TempDir()
	planHost := fsx.Host{Home: tmp} // no resolver → no proximo on this host

	caPath := filepath.Join(tmp, ".proximo", "tls", "ca.pem")
	if err := os.MkdirAll(filepath.Dir(caPath), 0o700); err != nil {
		t.Fatalf("mkdir CA: %v", err)
	}
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	workspace := filepath.Join(tmp, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := &config.Config{Shell: "zsh", Proximo: new(true)}
	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Host:      planHost,
		Cfg:       cfg,
		Workspace: workspace,
		Proximo:   proximo.Resolve(planHost, cfg),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if !plan.Proximo {
		t.Error("plan.Proximo = false, want true")
	}
	if !slices.Contains(plan.Env, "NODE_EXTRA_CA_CERTS="+proximo.CATarget) {
		t.Errorf("plan.Env missing NODE_EXTRA_CA_CERTS, got %v", plan.Env)
	}

	var caBound bool
	for _, b := range plan.Binds {
		if b.Target == proximo.CATarget {
			caBound = true
			if b.Mode != "ro" {
				t.Errorf("proximo CA bind mode = %q, want ro", b.Mode)
			}
		}
	}
	if !caBound {
		t.Errorf("proximo CA not bound at %q; binds = %v", proximo.CATarget, plan.Binds)
	}
}

// TestPlanFollowsTheProximoGateItWasGiven pins the seam the gate now crosses:
// all three of the session's proximo-shaped outputs — the create-edge
// discovery flag, the CA bind and the trust env — come from
// PlanInput.Proximo, and the plan never re-derives the decision from cfg.
//
// The config here is auto (nil) with no CA under the host's home, so a plan
// that asked again would produce none of the three; every one of them present
// can only have come from the gate on the input.
func TestPlanFollowsTheProximoGateItWasGiven(t *testing.T) {
	tmp := t.TempDir()
	planHost := fsx.Host{Home: tmp} // no resolver → no proximo on this host

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	workspace := filepath.Join(tmp, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Host:      planHost,
		Cfg:       testConfig(),
		Workspace: workspace,
		Proximo:   proximo.Gate{Enabled: true, CAPath: caPath, CAExists: true},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if !plan.Proximo {
		t.Error("plan.Proximo = false, want the gate's decision")
	}
	if !slices.Contains(plan.Env, "NODE_EXTRA_CA_CERTS="+proximo.CATarget) {
		t.Errorf("plan.Env missing NODE_EXTRA_CA_CERTS, got %v", plan.Env)
	}
	var bound string
	for _, b := range plan.Binds {
		if b.Target == proximo.CATarget {
			bound = b.Source
		}
	}
	if bound != caPath {
		t.Errorf("proximo CA bound from %q, want the gate's path %q", bound, caPath)
	}
}

// TestPlanProximoDisabled is the negative: with proximo unset (auto-detect) and
// no proximo CA on the host, the plan carries no proximo flag and no CA-trust
// env. HOME points at a CA-less dir so auto-detect is deterministically off
// regardless of whether the test host has proximo installed.
func TestPlanProximoDisabled(t *testing.T) {
	tmp := t.TempDir()              // no CA written → auto off
	planHost := fsx.Host{Home: tmp} // no resolver → no proximo on this host
	workspace := filepath.Join(tmp, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Host:      planHost,
		Cfg:       cfg,
		Workspace: workspace,
		Proximo:   proximo.Resolve(planHost, cfg),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Proximo {
		t.Error("plan.Proximo = true for default config, want false")
	}
	for _, e := range plan.Env {
		if strings.HasPrefix(e, "NODE_EXTRA_CA_CERTS=") {
			t.Errorf("unexpected proximo env on default config: %q", e)
		}
	}
}
