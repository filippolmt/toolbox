package configui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

// isolatedHome points GlobalConfigPath at a temp dir so global-layer reads and
// writes never touch the real home.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func stateFor(t *testing.T, states []KeyState, key string) KeyState {
	t.Helper()
	for _, s := range states {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("key %q not present in snapshot", key)
	return KeyState{}
}

// TestKeysOmitsDeprecated verifies browser_bridge is never a UI row.
func TestKeysOmitsDeprecated(t *testing.T) {
	if slices.Contains(Keys(), deprecatedKey) {
		t.Errorf("Keys() must not include the deprecated %q key", deprecatedKey)
	}
	if !slices.Contains(Keys(), "bridge") {
		t.Error("Keys() must include bridge")
	}
}

// TestSnapshotProvenance: a value set in the repo file is attributed to the
// project origin and rendered as its effective value.
func TestSnapshotProvenance(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".toolbox.yaml"), "pull: never\n")

	_, states, err := Snapshot(repo, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	pull := stateFor(t, states, "pull")
	if pull.Origin != configedit.OriginProject {
		t.Errorf("pull origin = %v, want OriginProject", pull.Origin)
	}
	if pull.Display != "never" {
		t.Errorf("pull display = %q, want %q", pull.Display, "never")
	}
	if pull.FromEnv {
		t.Error("pull must not be marked FromEnv when a file sets it")
	}
}

