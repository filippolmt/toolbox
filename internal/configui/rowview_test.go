package configui

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// What each editor kind puts on screen, read off View — the same rendering
// bubbletea paints, reached the same way. The editors are opened by pressing
// keys, so a pane can only be asserted on in a state the program can actually
// reach.

// A pair row renders "key = value"; a single-column row renders the value
// alone. Getting this backwards is what the rowPair flag exists to prevent.
func TestRowLineRendersPairAndSingleColumn(t *testing.T) {
	pair := rowsEditor(t, "env", &config.Config{Env: map[string]string{"FOO": "bar"}})
	wantOnScreen(t, pair, "FOO = bar")

	single := rowsEditor(t, "worktree", &config.Config{
		Worktree: config.WorktreeConfig{Seed: []string{".env.local"}},
	})
	wantOnScreen(t, single, ".env.local")
	if line := rowsPaneLine(t, single, ".env.local"); strings.Contains(line, "=") {
		t.Errorf("single-column row = %q, want no %q separator", line, "=")
	}
}

// The cursor marker tracks the selected row, and disappears while a row is
// being added — at that point the field prompt below owns the focus.
func TestRowCursorMarkerTracksTheSelection(t *testing.T) {
	m := rowsEditor(t, "env", &config.Config{Env: map[string]string{"ALPHA": "1", "BETA": "2"}})
	wantOnScreen(t, m, "> ALPHA = 1", "  BETA = 2")

	m = press(m, "down")
	wantOnScreen(t, m, "  ALPHA = 1", "> BETA = 2")

	m = press(m, "a")
	notOnScreen(t, m, "> ALPHA", "> BETA")
}

// The footer swaps between the row-list keys and the field prompt, and the
// prompt names the column being typed into — "key" only for the first column
// of a pair editor, "value" everywhere else.
func TestRowsFooterSwitchesOnEditState(t *testing.T) {
	pair := rowsEditor(t, "env", &config.Config{Env: map[string]string{"FOO": "bar"}})
	wantOnScreen(t, pair, "a: add")

	keyField := press(pair, "enter")
	wantOnScreen(t, keyField, "key:")

	valField := typeText(keyField, "X")
	valField = press(valField, "enter")
	wantOnScreen(t, valField, "value:")

	single := rowsEditor(t, "worktree", &config.Config{
		Worktree: config.WorktreeConfig{Seed: []string{".env.local"}},
	})
	wantOnScreen(t, press(single, "enter"), "value:")
}

// An empty rows editor still has to say so, rather than rendering a bare
// keybar over nothing.
func TestRenderRowsReportsAnEmptyList(t *testing.T) {
	wantOnScreen(t, rowsEditor(t, "env", &config.Config{}), "(no entries)")
}

// Every option is listed, the cursor one is marked, and the current/default
// tags are attached to the right options.
func TestRenderOptionsMarksCursorAndTags(t *testing.T) {
	m := press(browsing(ScopeRepo, &config.Config{Pull: config.PullNever},
		KeyState{Key: "pull", ScopeSet: true}), "enter")
	if !m.editing || m.ed.kind != config.EditorChoice {
		t.Fatalf("pull did not open an enum editor (status %q)", m.status)
	}
	// The editor opens on the current value; step up so the cursor and the
	// "current" tag sit on different options and cannot be confused.
	m = press(m, "up")
	wantOnScreen(t, m, "auto", "always", "never",
		"> always", "never (current)", "auto (default)")
}

// The checkbox state is per option and independent of the cursor: the box
// reflects the selection, the marker reflects where you are.
func TestRenderCheckboxesTracksSelectionNotCursor(t *testing.T) {
	opts := SDDOptions()
	if len(opts) < 2 {
		t.Skipf("need two SDD packs to separate cursor from selection, have %v", opts)
	}
	cfg := &config.Config{SDD: map[string]config.SDDSkill{opts[1]: {Enabled: true}}}
	m := press(browsing(ScopeRepo, cfg, KeyState{Key: "sdd", ScopeSet: true}), "enter")
	if !m.editing || m.ed.kind != config.EditorSet {
		t.Fatalf("sdd did not open a multi-select (status %q)", m.status)
	}
	wantOnScreen(t, m, "> [ ] "+opts[0], "  [x] "+opts[1])
}

// The detail pane names the escape hatch only for the keys that offer one, so
// the keybar never advertises a key press that does nothing.
func TestDetailKeybarOffersTheEditorEscapeOnlyWhereItExists(t *testing.T) {
	withEscape := browsing(ScopeRepo, &config.Config{}, KeyState{Key: "mounts", ScopeSet: true})
	wantOnScreen(t, withEscape, "$EDITOR")

	withoutEscape := browsing(ScopeRepo, &config.Config{}, KeyState{Key: "pull", ScopeSet: true})
	notOnScreen(t, withoutEscape, "$EDITOR")
}

// rowsPaneLine returns the rendered line containing want, so an assertion can
// be made about that line alone rather than the whole screen.
func rowsPaneLine(t *testing.T, m Model, want string) string {
	t.Helper()
	for _, line := range strings.Split(screen(m), "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no rendered line contains %q:\n%s", want, screen(m))
	return ""
}
