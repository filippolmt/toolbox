package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
)

// Plan-level fs tests live alongside the unexported walkUp tests so HOME
// overrides via t.Setenv stay package-local. Pitfall 3: t.Setenv blocks
// t.Parallel and auto-restores; the non-restoring stdlib variant is
// forbidden, as is any global viper reset (D-09 — Plan owns its own
// *viper.Viper; no global churn).

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

// TestPlanCanonicalPipeline runs the pure-defaults canonical pipeline — no
// global, no project, no override. Asserts every catalog tool comes back
// default-true and Shell defaults to "zsh".
func TestPlanCanonicalPipeline(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir() // empty HOME — no global ~/.toolbox.yaml
	t.Setenv("HOME", home)

	cfg, err := Plan(cwd, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if cfg.Shell != "zsh" {
		t.Errorf("Shell = %q, want zsh (default)", cfg.Shell)
	}
	for _, k := range catalog.Keys() {
		if !cfg.Tools[k] {
			t.Errorf("tool %q should default to true after canonical Plan, got false", k)
		}
	}
	if cfg.MountsRoot != "" {
		t.Errorf("MountsRoot should be empty by default, got %q", cfg.MountsRoot)
	}
}

// TestPlanWalksUpFromSubdir pins CFG-04 invariant via the Plan Seam: a
// project .toolbox.yaml at the workspace root is found from a deep subdir.
// Source: 08-RESEARCH §Code Examples §Example 3.
func TestPlanWalksUpFromSubdir(t *testing.T) {
	workspace := t.TempDir()
	mountsRoot := filepath.Join(workspace, "mounts")
	yaml := "mounts_root: " + mountsRoot + "\n"
	if err := os.WriteFile(filepath.Join(workspace, ".toolbox.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	nested := filepath.Join(workspace, "deep", "nested", "subdir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	emptyHome := t.TempDir()
	t.Setenv("HOME", emptyHome)

	cfg, err := Plan(nested, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if cfg.MountsRoot != mountsRoot {
		t.Errorf("walk-up did not find workspace .toolbox.yaml: got %q, want %q", cfg.MountsRoot, mountsRoot)
	}
}

// TestPlanExplicitOverrideShortCircuits pins CFG-04 invariant 4: --config
// short-circuits both global and project file reads. Source: 08-RESEARCH
// §Code Examples §Example 3.
func TestPlanExplicitOverrideShortCircuits(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(explicit, []byte("tools:\n  gcloud: false\n"), 0o600); err != nil {
		t.Fatalf("write explicit: %v", err)
	}
	// Global at HOME and a project file at CWD that should both be ignored.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte("shell: bash\n"), 0o600); err != nil {
		t.Fatalf("write home: %v", err)
	}
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".toolbox.yaml"), []byte("mounts_root: /should-not-load\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := Plan(cwd, explicit)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if cfg.Tools["gcloud"] {
		t.Error("gcloud should be false from --config")
	}
	if cfg.Shell != "zsh" {
		t.Errorf("Shell = %q, want zsh (global must NOT be read when --config set)", cfg.Shell)
	}
	if cfg.MountsRoot != "" {
		t.Errorf("MountsRoot = %q, want \"\" (project must NOT be read when --config set)", cfg.MountsRoot)
	}
}

// TestPlanRejectsInvalidShell asserts ValidateShell runs inside Plan's tail
// (CFG-05). Migrated semantically from internal/config/config_shell_test.go::
// TestLoadShellInvalid; that test stays in place targeting the deprecated
// Load() wrapper (Plan 05).
func TestPlanRejectsInvalidShell(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "bad-shell.yaml")
	if err := os.WriteFile(explicit, []byte("shell: fish\n"), 0o600); err != nil {
		t.Fatalf("write explicit: %v", err)
	}
	t.Setenv("HOME", t.TempDir())

	_, err := Plan(dir, explicit)
	if err == nil {
		t.Fatal("Plan should reject shell: fish")
	}
	if !strings.Contains(err.Error(), "fish") {
		t.Errorf("error should mention the rejected shell value, got: %v", err)
	}
}

// TestPlanGlobalMalformedIsBestEffort pins the Codex-PR-152 fix: a malformed
// ~/.toolbox.yaml must NOT fail Plan; the broken layer is dropped and Plan
// continues with project + defaults so commands like `stop --all` still
// run. Pre-Plan-08 cmd/root.go::initConfig swallowed every error from
// viper.ReadInConfig — this test pins that contract at the Plan layer.
func TestPlanGlobalMalformedIsBestEffort(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte(":\n  not: yaml\n  -bad\n"), 0o600); err != nil {
		t.Fatalf("write malformed home: %v", err)
	}
	t.Setenv("HOME", home)
	cwd := t.TempDir() // no project file, no walk-up match

	cfg, err := Plan(cwd, "")
	if err != nil {
		t.Fatalf("Plan must not fail on malformed global config: %v", err)
	}
	if cfg.Shell != "zsh" {
		t.Errorf("Shell = %q, want zsh (defaults survive a dropped global)", cfg.Shell)
	}
	for _, k := range catalog.Keys() {
		if !cfg.Tools[k] {
			t.Errorf("tool %q should default to true after global drop, got false", k)
		}
	}
}

// TestPlanGlobalUnreadableIsBestEffort pins the read-error branch of the
// same fix: an unreadable ~/.toolbox.yaml (here, a directory at that
// path) must NOT fail Plan. Using a directory is portable across
// containers running as root where chmod 000 is bypassed.
func TestPlanGlobalUnreadableIsBestEffort(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".toolbox.yaml"), 0o755); err != nil {
		t.Fatalf("mkdir global as dir: %v", err)
	}
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	cfg, err := Plan(cwd, "")
	if err != nil {
		t.Fatalf("Plan must not fail on unreadable global config: %v", err)
	}
	if cfg.Shell != "zsh" {
		t.Errorf("Shell = %q, want zsh", cfg.Shell)
	}
}

// TestPlanRejectsRelativeMountsRoot asserts ValidateMountsRoot runs inside
// Plan's tail (CFG-05).
func TestPlanRejectsRelativeMountsRoot(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "bad-mounts.yaml")
	if err := os.WriteFile(explicit, []byte("mounts_root: ./relative\n"), 0o600); err != nil {
		t.Fatalf("write explicit: %v", err)
	}
	t.Setenv("HOME", t.TempDir())

	_, err := Plan(dir, explicit)
	if err == nil {
		t.Fatal("Plan should reject relative mounts_root")
	}
	if !strings.Contains(err.Error(), "mounts_root") {
		t.Errorf("error should mention mounts_root, got: %v", err)
	}
}
