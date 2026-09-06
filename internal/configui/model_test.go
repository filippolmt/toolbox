package configui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

// browsing builds a model sitting on one key of the key list, as the program
// stands after a reload — the state every keystroke below starts from.
func browsing(scope Scope, cfg *config.Config, st KeyState) Model {
	return Model{scope: scope, cfg: cfg, states: []KeyState{st}}
}

// TestResetInheritedKeyIsNoOp: reset on a key the selected scope does not set
// reports "nothing to reset" and never touches a file (guarded before any write
// path, so an empty cwd/target is safe). The layer it inherits from is named in
// the status, not merely somewhere on screen — the list tag and the detail
// pane's "(unset — inherits global)" both say "global" whatever reset does.
func TestResetInheritedKeyIsNoOp(t *testing.T) {
	m := press(browsing(ScopeRepo, &config.Config{},
		KeyState{Key: "pull", Origin: configedit.OriginGlobal}), "r")
	wantInStatus(t, m, "nothing to reset", "global")
}

// TestResetEnvSourcedKeyPointsAtTheHostVar: a value coming from TOOLBOX_* is
// not in any file, so reset has nothing to remove — the status names the var to
// unset instead of reporting a reset that did not happen.
func TestResetEnvSourcedKeyPointsAtTheHostVar(t *testing.T) {
	m := press(browsing(ScopeRepo, &config.Config{},
		KeyState{Key: "pull", FromEnv: true, ScopeSet: true}), "r")
	wantInStatus(t, m, "TOOLBOX_PULL", "unset")
	notInStatus(t, m, "reset failed", "reset pull")
}

// TestEnterOnEnvSourcedKeyRefuses: env sits above every file layer the UI can
// write, so an editor here would save a value the next load ignores. Enter
// refuses and the status names which var owns the value — the detail pane says
// "read-only — set via TOOLBOX_PULL" for an env-sourced key whether enter was
// pressed or not, so only the status can testify about the refusal.
func TestEnterOnEnvSourcedKeyRefuses(t *testing.T) {
	m := press(browsing(ScopeRepo, &config.Config{},
		KeyState{Key: "pull", FromEnv: true}), "enter")
	if m.editing {
		t.Error("an env-sourced key must not open an editor")
	}
	wantInStatus(t, m, "TOOLBOX_PULL", "read-only")
}

// TestEnterOnSingleValuedKeyRefuses: a key whose enum holds one option has
// nothing to choose, so enter says so rather than opening a list of one.
func TestEnterOnSingleValuedKeyRefuses(t *testing.T) {
	m := press(browsing(ScopeRepo, &config.Config{},
		KeyState{Key: "shell", ReadOnly: true, Default: "zsh"}), "enter")
	if m.editing {
		t.Error("a single-valued key must not open an editor")
	}
	wantInStatus(t, m, "single supported value", "zsh")
}

// TestEnterOnAnUnsetKeyWarnsItCreatesAnOverride: opening an editor in a scope
// whose file does not set the key will fork a value into that layer, so the
// status says so before anything is written.
func TestEnterOnAnUnsetKeyWarnsItCreatesAnOverride(t *testing.T) {
	m := press(browsing(ScopeRepo, &config.Config{}, KeyState{Key: "pull"}), "enter")
	if !m.editing {
		t.Fatalf("pull must open an editor, status = %q", m.status)
	}
	wantInStatus(t, m, "creates an override in "+ScopeRepo.String())
}

// TestEnterOnAKeyTheScopeSetsWarnsNothing is the other half: editing a key the
// layer already sets forks nothing, so the override warning must not fire.
func TestEnterOnAKeyTheScopeSetsWarnsNothing(t *testing.T) {
	m := press(browsing(ScopeRepo, &config.Config{},
		KeyState{Key: "pull", ScopeSet: true}), "enter")
	notInStatus(t, m, "creates an override")
}

// rowsEditor opens a key's collection editor through the key stream and fails
// the test if it did not open.
func rowsEditor(t *testing.T, key string, cfg *config.Config) Model {
	t.Helper()
	m := press(browsing(ScopeRepo, cfg, KeyState{Key: key, ScopeSet: true}), "enter")
	if !m.editing || m.ed.kind != edRows {
		t.Fatalf("%q did not open a rows editor (status %q)", key, m.status)
	}
	return m
}

