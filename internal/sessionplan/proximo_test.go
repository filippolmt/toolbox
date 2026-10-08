package sessionplan_test

import (
	"os"
	"os/exec"
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
// host whose CA is present emits the CA-trust env and binds the CA file — the
// whole chain from config to plan, through the one proximo.Resolve a session
// pays.
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

// TestPlanFollowsTheProximoGateItWasGiven pins the seam the gate crosses: the
// inventory mount, CA bind and trust env come from PlanInput.Proximo, and the
// plan never re-derives the decision from cfg.
//
// The config here is auto (nil) with no CA under the host's home, so a plan
// that asked again would produce none of them; every one present can only have
// come from the gate on the input.
func TestPlanFollowsTheProximoGateItWasGiven(t *testing.T) {
	tmp := t.TempDir()
	planHost := fsx.Host{Home: tmp} // no resolver → no proximo on this host

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	inventoryDir := filepath.Join(t.TempDir(), "inventory")
	if err := os.MkdirAll(inventoryDir, 0o755); err != nil {
		t.Fatalf("write inventory dir: %v", err)
	}

	workspace := filepath.Join(tmp, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Host:      planHost,
		Cfg:       testConfig(),
		Workspace: workspace,
		Proximo: proximo.Gate{
			Enabled: true, CAPath: caPath, CAExists: true,
			InventoryDir: inventoryDir, InventoryExists: true,
		},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if !slices.Contains(plan.Env, "NODE_EXTRA_CA_CERTS="+proximo.CATarget) {
		t.Errorf("plan.Env missing NODE_EXTRA_CA_CERTS, got %v", plan.Env)
	}
	bound := map[string]string{}
	for _, b := range plan.Binds {
		bound[b.Target] = b.Source
	}
	if bound[proximo.CATarget] != caPath {
		t.Errorf("proximo CA bound from %q, want the gate's path %q", bound[proximo.CATarget], caPath)
	}
	if bound[proximo.InventoryTarget] != inventoryDir {
		t.Errorf("proximo inventory bound from %q, want the gate's path %q", bound[proximo.InventoryTarget], inventoryDir)
	}
}

// TestPlanProximoDisabled is the negative: with proximo unset (auto-detect) and
// no proximo CA on the host, the plan carries no CA-trust env or mount. HOME
// points at a CA-less dir so auto-detect is deterministically off regardless
// of whether the test host has proximo installed.
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
	for _, e := range plan.Env {
		if strings.HasPrefix(e, "NODE_EXTRA_CA_CERTS=") {
			t.Errorf("unexpected proximo env on default config: %q", e)
		}
	}
	for _, b := range plan.Binds {
		if b.Target == proximo.CATarget || b.Target == proximo.InventoryTarget {
			t.Errorf("unexpected proximo bind on default config: %v", b)
		}
	}
}

// TestPlanNeverRederivesTheProximoGate is the guardrail the resolved value was
// introduced for: the planners READ the gate the composition root handed them,
// and the pipeline that a shell start drives must not ask the host again.
//
// The other proximo tests here prove the plan FOLLOWS the gate; this one proves
// it does not also re-derive one. They are different failures: a planner that
// re-derived and happened to agree would satisfy the former and still pay the
// `proximo config ca-path` spawn this value exists to stop paying. The host
// counts every lookup of the binary, so any re-derivation inside
// sessionplan.Plan — or inside the mountplan.Plan it drives — is a non-zero
// count no matter what it concludes.
func TestPlanNeverRederivesTheProximoGate(t *testing.T) {
	tmp := t.TempDir()

	var lookups int
	planHost := fsx.Host{Home: tmp}
	planHost.LookPath = func(name string) (string, error) {
		if name == "proximo" {
			lookups++
		}
		return "", exec.ErrNotFound
	}

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
	if lookups != 0 {
		t.Errorf("planning looked the proximo binary up %d times, want 0: the gate is an input, never a derivation", lookups)
	}

	// Guards the guard: a plan that ignored proximo entirely would also report
	// zero lookups, so pin that the gate was in fact consumed.
	if !slices.Contains(plan.Env, "NODE_EXTRA_CA_CERTS="+proximo.CATarget) {
		t.Errorf("plan.Env missing proximo trust entry: %v", plan.Env)
	}
}
