package configui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

// TestSnapshotEnv: a TOOLBOX_* override surfaces as a read-only env-sourced key.
func TestSnapshotEnv(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	t.Setenv("TOOLBOX_AGENT", "codex")

	_, states, err := Snapshot(repo, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	agent := stateFor(t, states, "agent")
	if !agent.FromEnv {
		t.Errorf("agent must be marked FromEnv when TOOLBOX_AGENT is set, got %+v", agent)
	}
}

// TestSaveScalarCommentPreserved: a save updates only the edited key and keeps
// comments and untouched keys.
func TestSaveScalarCommentPreserved(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	writeFile(t, target, "# keep me\npull: always\n")

	if err := SaveScalar(target, repo, "agent", "codex"); err != nil {
		t.Fatalf("SaveScalar: %v", err)
	}
	got := readFile(t, target)
	if !strings.Contains(got, "# keep me") {
		t.Errorf("comment lost:\n%s", got)
	}
	if !strings.Contains(got, "pull: always") {
		t.Errorf("untouched key lost:\n%s", got)
	}
	if !strings.Contains(got, "agent: codex") {
		t.Errorf("edited key missing:\n%s", got)
	}
}

// TestSaveScalarDoctorBlocked: an invalid edit is rejected and the file is left
// byte-identical (no partial write survives).
func TestSaveScalarDoctorBlocked(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	before := "pull: always\n"
	writeFile(t, target, before)

	err := SaveScalar(target, repo, "shell", "bash") // bash is unsupported
	if err == nil {
		t.Fatal("expected SaveScalar to reject shell: bash")
	}
	if got := readFile(t, target); got != before {
		t.Errorf("file must be unchanged after a blocked save, got:\n%s", got)
	}
}

// TestSaveScalarBlockedOnNewFileRemovesIt: a blocked save that would have
// created the file leaves nothing behind.
func TestSaveScalarBlockedOnNewFileRemovesIt(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")

	if err := SaveScalar(target, repo, "shell", "bash"); err == nil {
		t.Fatal("expected rejection")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("blocked save must not leave a created file behind (stat err=%v)", err)
	}
}

// TestUnsetRemovesKey: unset deletes the key from the file.
func TestUnsetRemovesKey(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	writeFile(t, target, "pull: always\nagent: codex\n")

	if err := Unset(target, repo, "pull"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	got := readFile(t, target)
	if strings.Contains(got, "pull:") {
		t.Errorf("pull key must be gone:\n%s", got)
	}
	if !strings.Contains(got, "agent: codex") {
		t.Errorf("sibling key must survive:\n%s", got)
	}
}

// TestSaveBoolTriState: explicit false persists; nil removes the key.
func TestSaveBoolTriState(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")

	no := false
	if err := SaveBool(target, repo, "bridge", &no); err != nil {
		t.Fatalf("SaveBool false: %v", err)
	}
	if got := readFile(t, target); !strings.Contains(got, "bridge: false") {
		t.Errorf("want bridge: false, got:\n%s", got)
	}

	if err := SaveBool(target, repo, "bridge", nil); err != nil {
		t.Fatalf("SaveBool nil: %v", err)
	}
	if got := readFile(t, target); strings.Contains(got, "bridge:") {
		t.Errorf("unset must remove the key, got:\n%s", got)
	}
}

// TestSaveStringList: writes a sequence; empty removes the key.
func TestSaveStringList(t *testing.T) {
	home := isolatedHome(t)
	// inherit_host_auth entries are doctor-validated against their host
	// credential paths, so materialise the ones this test enables.
	mkdirAll(t, filepath.Join(home, ".claude"))
	mkdirAll(t, filepath.Join(home, ".config", "gh"))
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")

	if err := SaveStringList(target, repo, "inherit_host_auth", []string{"claude", "gh"}); err != nil {
		t.Fatalf("SaveStringList: %v", err)
	}
	got := readFile(t, target)
	if !strings.Contains(got, "claude") || !strings.Contains(got, "gh") {
		t.Errorf("list entries missing:\n%s", got)
	}

	if err := SaveStringList(target, repo, "inherit_host_auth", nil); err != nil {
		t.Fatalf("SaveStringList empty: %v", err)
	}
	if got := readFile(t, target); strings.Contains(got, "inherit_host_auth") {
		t.Errorf("empty list must remove the key:\n%s", got)
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

// TestSaveMap: env pairs are written (sorted) and an empty map removes the key.
func TestSaveMap(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")

	if err := SaveMap(target, repo, "env", map[string]string{"FOO": "bar", "BAZ": "qux"}); err != nil {
		t.Fatalf("SaveMap: %v", err)
	}
	got := readFile(t, target)
	if !strings.Contains(got, "FOO: bar") || !strings.Contains(got, "BAZ: qux") {
		t.Errorf("env pairs missing:\n%s", got)
	}
	if strings.Index(got, "BAZ") > strings.Index(got, "FOO") {
		t.Errorf("env keys must be sorted:\n%s", got)
	}

	if err := SaveMap(target, repo, "env", nil); err != nil {
		t.Fatalf("SaveMap empty: %v", err)
	}
	if got := readFile(t, target); strings.Contains(got, "env:") {
		t.Errorf("empty map must remove the key:\n%s", got)
	}
}

// TestSaveShellsPreservesEnv: saving a shell's path keeps a kept shell's env
// overlay and drops a removed shell entirely.
func TestSaveShellsPreservesEnv(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	writeFile(t, target, "shells:\n  infra:\n    path: /repo/infra\n    env:\n      REGION: eu\n  old:\n    path: /repo/old\n")

	if err := SaveShells(target, repo, map[string]string{"infra": "/repo/infra"}); err != nil {
		t.Fatalf("SaveShells: %v", err)
	}
	got := readFile(t, target)
	if !strings.Contains(got, "REGION: eu") {
		t.Errorf("kept shell's env overlay must survive:\n%s", got)
	}
	if strings.Contains(got, "old:") {
		t.Errorf("removed shell must be gone:\n%s", got)
	}
}

// TestSaveSeed: worktree.seed is written nested and an empty list removes the
// whole worktree block.
func TestSaveSeed(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")

	if err := SaveSeed(target, repo, []string{".env", "openspec"}); err != nil {
		t.Fatalf("SaveSeed: %v", err)
	}
	got := readFile(t, target)
	if !strings.Contains(got, "worktree:") || !strings.Contains(got, "seed:") {
		t.Errorf("worktree.seed missing:\n%s", got)
	}
	if !strings.Contains(got, ".env") || !strings.Contains(got, "openspec") {
		t.Errorf("seed entries missing:\n%s", got)
	}

	if err := SaveSeed(target, repo, nil); err != nil {
		t.Fatalf("SaveSeed empty: %v", err)
	}
	if got := readFile(t, target); strings.Contains(got, "worktree") {
		t.Errorf("empty seed must remove the worktree block:\n%s", got)
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

// TestSaveSDD: enabling writes the bool shorthand; disabling removes it; an
// object-form entry with custom steps survives a re-enable.
func TestSaveSDD(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	skill := SDDOptions()[0]

	if err := SaveSDD(target, repo, map[string]bool{skill: true}); err != nil {
		t.Fatalf("SaveSDD enable: %v", err)
	}
	if got := readFile(t, target); !strings.Contains(got, skill+": true") {
		t.Errorf("want %s: true, got:\n%s", skill, got)
	}

	if err := SaveSDD(target, repo, nil); err != nil {
		t.Fatalf("SaveSDD disable: %v", err)
	}
	if got := readFile(t, target); strings.Contains(got, "sdd:") {
		t.Errorf("empty selection must remove the sdd block:\n%s", got)
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

// TestSaveMountDisabled: disabling a default writes a disable patch; re-enabling
// drops the pure patch.
func TestSaveMountDisabled(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	name := DefaultMountNames()[0]

	if err := SaveMountDisabled(target, repo, map[string]bool{name: true}); err != nil {
		t.Fatalf("SaveMountDisabled on: %v", err)
	}
	got := readFile(t, target)
	if !strings.Contains(got, "name: "+name) || !strings.Contains(got, "disabled: true") {
		t.Errorf("want a disable patch for %s, got:\n%s", name, got)
	}

	if err := SaveMountDisabled(target, repo, map[string]bool{name: false}); err != nil {
		t.Fatalf("SaveMountDisabled off: %v", err)
	}
	if got := readFile(t, target); strings.Contains(got, "disabled: true") {
		t.Errorf("re-enable must drop the pure disable patch:\n%s", got)
	}
}

// TestSaveMountDisabledKeepsRichPatch: a user's source override is not clobbered
// when the mount is toggled off.
func TestSaveMountDisabledKeepsRichPatch(t *testing.T) {
	isolatedHome(t)
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	name := DefaultMountNames()[0]
	writeFile(t, target, "mounts:\n  - name: "+name+"\n    source: /custom/path\n")

	if err := SaveMountDisabled(target, repo, map[string]bool{name: false}); err != nil {
		t.Fatalf("SaveMountDisabled: %v", err)
	}
	if got := readFile(t, target); !strings.Contains(got, "/custom/path") {
		t.Errorf("rich patch must survive a re-enable, got:\n%s", got)
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