// TestAddingAPairRowBuffersTheKeyThenTheValue: a key→value editor takes the two
// columns in two commits, and the finished row joins the list under the cursor.
func TestAddingAPairRowBuffersTheKeyThenTheValue(t *testing.T) {
	m := rowsEditor(t, "env", &config.Config{})
	m = press(m, "a")
	wantOnScreen(t, m, "key:") // the key column is open first
	m = typeText(m, "REGION")
	m = press(m, "enter")
	wantOnScreen(t, m, "value:") // committing the key advances to the value
	m = typeText(m, "eu")
	m = press(m, "enter")
	wantOnScreen(t, m, "> REGION = eu")
	notOnScreen(t, m, "(no entries)")
}

// TestAnEmptyKeyAbortsTheRow: committing an empty key column writes no row —
// a pair whose key is blank has nothing to write it under. The field has to
// close too, or the abort is just a stuck prompt.
func TestAnEmptyKeyAbortsTheRow(t *testing.T) {
	m := rowsEditor(t, "env", &config.Config{})
	m = press(m, "a")
	wantOnScreen(t, m, "key:")
	m = press(m, "enter")
	wantOnScreen(t, m, "(no entries)", "a: add")
	notOnScreen(t, m, "key:")
}

// TestAnEmptyValueAbortsASingleColumnRow is its single-column sibling: a seed
// path editor has only one column, so an empty one is the whole row.
func TestAnEmptyValueAbortsASingleColumnRow(t *testing.T) {
	m := rowsEditor(t, "worktree", &config.Config{})
	m = press(m, "a")
	wantOnScreen(t, m, "value:")
	m = press(m, "enter")
	wantOnScreen(t, m, "(no entries)", "a: add")
	notOnScreen(t, m, "value:")
}

// TestEscBacksOutOfAFieldWithoutClosingTheEditor: esc has two meanings in a
// rows editor — abandon the field being typed, then close the editor — and the
// row list has to survive the first.
func TestEscBacksOutOfAFieldWithoutClosingTheEditor(t *testing.T) {
	m := rowsEditor(t, "env", &config.Config{Env: map[string]string{"REGION": "eu"}})
	m = press(m, "enter") // open the key column of row 0
	wantOnScreen(t, m, "key:")
	m = press(m, "esc")
	if !m.editing {
		t.Fatal("esc in a field must not close the editor")
	}
	wantOnScreen(t, m, "REGION = eu", "a: add")
	notOnScreen(t, m, "key:")

	m = press(m, "esc")
	if m.editing {
		t.Error("esc on the row list must close the editor")
	}
}

// TestDeleteRemovesTheSelectedRow: the delete key acts on the row under the
// cursor, and only that one.
func TestDeleteRemovesTheSelectedRow(t *testing.T) {
	m := rowsEditor(t, "env", &config.Config{Env: map[string]string{"ALPHA": "1", "BETA": "2"}})
	m = press(m, "down", "d")
	wantOnScreen(t, m, "ALPHA = 1")
	notOnScreen(t, m, "BETA")
}

