package configui

import (
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

// editScope is the scope a key's editor opens in: the repo scope, except for a
// host-only key, which only the Global scope edits.
func editScope(key string) Scope {
	if row, _ := config.KeyByName(key); row.HostOnly {
		return ScopeGlobal
	}
	return ScopeRepo
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

// TestEnterOnAHostOnlyKeyInTheRepoScopeRefuses: a host-only key is read from
// ~/.toolbox.yaml alone, so a repo-scope editor would save a value nothing
// reads. Enter refuses there and names the scope that works; the Global scope
// edits it as usual.
func TestEnterOnAHostOnlyKeyInTheRepoScopeRefuses(t *testing.T) {
	m := press(browsing(ScopeRepo, &config.Config{}, KeyState{Key: config.HostTerminalRelayKey}), "enter")
	if m.editing {
		t.Error("a host-only key must not open an editor in the repo scope")
	}
	wantInStatus(t, m, "~/.toolbox.yaml", "Global")
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
	if !m.editing || m.ed.kind != config.EditorRows {
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
// every multi-select editor (mounts / inherit_host_auth). Under
// bubbletea v2 a spacebar press stringifies to "space", not " ", so matching
// only " " left the toggle dead — no option could be selected. Driving the
// real space key through Update keeps the binding honest across future
// bubbletea bumps.
func TestMultiSelectSpaceTogglesSelection(t *testing.T) {
	if got := (tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}).String(); got != "space" {
		t.Fatalf("bubbletea space key stringifies to %q; test premise stale", got)
	}
	m := press(browsing(ScopeRepo, &config.Config{}, KeyState{Key: "mounts"}), "enter")
	if !m.editing || m.ed.kind != config.EditorSet {
		t.Fatalf("mounts did not open a multi-select (status %q)", m.status)
	}
	first := m.ed.options[0]
	wantOnScreen(t, m, "[ ] "+first)
	m = press(m, "space")
	wantOnScreen(t, m, "[x] "+first)
	m = press(m, "space")
	wantOnScreen(t, m, "[ ] "+first)
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
