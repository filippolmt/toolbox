package configui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
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
		m.finishSave(m.saveRows())
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
		m.ed.cursor = len(m.ed.rows) - 1
		return
	}
	if m.ed.cursor < len(m.ed.rows) {
		m.ed.rows[m.ed.cursor] = row
	}
}

func (m *Model) deleteRow() {
	if len(m.ed.rows) == 0 {
		return
	}
	i := m.ed.cursor
	m.ed.rows = append(m.ed.rows[:i], m.ed.rows[i+1:]...)
	if m.ed.cursor >= len(m.ed.rows) && m.ed.cursor > 0 {
		m.ed.cursor--
	}
}

// saveRows persists the editor's rows via the matching adapter writer.
func (m *Model) saveRows() error {
	switch m.ed.key {
	case "env":
		return SaveMap(m.target, m.cwd, "env", rowsToPairs(m.ed.rows))
	case "shells":
		return SaveShells(m.target, m.cwd, rowsToPairs(m.ed.rows))
	case "worktree":
		return SaveSeed(m.target, m.cwd, rowsToValues(m.ed.rows))
	}
	// Unreachable today (openEditor only assigns edRows to the three keys
	// above); an explicit error stops a future rows key from reporting a false
	// "saved" with no write.
	return fmt.Errorf("no rows writer for key %q", m.ed.key)
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
