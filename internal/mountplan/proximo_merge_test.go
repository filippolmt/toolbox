package mountplan

import (
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

func boolPtr(b bool) *bool { return &b }

// TestMergeInjectsProximoCAWhenEnabled asserts the proximo CA bind is appended
// (read-only, sourced from <config-dir>/proximo/tls/ca.pem) when proximo is
// enabled, and absent when force-disabled. The mount is injected in Merge —
// not in defaults() — so the canonical default set (and the smoke-test
// bijection) stays unchanged. Force on/off is used (not auto) so the assertion
// is independent of whether the build host happens to have proximo installed.
func TestMergeInjectsProximoCAWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	wantSource := filepath.Join(dir, "proximo", "tls", "ca.pem")

	merged, err := Merge(&config.Config{Proximo: boolPtr(true)})
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

	off, err := Merge(&config.Config{Proximo: boolPtr(false)})
	if err != nil {
		t.Fatalf("Merge (disabled): %v", err)
	}
	if findMount(off, "proximo-ca") != nil {
		t.Error("proximo-ca mount must be absent when proximo disabled")
	}
}