// TestRenamingAShellAfterADeleteKeepsItsEnv: a shells row carries the identity
// of the entry it opened with, so the writer can tell a rename from a new entry
// and move that entry's env: block with it. Deleting an *earlier* row first is
// the case that proves the identities stay aligned with the rows: drop the
// alignment and every later row inherits its neighbour's identity, so the rename
// credits the wrong entry and takes the env block of a shell the user never
// touched. Deleting a later row would not show it — the surviving row's index is
// unchanged — which is why this one deletes the first of the two.
//
// Driven end to end: the assertion is on the file the save wrote.
func TestRenamingAShellAfterADeleteKeepsItsEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	target := filepath.Join(cwd, ".toolbox.yaml")
	writeFile(t, target, "shells:\n  infra:\n    path: /repo/infra\n  legacy:\n    path: /repo/legacy\n    env:\n      REGION: eu\n")
	cfg := &config.Config{Shells: map[string]config.NamedShell{
		"infra":  {Path: "/repo/infra"},
		"legacy": {Path: "/repo/legacy", Env: map[string]string{"REGION": "eu"}},
	}}

	m := Model{cwd: cwd, target: target, scope: ScopeRepo, cfg: cfg,
		states: []KeyState{{Key: "shells", ScopeSet: true}}}
	m = press(m, "enter") // rows: infra, legacy (sorted)
	m = press(m, "d")     // drop infra, the first row; legacy shifts up into its slot
	m = press(m, "enter") // open the surviving row's name column
	m = eraseField(m, len("legacy"))
	m = typeText(m, "prod")
	m = press(m, "enter") // commit the name, advance to the path column
	m = press(m, "enter") // keep the path
	m = press(m, "s")     // save

	wantInStatus(t, m, "saved shells")
	got := readFile(t, target)
	if !strings.Contains(got, "prod:") || !strings.Contains(got, "/repo/legacy") {
		t.Errorf("the rename did not reach the file, or renamed the wrong row:\n%s", got)
	}
	if strings.Contains(got, "infra") {
		t.Errorf("the deleted shell survived the save:\n%s", got)
	}
	if !strings.Contains(got, "REGION: eu") {
		t.Errorf("the renamed shell lost the env block it never edited:\n%s", got)
	}
}

// TestMultiSelectSpaceTogglesSelection guards the checkbox toggle shared by
// every multi-select editor (sdd / mounts / inherit_host_auth). Under
// bubbletea v2 a spacebar press stringifies to "space", not " ", so matching
// only " " left the toggle dead — no SDD skill could be selected. Driving the
// real space key through Update keeps the binding honest across future
// bubbletea bumps.
func TestMultiSelectSpaceTogglesSelection(t *testing.T) {
	if got := (tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}).String(); got != "space" {
		t.Fatalf("bubbletea space key stringifies to %q; test premise stale", got)
	}
	m := press(browsing(ScopeRepo, &config.Config{}, KeyState{Key: "sdd"}), "enter")
	if !m.editing || m.ed.kind != edMulti {
		t.Fatalf("sdd did not open a multi-select (status %q)", m.status)
	}
	first := m.ed.options[0]
	wantOnScreen(t, m, "[ ] "+first)
	m = press(m, "space")
	wantOnScreen(t, m, "[x] "+first)
	m = press(m, "space")
	wantOnScreen(t, m, "[ ] "+first)
}

// TestOpenEditorRefusesWorkspaceOnlyKeyInGlobalScope: sdd's effect is anchored
// to the workspace, so the structured editor must not write it into the global
// layer. The row still displays (a hand-written global flag stays legal and
// visible, and the $EDITOR escape still reaches it) — enter refuses, and the
// status names both the reason and the way out. Asserted on the status, because
// the keybar names `tab` and the detail pane names the scope on every frame.
func TestOpenEditorRefusesWorkspaceOnlyKeyInGlobalScope(t *testing.T) {
	m := press(browsing(ScopeGlobal, &config.Config{}, KeyState{Key: "sdd"}), "enter")
	if m.editing {
		t.Error("sdd must not open an editor in the global scope")
	}
	wantInStatus(t, m, "per-workspace", ScopeGlobal.String(), "press tab")
}

// TestOpenEditorAllowsWorkspaceOnlyKeyInRepoScope is the other half: the guard
// is per-scope, not a blanket read-only marking of the key.
func TestOpenEditorAllowsWorkspaceOnlyKeyInRepoScope(t *testing.T) {
	m := press(browsing(ScopeRepo, &config.Config{}, KeyState{Key: "sdd", ScopeSet: true}), "enter")
	if !m.editing {
		t.Errorf("sdd must stay editable in the repo scope, status = %q", m.status)
	}
	notInStatus(t, m, "per-workspace")
}

// enabledSDDRepo builds a workspace with gsd enabled through the real save
// path, and returns its cwd and project-config path.
func enabledSDDRepo(t *testing.T) (cwd, target string) {
	t.Helper()
	cwd = t.TempDir()
	target = filepath.Join(cwd, ".toolbox.yaml")
	if err := SaveSDD(target, cwd, map[string]bool{"gsd": true}); err != nil {
		t.Fatalf("SaveSDD: %v", err)
	}
	return cwd, target
}

