package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
	if len(cfg.InheritHostAuth) != 0 {
		t.Errorf("InheritHostAuth = %v, want empty (isolated default)", cfg.InheritHostAuth)
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
	if err := os.WriteFile(explicit, []byte("inherit_host_auth: [gh]\n"), 0o600); err != nil {
		t.Fatalf("write explicit: %v", err)
	}
	// Global at HOME and a project file at CWD that should both be ignored.
	// The global uses `inherit_host_auth: [glab]` as the sentinel — distinct
	// from the explicit file's `[gh]` so the assertion below catches the
	// regression where global leaks in via the merge path.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte("inherit_host_auth: [glab]\n"), 0o600); err != nil {
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
	if len(cfg.InheritHostAuth) != 1 || cfg.InheritHostAuth[0] != "gh" {
		t.Errorf("InheritHostAuth = %v, want [gh] from --config", cfg.InheritHostAuth)
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

// TestLoadLayersNoProject pins the no-project shape: only the global buffer
// is populated and projectPath stays empty.
func TestLoadLayersNoProject(t *testing.T) {
	home := t.TempDir()
	globalYAML := "inherit_host_auth: [gh]\n"
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte(globalYAML), 0o600); err != nil {
		t.Fatalf("write home config: %v", err)
	}
	t.Setenv("HOME", home)
	cwd := t.TempDir()

	global, project, explicit, projectPath, err := LoadLayers(cwd, "")
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if string(global) != globalYAML {
		t.Errorf("global = %q, want %q", global, globalYAML)
	}
	if project != nil || projectPath != "" {
		t.Errorf("project = %q (path %q), want nil/empty", project, projectPath)
	}
	if explicit != nil {
		t.Errorf("explicit = %q, want nil", explicit)
	}
}

// TestLoadLayersWalkedUpProject pins that a walked-up project file populates
// both the buffer and the discovered path.
func TestLoadLayersWalkedUpProject(t *testing.T) {
	workspace := t.TempDir()
	projectYAML := "mounts_root: /tmp/x\n"
	wantPath := filepath.Join(workspace, ".toolbox.yaml")
	if err := os.WriteFile(wantPath, []byte(projectYAML), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	nested := filepath.Join(workspace, "deep", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	t.Setenv("HOME", t.TempDir())

	_, project, _, projectPath, err := LoadLayers(nested, "")
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if string(project) != projectYAML {
		t.Errorf("project = %q, want %q", project, projectYAML)
	}
	if projectPath != wantPath {
		t.Errorf("projectPath = %q, want %q", projectPath, wantPath)
	}
}

// TestLoadLayersExplicitShortCircuits pins the --config short-circuit: only
// the explicit buffer is populated even when global + project files exist.
func TestLoadLayersExplicitShortCircuits(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte("shell: zsh\n"), 0o600); err != nil {
		t.Fatalf("write home: %v", err)
	}
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".toolbox.yaml"), []byte("mounts_root: /tmp/x\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	explicitYAML := "inherit_host_auth: [gh]\n"
	explicitPath := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(explicitPath, []byte(explicitYAML), 0o600); err != nil {
		t.Fatalf("write explicit: %v", err)
	}
	t.Setenv("HOME", home)

	global, project, explicit, projectPath, err := LoadLayers(cwd, explicitPath)
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if global != nil || project != nil || projectPath != "" {
		t.Errorf("explicit override must short-circuit: global=%q project=%q path=%q", global, project, projectPath)
	}
	if string(explicit) != explicitYAML {
		t.Errorf("explicit = %q, want %q", explicit, explicitYAML)
	}
}

// TestPlanEqualsMergeOverLoadLayers is the regression guard for the
// LoadLayers extraction: Plan must behave identically to composing Merge
// over LoadLayers for a layered global + project setup.
func TestPlanEqualsMergeOverLoadLayers(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte("inherit_host_auth: [gh]\n"), 0o600); err != nil {
		t.Fatalf("write home: %v", err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".toolbox.yaml"), []byte("mounts_root: /tmp/x\nshells:\n  infra:\n    path: /tmp/infra\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	t.Setenv("HOME", home)

	fromPlan, err := Plan(workspace, "")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	global, project, explicit, _, err := LoadLayers(workspace, "")
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	fromCompose, err := Merge(global, project, explicit)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !reflect.DeepEqual(fromPlan, fromCompose) {
		t.Errorf("Plan != Merge(LoadLayers(...)):\n plan:    %+v\n compose: %+v", fromPlan, fromCompose)
	}
}

// TestWalkUpProjectConfigDelegates pins the exported delegator against the
// unexported walkUp on the same tree.
func TestWalkUpProjectConfigDelegates(t *testing.T) {
	workspace := t.TempDir()
	want := filepath.Join(workspace, ".toolbox.yaml")
	if err := os.WriteFile(want, []byte("# project\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	nested := filepath.Join(workspace, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	t.Setenv("HOME", t.TempDir())

	if got := WalkUpProjectConfig(nested); got != want {
		t.Errorf("WalkUpProjectConfig = %q, want %q", got, want)
	}
}

func TestPlan_BridgeDefaultsTrue(t *testing.T) {
	cfg, err := Merge(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bridge == nil {
		t.Fatal("Bridge nil, want *true")
	}
	if !*cfg.Bridge {
		t.Errorf("Bridge = false, want true")
	}
}

func TestPlan_BridgeExplicitFalse(t *testing.T) {
	yaml := []byte("bridge: false\n")
	cfg, err := Merge(nil, yaml, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bridge == nil {
		t.Fatal("Bridge nil, want *false")
	}
	if *cfg.Bridge {
		t.Errorf("Bridge = true, want false")
	}
}

func TestPlan_BridgeLegacyKeyFallsBack(t *testing.T) {
	yaml := []byte("browser_bridge: false\n")
	cfg, err := Merge(nil, yaml, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bridge == nil {
		t.Fatal("Bridge nil, want *false from legacy browser_bridge key")
	}
	if *cfg.Bridge {
		t.Errorf("Bridge = true, want false from legacy browser_bridge key")
	}
}

func TestPlan_BridgeEnvOverride(t *testing.T) {
	t.Setenv("TOOLBOX_BRIDGE", "false")
	cfg, err := Merge(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bridge == nil {
		t.Fatal("Bridge nil, want *false from TOOLBOX_BRIDGE")
	}
	if *cfg.Bridge {
		t.Errorf("Bridge = true, want false from TOOLBOX_BRIDGE")
	}
}

func TestPlan_BridgeNewKeyWinsOverLegacy(t *testing.T) {
	yaml := []byte("bridge: true\nbrowser_bridge: false\n")
	cfg, err := Merge(nil, yaml, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bridge == nil {
		t.Fatal("Bridge nil, want *true")
	}
	if !*cfg.Bridge {
		t.Errorf("Bridge = false, want true — explicit bridge: must outrank legacy browser_bridge:")
	}
}

// TestDeprecatedAliasesAreFoldedByTheLoadPath: the alias table is only worth
// reading if every pair it declares is the fold Merge actually performs. A file
// setting nothing but the alias must leave its live key set after the load.
func TestDeprecatedAliasesAreFoldedByTheLoadPath(t *testing.T) {
	for alias, live := range DeprecatedAliases() {
		cfg, err := Merge(nil, []byte(alias+": false\n"), nil)
		if err != nil {
			t.Fatalf("Merge(%s): %v", alias, err)
		}
		field, ok := fieldByTag(cfg, live)
		if !ok {
			t.Fatalf("alias %q names live key %q, which is not a schema key", alias, live)
		}
		if field.IsZero() {
			t.Errorf("a file setting only %q left %q unset — the fold is gone", alias, live)
		}
	}
}

// fieldByTag returns the Config field carrying the given mapstructure tag.
func fieldByTag(cfg *Config, tag string) (reflect.Value, bool) {
	v := reflect.ValueOf(*cfg)
	for f := range reflect.TypeFor[Config]().Fields() {
		if f.Tag.Get("mapstructure") == tag {
			return v.FieldByName(f.Name), true
		}
	}
	return reflect.Value{}, false
}

// TestEnvBoundKeysAreDocumented pins docs/configuration.md's env-var list to
// EnvBoundKeys. That doc is where a user goes to learn which TOOLBOX_* vars
// work, so it has to name the keys rather than point at a Go symbol — and a
// list written out by hand is what let it sit at four keys after peer_messaging
// joined, answering the question wrong for everyone who read it (#910).
func TestEnvBoundKeysAreDocumented(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration.md"))
	if err != nil {
		t.Fatalf("read docs/configuration.md: %v", err)
	}
	doc := string(raw)

	var line string
	for _, l := range strings.Split(doc, "\n") {
		if strings.Contains(l, "`TOOLBOX_*` environment variables") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("docs/configuration.md: no `TOOLBOX_*` environment variables line found — " +
			"if the load-order list moved, point this test at its new home")
	}
	for _, key := range EnvBoundKeys {
		if !strings.Contains(line, "`"+key+"`") {
			t.Errorf("docs/configuration.md does not list env-bound key %q: %s", key, line)
		}
	}
}
