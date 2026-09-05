package mountplan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/proximo"
)

// TestPlanBindsTheProximoCAItsGateResolved is the seam this pipeline reads the
// gate at: Plan mounts what the resolved [proximo.Gate] on its input says, and
// never re-derives the decision from the config. The config here is auto
// (nil), and nothing sits at the host's ~/.proximo fallback — so a plan that
// asked the question again would bind nothing, and the CA that does get bound
// can only have come from the gate.
func TestPlanBindsTheProximoCAItsGateResolved(t *testing.T) {
	home := t.TempDir()
	workspace := filepath.Join(home, "ws")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("setup workspace: %v", err)
	}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatalf("setup CA: %v", err)
	}

	in := PlanInput{
		Host:      fsx.Host{Home: home},
		Cfg:       &config.Config{},
		Workspace: workspace,
		Proximo:   proximo.Gate{Enabled: true, CAPath: caPath, CAExists: true},
	}

	result, err := Plan(in)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	ca, ok := findBind(result.Binds, proximo.CATarget)
	if !ok {
		t.Fatalf("no bind at %q; binds = %v", proximo.CATarget, result.Binds)
	}
	if ca.Source != caPath {
		t.Errorf("proximo CA bind Source = %q, want the gate's path %q", ca.Source, caPath)
	}
	if ca.Mode != "ro" {
		t.Errorf("proximo CA bind Mode = %q, want ro", ca.Mode)
	}

	// The zero gate is a session with proximo off, and the same config must
	// then bind nothing — the decision travels with the input, not with cfg.
	in.Proximo = proximo.Gate{}
	off, err := Plan(in)
	if err != nil {
		t.Fatalf("Plan (gate off): %v", err)
	}
	if b, ok := findBind(off.Binds, proximo.CATarget); ok {
		t.Errorf("proximo CA bound under a zero gate: %+v", b)
	}
}

// TestMergeInjectsProximoCAWhenEnabled asserts the proximo CA bind is appended
// (read-only, sourced from ~/.proximo/tls/ca.pem) when proximo is
// enabled, and absent when force-disabled. The mount is injected in Merge —
// not in defaults() — so the canonical default set (and the smoke-test
// bijection) stays unchanged. Force on/off is used (not auto) so the assertion
// is independent of whether the build host happens to have proximo installed.
func TestMergeInjectsProximoCAWhenEnabled(t *testing.T) {
	// A declared host with no proximo on its PATH: the CA path is the
	// deterministic ~/.proximo fallback, whether or not the build host has
	// proximo installed.
	host := fsx.Host{Home: t.TempDir()}
	wantSource := filepath.Join(host.Home, ".proximo", "tls", "ca.pem")

	cfgOn := &config.Config{Proximo: new(true)}
	merged, err := Merge(host, cfgOn, nil, proximo.Resolve(host, cfgOn))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	ca := findMount(merged, "proximo-ca")
	if ca == nil {
		t.Fatal("proximo-ca mount missing when proximo enabled")
	}
	if ca.Source != wantSource {
		t.Errorf("proximo-ca Source = %q, want %q", ca.Source, wantSource)
	}
	if !ca.ReadOnly {
		t.Error("proximo-ca mount must be read-only")
	}

	cfgOff := &config.Config{Proximo: new(false)}
	off, err := Merge(host, cfgOff, nil, proximo.Resolve(host, cfgOff))
	if err != nil {
		t.Fatalf("Merge (disabled): %v", err)
	}
	if findMount(off, "proximo-ca") != nil {
		t.Error("proximo-ca mount must be absent when proximo disabled")
	}
}
