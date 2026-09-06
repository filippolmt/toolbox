package configui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/filippolmt/toolbox/internal/config"
)

// pairsToRows flattens a map into sorted [key, value] rows for the editor.
func pairsToRows(m map[string]string) [][2]string {
	rows := make([][2]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		rows = append(rows, [2]string{k, m[k]})
	}
	return rows
}

// valuesToRows wraps a list into single-column rows (value in column 0).
func valuesToRows(vals []string) [][2]string {
	rows := make([][2]string, 0, len(vals))
	for _, v := range vals {
		rows = append(rows, [2]string{v, ""})
	}
	return rows
}

// columnZero snapshots the column-0 value of each row — the per-row original
// name used to carry a renamed shell's env overlay.
func columnZero(rows [][2]string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r[0]
	}
	return out
}

// rowsEditorState is the collection editor's opening state: the rows plus the
// index-aligned original-name snapshot a rename is detected against.
func rowsEditorState(key string, pair bool, rows [][2]string) editor {
	return editor{key: key, kind: config.EditorRows, rowPair: pair, rows: rows, orig: columnZero(rows)}
}

// updateRows drives the collection editor for env / shells / worktree.seed. It
// has two sub-modes: navigating the row list, and typing into a field.
func (m Model) updateRows(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.ed.rowEdit {
		return m.updateRowField(msg)
	}
	switch msg.String() {
	case "esc":
		m.closeEditor()
	case "up", "k":
		if m.ed.cursor > 0 {
			m.ed.cursor--
		}
	case "down", "j":
		if m.ed.cursor < len(m.ed.rows)-1 {
			m.ed.cursor++
		}
	case "a":
		m.beginRowField(len(m.ed.rows), 0, true)
	case "enter":
		if len(m.ed.rows) > 0 {
			m.beginRowField(m.ed.cursor, 0, false)
		}
	case "d", "x":
		m.deleteRow()
	case "s":
		m.finishSave(m.saveEdit())
	}
	return m, nil
}

// beginRowField opens the text input on one column of a row, seeding it with the
// current cell (empty for a freshly added row).
func (m *Model) beginRowField(row, field int, adding bool) {
	ti := textinput.New()
	if !adding && row < len(m.ed.rows) {
		ti.SetValue(m.ed.rows[row][field])
	}
	ti.Focus()
	m.ed.input = ti
	m.ed.cursor = row
	m.ed.field = field
	m.ed.rowEdit = true
	m.ed.adding = adding
	if field == 0 {
		m.ed.fieldKey = ""
	}
}

func (m Model) updateRowField(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.ed.rowEdit = false
		m.ed.adding = false
		return m, nil
	case "enter":
		m.commitRowField()
		return m, nil
	}
	var cmd tea.Cmd
	m.ed.input, cmd = m.ed.input.Update(msg)
	return m, cmd
}

// commitRowField applies the typed field. For key→value editors the key column
// advances to the value column; otherwise the row is written back.
func (m *Model) commitRowField() {
	val := strings.TrimSpace(m.ed.input.Value())
	if m.ed.rowPair && m.ed.field == 0 {
		if val == "" { // an empty key aborts the row
			m.ed.rowEdit = false
			m.ed.adding = false
			return
		}
		m.ed.fieldKey = val
		m.beginRowField(m.ed.cursor, 1, m.ed.adding)
		return
	}
	m.writeRow(val)
	m.ed.rowEdit = false
	m.ed.adding = false
}

// writeRow stores the completed row into the editor's row set.
func (m *Model) writeRow(val string) {
	var row [2]string
	if m.ed.rowPair {
		row = [2]string{m.ed.fieldKey, val}
	} else {
		if val == "" {
			return
		}
		row = [2]string{val, ""}
	}
	if m.ed.adding {
		m.ed.rows = append(m.ed.rows, row)
		m.ed.orig = append(m.ed.orig, "") // added row has no original name
		m.ed.cursor = len(m.ed.rows) - 1
		return
	}
	if m.ed.cursor < len(m.ed.rows) {
		m.ed.rows[m.ed.cursor] = row // orig[cursor] stays: identity survives a rename
	}
}

func (m *Model) deleteRow() {
	if len(m.ed.rows) == 0 {
		return
	}
	i := m.ed.cursor
	m.ed.rows = append(m.ed.rows[:i], m.ed.rows[i+1:]...)
	if i < len(m.ed.orig) { // keep orig index-aligned with rows
		m.ed.orig = append(m.ed.orig[:i], m.ed.orig[i+1:]...)
	}
	if m.ed.cursor >= len(m.ed.rows) && m.ed.cursor > 0 {
		m.ed.cursor--
	}
}

// shellEntries turns the shells rows into ShellEntry values, carrying each
// row's original name (for rename detection) and, from the given config, the
// env overlay of that original shell so a rename doesn't drop it.
func (e *editor) shellEntries(cfg *config.Config) []ShellEntry {
	var entries []ShellEntry
	for i, r := range e.rows {
		if r[0] == "" {
			continue
		}
		orig := ""
		if i < len(e.orig) {
			orig = e.orig[i]
		}
		var env map[string]string
		if orig != "" && cfg != nil {
			if s, ok := cfg.Shells[orig]; ok {
				env = s.Env
			}
		}
		entries = append(entries, ShellEntry{Name: r[0], Path: r[1], OrigName: orig, Env: env})
	}
	return entries
}

func rowsToPairs(rows [][2]string) map[string]string {
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if r[0] != "" {
			out[r[0]] = r[1]
		}
	}
	return out
}

func rowsToValues(rows [][2]string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r[0] != "" {
			out = append(out, r[0])
		}
	}
	return out
}
