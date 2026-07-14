package configui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

// TestResetInheritedKeyIsNoOp: pressing reset on a key the selected scope does
// not set reports "nothing to reset" and never touches a file (guarded before
// any write path, so an empty cwd/target is safe).
func TestResetInheritedKeyIsNoOp(t *testing.T) {
	m := &Model{
		scope:  ScopeRepo,
		states: []KeyState{{Key: "pull", Origin: configedit.OriginGlobal, ScopeSet: false}},
	}
	m.resetToDefault()
	if !strings.Contains(m.status, "nothing to reset") {
		t.Errorf("reset on an inherited key must say nothing to reset, got %q", m.status)
	}
	if !strings.Contains(m.status, "global") {
		t.Errorf("reset status should name the inherited layer, got %q", m.status)
	}
}

// The rows reducer is pure state manipulation on m.ed; these drive it directly
// (no tea key stream) so the row-edit state machine is covered without a TTY.

func fieldInput(v string) textinput.Model {
	ti := textinput.New()
	ti.SetValue(v)
	return ti
}

func TestWriteRowAddsPair(t *testing.T) {
	m := &Model{ed: editor{rowPair: true, adding: true, fieldKey: "K"}}
	m.writeRow("V")
	if len(m.ed.rows) != 1 || m.ed.rows[0] != [2]string{"K", "V"} {
		t.Fatalf("rows = %v, want [[K V]]", m.ed.rows)
	}
	if len(m.ed.orig) != 1 || m.ed.orig[0] != "" {
		t.Errorf("added row must get an empty orig, got %v", m.ed.orig)
	}
	if m.ed.cursor != 0 {
		t.Errorf("cursor should point at the new row, got %d", m.ed.cursor)
	}
}

func TestWriteRowEditsPairKeepingOrig(t *testing.T) {
	m := &Model{ed: editor{rowPair: true, rows: [][2]string{{"a", "1"}}, orig: []string{"a"}, cursor: 0, fieldKey: "a2"}}
	m.writeRow("2")
	if m.ed.rows[0] != [2]string{"a2", "2"} {
		t.Errorf("edited row = %v, want [a2 2]", m.ed.rows[0])
	}
	if m.ed.orig[0] != "a" {
		t.Errorf("orig must survive a rename edit, got %q", m.ed.orig[0])
	}
}

func TestWriteRowSingleEmptyIgnored(t *testing.T) {
	m := &Model{ed: editor{rowPair: false, adding: true}}
	m.writeRow("")
	if len(m.ed.rows) != 0 {
		t.Errorf("empty single-value row must not be added, got %v", m.ed.rows)
	}
}

func TestDeleteRowKeepsOrigAligned(t *testing.T) {
	m := &Model{ed: editor{rows: [][2]string{{"a", ""}, {"b", ""}}, orig: []string{"a", "b"}, cursor: 1}}
	m.deleteRow()
	if len(m.ed.rows) != 1 || m.ed.rows[0][0] != "a" {
		t.Fatalf("rows after delete = %v, want [[a]]", m.ed.rows)
	}
	if len(m.ed.orig) != 1 || m.ed.orig[0] != "a" {
		t.Errorf("orig must stay index-aligned after delete, got %v", m.ed.orig)
	}
	if m.ed.cursor != 0 {
		t.Errorf("cursor should clamp to 0, got %d", m.ed.cursor)
	}
}

func TestCommitRowFieldAdvancesKeyToValue(t *testing.T) {
	m := &Model{ed: editor{rowPair: true, field: 0, rowEdit: true, adding: true, input: fieldInput("K")}}
	m.commitRowField()
	if m.ed.fieldKey != "K" {
		t.Errorf("key column must be buffered, got %q", m.ed.fieldKey)
	}
	if m.ed.field != 1 || !m.ed.rowEdit {
		t.Errorf("must advance to the value column still editing, got field=%d rowEdit=%v", m.ed.field, m.ed.rowEdit)
	}
}

func TestCommitRowFieldEmptyKeyAborts(t *testing.T) {
	m := &Model{ed: editor{rowPair: true, field: 0, rowEdit: true, adding: true, input: fieldInput("  ")}}
	m.commitRowField()
	if m.ed.rowEdit || m.ed.adding {
		t.Errorf("an empty key must abort the row, got rowEdit=%v adding=%v", m.ed.rowEdit, m.ed.adding)
	}
	if len(m.ed.rows) != 0 {
		t.Errorf("no row must be written on abort, got %v", m.ed.rows)
	}
}

// TestMultiSelectSpaceTogglesSelection guards the checkbox toggle shared by
// every multi-select editor (sdd / mounts / inherit_host_auth). Under
// bubbletea v2 a spacebar press stringifies to "space", not " ", so matching
// only " " left the toggle dead — no SDD skill could be selected. Driving the
// real space KeyPressMsg keeps the binding honest across future bubbletea bumps.
func TestMultiSelectSpaceTogglesSelection(t *testing.T) {
	space := tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	if got := space.String(); got != "space" {
		t.Fatalf("bubbletea space key stringifies to %q; test premise stale", got)
	}
	opts := SDDOptions()
	sel := map[string]bool{}
	m := Model{editing: true, ed: editor{key: "sdd", kind: edMulti, options: opts, selected: sel, cursor: indexOf(opts, "openspec")}}
	if _, _ = m.updateEditing(space); !sel["openspec"] {
		t.Fatalf("space must toggle openspec on, got selected = %v", sel)
	}
}

func TestShellEntriesCarriesEnvFromOrig(t *testing.T) {
	m := &Model{
		cfg: &config.Config{Shells: map[string]config.NamedShell{
			"infra": {Path: "/repo/infra", Env: map[string]string{"REGION": "eu"}},
		}},
		ed: editor{key: "shells", rows: [][2]string{{"prod", "/repo/infra"}}, orig: []string{"infra"}},
	}
	entries := m.shellEntries()
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want 1", entries)
	}
	e := entries[0]
	if e.Name != "prod" || e.OrigName != "infra" || e.Env["REGION"] != "eu" {
		t.Errorf("rename entry must carry orig+env, got %+v", e)
	}
}
