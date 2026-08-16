package configui

import (
	"strings"
	"testing"
)

// rowsModel builds a Model whose rows editor is in a known state, so the
// renderers below are exercised without driving the whole tea loop.
func rowsModel(ed editor) Model { return Model{ed: ed} }

// A pair row renders "key = value"; a single-column row renders the value
// alone. Getting this backwards is what the rowPair flag exists to prevent.
func TestRowLineRendersPairAndSingleColumn(t *testing.T) {
	pair := rowsModel(editor{rows: [][2]string{{"FOO", "bar"}}, rowPair: true, cursor: -1})
	if got := pair.rowLine(0, [2]string{"FOO", "bar"}); !strings.Contains(got, "FOO = bar") {
		t.Errorf("pair row = %q, want it to contain %q", got, "FOO = bar")
	}

	single := rowsModel(editor{rows: [][2]string{{"openspec", ""}}, cursor: -1})
	got := single.rowLine(0, [2]string{"openspec", ""})
	if !strings.Contains(got, "openspec") {
		t.Errorf("single row = %q, want it to contain %q", got, "openspec")
	}
	if strings.Contains(got, "=") {
		t.Errorf("single row = %q, want no %q separator", got, "=")
	}
}

// The cursor marker tracks the selected row, and disappears while a row is
// being added — at that point the field prompt below owns the focus.
func TestRowLineCursorMarker(t *testing.T) {
	m := rowsModel(editor{rows: [][2]string{{"a", ""}, {"b", ""}}, cursor: 1})
	if got := m.rowLine(1, [2]string{"b", ""}); !strings.HasPrefix(stripStyle(got), "> ") {
		t.Errorf("selected row = %q, want it to start with %q", stripStyle(got), "> ")
	}
	if got := m.rowLine(0, [2]string{"a", ""}); !strings.HasPrefix(stripStyle(got), "  ") {
		t.Errorf("unselected row = %q, want it to start with two spaces", stripStyle(got))
	}

	adding := rowsModel(editor{rows: [][2]string{{"a", ""}}, cursor: 0, adding: true})
	if got := adding.rowLine(0, [2]string{"a", ""}); strings.HasPrefix(stripStyle(got), "> ") {
		t.Errorf("row while adding = %q, want no cursor marker", stripStyle(got))
	}
}

// The footer swaps between the row-list keys and the field prompt, and the
// prompt names the column being typed into — "key" only for the first column
// of a pair editor, "value" everywhere else.
func TestRowsFooterSwitchesOnEditState(t *testing.T) {
	list := rowsModel(editor{}).rowsFooter()
	if !strings.Contains(list, "a: add") {
		t.Errorf("row-list footer = %q, want it to offer %q", list, "a: add")
	}

	keyField := rowsModel(editor{rowEdit: true, rowPair: true, field: 0}).rowsFooter()
	if !strings.Contains(keyField, "key:") {
		t.Errorf("pair field 0 footer = %q, want it to prompt for %q", keyField, "key")
	}

	valField := rowsModel(editor{rowEdit: true, rowPair: true, field: 1}).rowsFooter()
	if !strings.Contains(valField, "value:") {
		t.Errorf("pair field 1 footer = %q, want it to prompt for %q", valField, "value")
	}

	single := rowsModel(editor{rowEdit: true, field: 0}).rowsFooter()
	if !strings.Contains(single, "value:") {
		t.Errorf("single-column footer = %q, want it to prompt for %q", single, "value")
	}
}

// An empty rows editor still has to say so, rather than rendering a bare
// keybar over nothing.
func TestRenderRowsReportsAnEmptyList(t *testing.T) {
	if got := rowsModel(editor{}).renderRows(); !strings.Contains(got, "(no entries)") {
		t.Errorf("empty rows = %q, want it to contain %q", got, "(no entries)")
	}
}

// Every option is listed, the cursor one is marked, and the current/default
// tags are attached to the right options.
func TestRenderOptionsMarksCursorAndTags(t *testing.T) {
	m := Model{ed: editor{
		kind:    edEnum,
		options: []string{"auto", "always", "never"},
		current: "never",
		def:     "auto",
		cursor:  1,
	}}
	got := stripStyle(m.renderOptions())
	for _, opt := range []string{"auto", "always", "never"} {
		if !strings.Contains(got, opt) {
			t.Errorf("options = %q, want it to list %q", got, opt)
		}
	}
	if !strings.Contains(got, "> always") {
		t.Errorf("options = %q, want the cursor on %q", got, "always")
	}
	if !strings.Contains(got, "never (current)") {
		t.Errorf("options = %q, want %q tagged current", got, "never")
	}
	if !strings.Contains(got, "auto (default)") {
		t.Errorf("options = %q, want %q tagged default", got, "auto")
	}
}

// The checkbox state is per option and independent of the cursor: the box
// reflects the selection, the marker reflects where you are.
func TestRenderCheckboxesTracksSelectionNotCursor(t *testing.T) {
	m := Model{ed: editor{
		kind:     edMulti,
		options:  []string{"claude", "gh"},
		selected: map[string]bool{"gh": true},
		cursor:   0,
	}}
	got := stripStyle(m.renderCheckboxes())
	if !strings.Contains(got, "> [ ] claude") {
		t.Errorf("checkboxes = %q, want the cursor on an unchecked %q", got, "claude")
	}
	if !strings.Contains(got, "  [x] gh") {
		t.Errorf("checkboxes = %q, want %q checked without the cursor", got, "gh")
	}
}

// stripStyle removes the ANSI escapes lipgloss adds, so assertions can match
// the text the user reads rather than the styling around it.
func stripStyle(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