// TestSnapshotDefault: an unset key resolves to its built-in default origin.
func TestSnapshotDefault(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()

	_, states, err := Snapshot(repo, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	pull := stateFor(t, states, "pull")
	if pull.Origin != configedit.OriginDefault {
		t.Errorf("pull origin = %v, want OriginDefault", pull.Origin)
	}
	if !strings.Contains(pull.Display, "auto") {
		t.Errorf("pull display = %q, want it to mention the auto default", pull.Display)
	}
}

// TestSnapshotEnv: a TOOLBOX_* override of an env-bound key surfaces as a
// read-only env-sourced key.
func TestSnapshotEnv(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	t.Setenv("TOOLBOX_PULL", "never") // pull is env-bound (config.EnvBoundKeys)

	_, states, err := Snapshot(repo, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	pull := stateFor(t, states, "pull")
	if !pull.FromEnv {
		t.Errorf("pull must be marked FromEnv when TOOLBOX_PULL is set, got %+v", pull)
	}
}

// TestSnapshotEnvIgnoresUnboundKey: a TOOLBOX_* var for a key config does NOT
// env-bind must not falsely mark the key read-only (regression: envSet used to
// over-report every key).
func TestSnapshotEnvIgnoresUnboundKey(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	t.Setenv("TOOLBOX_AGENT", "codex") // agent is NOT env-bound

	_, states, err := Snapshot(repo, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if agent := stateFor(t, states, "agent"); agent.FromEnv {
		t.Errorf("agent must NOT be FromEnv (not in config.EnvBoundKeys), got %+v", agent)
	}
}

// TestSnapshotBrowserBridgeFold: a file that sets only the deprecated
// browser_bridge is surfaced through the bridge control (spec: deprecated key
// handling). false differs from the true default, so it also lands as a repo
// override rather than collapsing into the default.
func TestSnapshotBrowserBridgeFold(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".toolbox.yaml"), "browser_bridge: false\n")

	_, states, err := Snapshot(repo, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	bridge := stateFor(t, states, "bridge")
	if bridge.Display != "false" {
		t.Errorf("browser_bridge value must surface through bridge, got Display=%q", bridge.Display)
	}
	if bridge.Origin != configedit.OriginProject {
		t.Errorf("folded browser_bridge must be attributed to the repo layer, got %v", bridge.Origin)
	}
}

// TestSnapshotShellsOrigin: a per-entry shells override credits the shells
// container row to the repo layer (originFor aggregation).
func TestSnapshotShellsOrigin(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, ".toolbox.yaml"), "shells:\n  infra:\n    path: /repo/infra\n")

	_, states, err := Snapshot(repo, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if shells := stateFor(t, states, "shells"); shells.Origin != configedit.OriginProject {
		t.Errorf("shells row must credit the repo layer via per-entry aggregation, got %v", shells.Origin)
	}
}

// TestTargetPath: global resolves under home; a repo scope with no file resolves
// to ./.toolbox.yaml in the working directory.
func TestTargetPath(t *testing.T) {
	home := isolatedHome(t)
	repo := t.TempDir()

	global, err := TargetPath(ScopeGlobal, repo)
	if err != nil {
		t.Fatalf("TargetPath global: %v", err)
	}
	if filepath.Dir(global) != home {
		t.Errorf("global target = %q, want it under home %q", global, home)
	}

	local, err := TargetPath(ScopeRepo, repo)
	if err != nil {
		t.Fatalf("TargetPath repo: %v", err)
	}
	if local != filepath.Join(repo, ".toolbox.yaml") {
		t.Errorf("repo target with no file = %q, want %q", local, filepath.Join(repo, ".toolbox.yaml"))
	}
}

// apply is the save path the model takes, with ApplyChecked's changed flag
// dropped — the TUI has no per-file report line to print, so its tests only ever
// care whether the write was accepted.
func apply(target, cwd string, mutate configedit.Mutator) error {
	_, err := configedit.ApplyChecked(target, cwd, mutate)
	return err
}

// TestApplyRemoveDropsOnlyTheNamedKey: the reset-to-default path leaves the rest
// of the file standing, and the removal still has to clear the Doctor gate — a
// key whose absence makes the file invalid must not be removable.
func TestApplyRemoveDropsOnlyTheNamedKey(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	writeFile(t, target, "pull: always\nagent: codex\n")

	if err := apply(target, repo, configedit.Remove("pull")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := readFile(t, target)
	if strings.Contains(got, "pull:") {
		t.Errorf("pull key must be gone:\n%s", got)
	}
	if !strings.Contains(got, "agent: codex") {
		t.Errorf("sibling key must survive:\n%s", got)
	}
}

// TestHostAuthOptions: the option set comes from the catalog, sorted, and
// includes a known eligible CLI.
func TestHostAuthOptions(t *testing.T) {
	opts := HostAuthOptions()
	if len(opts) == 0 {
		t.Fatal("expected a non-empty host-auth option set")
	}
	if !slices.Contains(opts, "claude") {
		t.Errorf("expected claude among host-auth options, got %v", opts)
	}
	if !slices.IsSorted(opts) {
		t.Errorf("host-auth options must be sorted, got %v", opts)
	}
}

// TestRowsRoundTrip: the row helpers are inverse conversions.
func TestRowsRoundTrip(t *testing.T) {
	pairs := map[string]string{"A": "1", "B": "2"}
	if got := rowsToPairs(pairsToRows(pairs)); len(got) != 2 || got["A"] != "1" || got["B"] != "2" {
		t.Errorf("pair round-trip failed: %v", got)
	}
	vals := []string{"x", "y"}
	if got := rowsToValues(valuesToRows(vals)); !slices.Equal(got, vals) {
		t.Errorf("value round-trip failed: %v", got)
	}
}

// TestSaveSDD: enabling writes the bool shorthand and the .gitignore fence;
// disabling removes both, converging the TUI path with `toolbox sdd init`.
func TestSaveSDD(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	gitignore := filepath.Join(repo, ".gitignore")
	const skill = "gsd" // a skill that declares gitignore entries

	if err := SaveSDD(target, repo, map[string]bool{skill: true}); err != nil {
		t.Fatalf("SaveSDD enable: %v", err)
	}
	if got := readFile(t, target); !strings.Contains(got, skill+": true") {
		t.Errorf("want %s: true, got:\n%s", skill, got)
	}
	if got := readFile(t, gitignore); !strings.Contains(got, configedit.GitignoreFenceStart(skill)) {
		t.Errorf("enable must write the gitignore fence:\n%s", got)
	}

	if err := SaveSDD(target, repo, nil); err != nil {
		t.Fatalf("SaveSDD disable: %v", err)
	}
	if got := readFile(t, target); strings.Contains(got, "sdd:") {
		t.Errorf("empty selection must remove the sdd block:\n%s", got)
	}
	if got := readFile(t, gitignore); strings.Contains(got, configedit.GitignoreFenceStart(skill)) {
		t.Errorf("disable must remove the gitignore fence:\n%s", got)
	}
}

// TestSaveSDDBlockedYamlSkipsFence: a reconcile the doctor rejects leaves the
// yaml at its pre-edit bytes and writes no .gitignore fence. The fence loop runs
// only after the yaml commits, so a rejected reconcile can never half-apply.
func TestSaveSDDBlockedYamlSkipsFence(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	before := "shell: bash\n" // bash is unsupported — Doctor rejects it
	writeFile(t, target, before)

	if err := SaveSDD(target, repo, map[string]bool{"gsd": true}); err == nil {
		t.Fatal("expected SaveSDD to be blocked by Doctor")
	}
	if got := readFile(t, target); got != before {
		t.Errorf("yaml must be left at its pre-edit bytes, got:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("a blocked reconcile must not write a fence (stat err=%v)", err)
	}
}

func TestSaveSDDPreservesCustomSteps(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	skill := SDDOptions()[0]
	writeFile(t, target, "sdd:\n  "+skill+":\n    steps:\n      - [\"--claude\"]\n")

	if err := SaveSDD(target, repo, map[string]bool{skill: true}); err != nil {
		t.Fatalf("SaveSDD: %v", err)
	}
	if got := readFile(t, target); !strings.Contains(got, "steps:") {
		t.Errorf("custom steps must survive a re-enable:\n%s", got)
	}
}

// TestScopeStatesPerFile: a file's own view reports only the keys that file
// sets — the data behind the per-scope "in <scope>" line. A key set in one
// layer's file is absent from the other layer's ScopeStates.
func TestScopeStatesPerFile(t *testing.T) {
	repo := t.TempDir()
	globalPath := filepath.Join(repo, "global.yaml")
	repoPath := filepath.Join(repo, ".toolbox.yaml")
	writeFile(t, globalPath, "pull: never\n")
	writeFile(t, repoPath, "agent: codex\n")

	global, err := ScopeStates(globalPath)
	if err != nil {
		t.Fatalf("ScopeStates global: %v", err)
	}
	if !global["pull"].set || global["pull"].display != "never" {
		t.Errorf("global scope must set pull=never, got %+v", global["pull"])
	}
	if global["agent"].set {
		t.Errorf("global scope must NOT set agent, got %+v", global["agent"])
	}

	rep, err := ScopeStates(repoPath)
	if err != nil {
		t.Fatalf("ScopeStates repo: %v", err)
	}
	if !rep["agent"].set || rep["agent"].display != "codex" {
		t.Errorf("repo scope must set agent=codex, got %+v", rep["agent"])
	}
	if rep["pull"].set {
		t.Errorf("repo scope must NOT set pull, got %+v", rep["pull"])
	}
}

// TestScopeStatesMissingFile: a scope whose file does not exist reports every
// key as unset (all inherited), never an error.
func TestScopeStatesMissingFile(t *testing.T) {
	got, err := ScopeStates(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("ScopeStates missing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a missing file must yield an all-unset map, got %+v", got)
	}
}

// TestScopeStatesCollectionCount: a collection key's per-scope display counts
// the entries present in that file.
func TestScopeStatesCollectionCount(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".toolbox.yaml")
	writeFile(t, path, "env:\n  FOO: bar\n  BAZ: qux\n")

	got, err := ScopeStates(path)
	if err != nil {
		t.Fatalf("ScopeStates: %v", err)
	}
	if got["env"].display != "2 vars" {
		t.Errorf("env per-scope display = %q, want %q", got["env"].display, "2 vars")
	}
}

// TestScopeStatesBrowserBridgeFold: a file that sets only the deprecated
// browser_bridge counts as setting bridge in that scope.
func TestScopeStatesBrowserBridgeFold(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, ".toolbox.yaml")
	writeFile(t, path, "browser_bridge: false\n")

	got, err := ScopeStates(path)
	if err != nil {
		t.Fatalf("ScopeStates: %v", err)
	}
	if !got["bridge"].set {
		t.Errorf("browser_bridge in a file must count as bridge being set in that scope, got %+v", got["bridge"])
	}
}

// TestDisplayValueHintNotDoubled: keys with no default value to echo render a
// single bare hint, not the doubled "(hint) (default)" the old orDefault made.
func TestDisplayValueHintNotDoubled(t *testing.T) {
	empty := &config.Config{}
	for key, want := range map[string]string{
		"image":           "(default)",
		"registry_mirror": "(none)",
		"mounts_root":     "(~/.toolbox)",
	} {
		if got := displayValue(empty, key); got != want {
			t.Errorf("displayValue(%q) = %q, want %q", key, got, want)
		}
	}
	// pull (and the other enum/scalar keys via orDefault) now echoes the bare
	// default value — the detail pane carries a dedicated "default:" line, so the
	// old "<value> (default)" suffix is gone.
	if got := displayValue(empty, "pull"); got != "auto" {
		t.Errorf("displayValue(pull) = %q, want %q", got, "auto")
	}
}

// TestEnumOptions: bounded enums resolve to their canonical value sets.
func TestEnumOptions(t *testing.T) {
	if got := EnumOptions("pull"); !slices.Equal(got, []string{"auto", "always", "never"}) {
		t.Errorf("pull enum = %v", got)
	}
	if got := EnumOptions("image"); got != nil {
		t.Errorf("non-enum key must return nil, got %v", got)
	}
}

// TestReadOnlyAndEnumDefault: a single-option enum (shell) is read-only, while
// multi-option enums are editable and expose their unset-resolution default.
func TestReadOnlyAndEnumDefault(t *testing.T) {
	if !ReadOnlyKey("shell") {
		t.Error("shell has one supported value — want ReadOnlyKey true")
	}
	for _, k := range []string{"agent", "pull", "image"} {
		if ReadOnlyKey(k) {
			t.Errorf("%s must be editable — want ReadOnlyKey false", k)
		}
	}
	if got := EnumDefault("agent"); got != config.DefaultAgent {
		t.Errorf("EnumDefault(agent) = %q, want %q", got, config.DefaultAgent)
	}
	if got := EnumDefault("pull"); got != config.PullAuto {
		t.Errorf("EnumDefault(pull) = %q, want %q", got, config.PullAuto)
	}
	if got := EnumDefault("image"); got != "" {
		t.Errorf("EnumDefault(non-enum) = %q, want empty", got)
	}
}

// TestSnapshotPopulatesDoc: every key carries a description and an explicit
// default from config.KeyDocs, and single-option keys are flagged read-only.
func TestSnapshotPopulatesDoc(t *testing.T) {
	isolatedHome(t)
	_, states, err := Snapshot(t.TempDir(), "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	shell := stateFor(t, states, "shell")
	if shell.Description == "" || shell.Default == "" {
		t.Errorf("shell missing doc: desc=%q default=%q", shell.Description, shell.Default)
	}
	if !shell.ReadOnly {
		t.Error("shell state must be ReadOnly")
	}
	if agent := stateFor(t, states, "agent"); agent.ReadOnly {
		t.Error("agent state must not be ReadOnly")
	}
}

// TestOptionTags: the editor annotates current and default distinctly, and both
// when they coincide.
func TestOptionTags(t *testing.T) {
	cases := []struct{ opt, cur, def, want string }{
		{"auto", "auto", "auto", " (current · default)"},
		{"always", "always", "auto", " (current)"},
		{"auto", "always", "auto", " (default)"},
		{"never", "always", "auto", ""},
	}
	for _, c := range cases {
		if got := optionTags(c.opt, c.cur, c.def); got != c.want {
			t.Errorf("optionTags(%q,%q,%q) = %q, want %q", c.opt, c.cur, c.def, got, c.want)
		}
	}
}