// TestResetWorkspaceOnlyKeyClearsFencesInRepoScope: dropping the flag without
// its .gitignore blocks would leave a fence nothing owns any more — the CLI has
// no uninstall, so an orphan is cleaned by hand or not at all.
func TestResetWorkspaceOnlyKeyClearsFencesInRepoScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd, target := enabledSDDRepo(t)
	if !strings.Contains(readFile(t, filepath.Join(cwd, ".gitignore")), configedit.GitignoreFenceStart("gsd")) {
		t.Fatal("fixture wrote no fence to reset")
	}

	m := Model{cwd: cwd, target: target, scope: ScopeRepo, states: []KeyState{{Key: "sdd", ScopeSet: true}}}
	m = press(m, "r")

	wantInStatus(t, m, "reset sdd")
	if got := readFile(t, target); strings.Contains(got, "sdd") {
		t.Errorf("reset left the sdd flag behind:\n%s", got)
	}
	if got := readFile(t, filepath.Join(cwd, ".gitignore")); strings.Contains(got, configedit.GitignoreFenceStart("gsd")) {
		t.Errorf("reset orphaned the gsd fence:\n%s", got)
	}
}

// TestResetWorkspaceOnlyKeyKeepsFencesInGlobalScope: resetting a hand-written
// global flag is allowed (the guard is about creating one, not clearing it),
// but the fences belong to whichever workspaces wrote them — removing the
// current one's on behalf of a file that applies everywhere is the same
// asymmetry, reversed.
func TestResetWorkspaceOnlyKeyKeepsFencesInGlobalScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, _ := enabledSDDRepo(t)
	global := filepath.Join(home, ".toolbox.yaml")
	if err := os.WriteFile(global, []byte("sdd:\n  gsd: true\n"), 0o600); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	m := Model{cwd: cwd, target: global, scope: ScopeGlobal, states: []KeyState{{Key: "sdd", ScopeSet: true}}}
	m = press(m, "r")

	if got := readFile(t, global); strings.Contains(got, "sdd") {
		t.Errorf("reset left the global sdd flag behind:\n%s", got)
	}
	if got := readFile(t, filepath.Join(cwd, ".gitignore")); !strings.Contains(got, configedit.GitignoreFenceStart("gsd")) {
		t.Errorf("a global reset must not touch the workspace fence:\n%s", got)
	}
	wantInStatus(t, m, "fences left untouched")
}

// TestResetInRepoScopeKeepsFencesAKeptGlobalFlagStillNeeds: reset removes the
// key from the selected layer only, so a hand-written global flag keeps the
// skill enabled — and a skill that still runs still needs its fence. Reset
// therefore reconciles against what the layers now resolve to, not against the
// empty set, or it produces the mirror of the orphan it exists to prevent.
func TestResetInRepoScopeKeepsFencesAKeptGlobalFlagStillNeeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, target := enabledSDDRepo(t)
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte("sdd:\n  gsd: true\n"), 0o600); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	m := Model{cwd: cwd, target: target, scope: ScopeRepo, states: []KeyState{{Key: "sdd", ScopeSet: true}}}
	press(m, "r")

	if got := readFile(t, target); strings.Contains(got, "sdd") {
		t.Errorf("reset left the repo sdd flag behind:\n%s", got)
	}
	if got := readFile(t, filepath.Join(cwd, ".gitignore")); !strings.Contains(got, configedit.GitignoreFenceStart("gsd")) {
		t.Errorf("gsd is still enabled by the global layer, so its fence must survive:\n%s", got)
	}
}

// TestQuitStopsTheProgram: q is the documented way out, and it has to return
// tea.Quit rather than merely blanking the view.
func TestQuitStopsTheProgram(t *testing.T) {
	next, cmd := browsing(ScopeRepo, &config.Config{}, KeyState{Key: "pull"}).Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("q must return a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q must quit, got %T", cmd())
	}
	if got := next.(Model).View().Content; got != "" {
		t.Errorf("a quitting model must render nothing, got %q", got)
	}
}
