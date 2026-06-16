package sessionplan_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/proximo"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

func boolPtr(b bool) *bool { return &b }

// TestPlanWiresProximo asserts that proximo: true sets the SessionPlan.Proximo
// flag (the create-edge discovery signal), emits the CA-trust env, and binds
// the CA file when present on the host.
func TestPlanWiresProximo(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PATH", t.TempDir()) // no host proximo → deterministic fallback path

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

	plan, err := sessionplan.Plan(&config.Config{Shell: "zsh", Proximo: boolPtr(true)}, workspace, nil, false, "")
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

// TestPlanProximoDisabled is the negative: with proximo unset (auto-detect) and
// no proximo CA on the host, the plan carries no proximo flag and no CA-trust
// env. HOME points at a CA-less dir so auto-detect is deterministically off
// regardless of whether the test host has proximo installed.
func TestPlanProximoDisabled(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)         // no CA written → auto off
	t.Setenv("PATH", t.TempDir()) // no host proximo → deterministic fallback path
	workspace := filepath.Join(tmp, "ws")
	if err := mkdirAll(t, workspace); err != nil {
		t.Fatalf("setup: %v", err)
	}

	plan, err := sessionplan.Plan(testConfig(), workspace, nil, false, "")
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
