package mountplan

import (
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
)

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

	merged, err := Merge(host, &config.Config{Proximo: new(true)}, nil)
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

	off, err := Merge(host, &config.Config{Proximo: new(false)}, nil)
	if err != nil {
		t.Fatalf("Merge (disabled): %v", err)
	}
	if findMount(off, "proximo-ca") != nil {
		t.Error("proximo-ca mount must be absent when proximo disabled")
	}
}
