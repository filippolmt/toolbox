package config

import (
	"os"
	"path/filepath"
	"testing"
)

// During Plan 01 the Plan body is still a stub (not yet implemented).
// These tests pin walk-up behaviour through the unexported walkUp helper
// (legal because the test lives in package config). Plan 02 swaps the
// assertions over to the real *Config returned by Plan; until then we
// guarantee CFG-04 invariants here.
//
// All tests use t.Setenv to override HOME (Pitfall 3 — auto-restoring;
// blocks t.Parallel). The non-restoring stdlib variant is forbidden, as is
// any global viper reset (D-09 — Plan owns its own *viper.Viper; no global
// churn).

// TestWalkUpStopsAtHome pins invariant 1 from RESEARCH §Walk-Up Termination
// Semantics: a CWD inside HOME with a project file at HOME itself must not
// be discovered, because ~/.toolbox.yaml is the global config and is
// handled separately by the read pipeline.
func TestWalkUpStopsAtHome(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte("# global\n"), 0o600); err != nil {
		t.Fatalf("write home config: %v", err)
	}
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	t.Setenv("HOME", home)

	if got := walkUp(work); got != "" {
		t.Errorf("walkUp inside HOME must stop at HOME, got %q", got)
	}
}

// TestWalkUpReturnsClosestMatch pins invariant 3: when ancestors contain a
// .toolbox.yaml at multiple levels, the closest one wins. This makes the
// behaviour explicit (it was implicit before Plan 01 — only covered by the
// happy path).
func TestWalkUpReturnsClosestMatch(t *testing.T) {
	workspace := t.TempDir()
	outer := filepath.Join(workspace, "outer")
	inner := filepath.Join(outer, "inner")
	sub := filepath.Join(inner, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	outerYaml := filepath.Join(outer, ".toolbox.yaml")
	innerYaml := filepath.Join(inner, ".toolbox.yaml")
	if err := os.WriteFile(outerYaml, []byte("# outer\n"), 0o600); err != nil {
		t.Fatalf("write outer: %v", err)
	}
	if err := os.WriteFile(innerYaml, []byte("# inner\n"), 0o600); err != nil {
		t.Fatalf("write inner: %v", err)
	}
	// Empty HOME so the home-stop short-circuit never fires on the walk-up
	// path between sub and the filesystem root.
	t.Setenv("HOME", t.TempDir())

	if got := walkUp(sub); got != innerYaml {
		t.Errorf("walkUp must return the closest match: got %q, want %q", got, innerYaml)
	}
}

// TestWalkUpStopsAtFilesystemRoot pins invariant 2: when no .toolbox.yaml
// exists anywhere up to the root, the walk terminates via the parent == cur
// short-circuit instead of looping forever. The implicit guard becomes
// explicit here.
func TestWalkUpStopsAtFilesystemRoot(t *testing.T) {
	workspace := t.TempDir()
	// HOME is not an ancestor of workspace, so the home-stop branch never
	// fires; the only termination path is parent == cur at the filesystem
	// root.
	t.Setenv("HOME", filepath.Join(t.TempDir(), "different-home"))

	if got := walkUp(workspace); got != "" {
		t.Errorf("walkUp without any project config must return \"\", got %q", got)
	}
}

// TestWalkUpHomeUnsetContinuesToRoot pins Pitfall 5: when HOME is unset (or
// os.UserHomeDir() returns ""), walk-up must still terminate at the
// filesystem root and must still find a planted .toolbox.yaml mid-tree.
func TestWalkUpHomeUnsetContinuesToRoot(t *testing.T) {
	workspace := t.TempDir()
	planted := filepath.Join(workspace, ".toolbox.yaml")
	if err := os.WriteFile(planted, []byte("# planted\n"), 0o600); err != nil {
		t.Fatalf("write planted: %v", err)
	}
	deep := filepath.Join(workspace, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir deep: %v", err)
	}
	// Explicitly empty HOME — what os.UserHomeDir() can return on a
	// misconfigured system. Must not panic; must not block the walk.
	t.Setenv("HOME", "")

	if got := walkUp(deep); got != planted {
		t.Errorf("walkUp with empty HOME must still find planted file: got %q, want %q", got, planted)
	}
}

// TestWalkUpIgnoresDirectoryNamedToolboxYaml pins the !info.IsDir() guard:
// if a directory happens to be named .toolbox.yaml, walk-up must skip it
// instead of returning its path (which would later fail on os.ReadFile).
func TestWalkUpIgnoresDirectoryNamedToolboxYaml(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".toolbox.yaml"), 0o755); err != nil {
		t.Fatalf("mkdir .toolbox.yaml: %v", err)
	}
	// Put HOME outside the walk path so the only termination is root.
	t.Setenv("HOME", filepath.Join(t.TempDir(), "different-home"))

	if got := walkUp(workspace); got != "" {
		t.Errorf("walkUp must skip a directory named .toolbox.yaml, got %q", got)
	}
}

// Bridging stubs — Plan 02 fills these in once Plan body is wired.

func TestPlanExplicitOverrideShortCircuits(t *testing.T) {
	t.Skip("Plan 02: Plan body must be wired before this test can assert *Config")
}

func TestPlanWalksUpFromSubdir(t *testing.T) {
	t.Skip("Plan 02: same as above — bridging stub during Plan 01")
}

func TestPlanCanonicalPipeline(t *testing.T) {
	t.Skip("Plan 02: defaults + validation must land before canonical pipeline asserts")
}
