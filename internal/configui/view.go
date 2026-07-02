package configui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	yaml "gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/configedit"
)

// Palette: one colour per provenance layer so a key's source is legible at a
// glance, matching the git-config-style origin vocabulary of config show.
var (
	colDefault  = lipgloss.Color("#6C6C6C")
	colGlobal   = lipgloss.Color("#5FAFFF")
	colProject  = lipgloss.Color("#5FD75F")
	colExplicit = lipgloss.Color("#D7AF5F")
	colEnv      = lipgloss.Color("#D75F87")
	colAccent   = lipgloss.Color("#AF87FF")
	colErr      = lipgloss.Color("#FF5F5F")

	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleTabOn    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(colAccent).Padding(0, 1)
	styleTabOff   = lipgloss.NewStyle().Foreground(colDefault).Padding(0, 1)
	styleKeybar   = lipgloss.NewStyle().Foreground(colDefault)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleErr      = lipgloss.NewStyle().Foreground(colErr)
	stylePanel    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

func originColor(st KeyState) color.Color {
	if st.FromEnv {
		return colEnv
	}
	switch st.Origin {
	case configedit.OriginGlobal:
		return colGlobal
	case configedit.OriginProject:
		return colProject
	case configedit.OriginExplicit:
		return colExplicit
	default:
		return colDefault
	}
}

// View implements tea.Model.
func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	if m.loadErr != nil {
		return tea.NewView(styleErr.Render("config error: "+m.loadErr.Error()) + "\n")
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render("toolbox config") + "  " + m.renderTabs() + "\n")
	b.WriteString(styleKeybar.Render("target: "+m.target) + "\n\n")

	body := lipgloss.JoinHorizontal(lipgloss.Top, m.renderList(), "  ", m.renderDetail())
	b.WriteString(body + "\n")

	if m.status != "" {
		b.WriteString("\n" + m.renderStatus() + "\n")
	}
	b.WriteString("\n" + m.renderKeybar())
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m Model) renderTabs() string {
	global, repo := styleTabOff, styleTabOff
	if m.scope == ScopeGlobal {
		global = styleTabOn
	} else {
		repo = styleTabOn
	}
	return global.Render("Global") + " " + repo.Render("Repo")
}

func (m Model) renderList() string {
	var rows []string
	for i, st := range m.states {
		marker := "  "
		name := st.Key
		if i == m.cursor {
			marker = "> "
			name = styleSelected.Render(name)
		}
		tag := lipgloss.NewStyle().Foreground(originColor(st)).Render("[" + originLabel(st) + "]")
		rows = append(rows, fmt.Sprintf("%s%-20s %s", marker, name, tag))
	}
	return stylePanel.Width(46).Render(strings.Join(rows, "\n"))
}

func (m Model) renderDetail() string {
	if len(m.states) == 0 {
		return stylePanel.Width(40).Render("(no keys)")
	}
	st := m.states[m.cursor]
	if m.editing {
		editor := stylePanel.Width(40).Render(m.renderEditor())
		return lipgloss.JoinHorizontal(lipgloss.Top, editor, "  ", m.renderPreview())
	}

	var b strings.Builder
	b.WriteString(styleTitle.Render(st.Key) + "\n\n")
	b.WriteString("value:  " + st.Display + "\n")
	b.WriteString("source: " + lipgloss.NewStyle().Foreground(originColor(st)).Render(originLabel(st)) + "\n")
	switch {
	case st.FromEnv:
		b.WriteString("\n" + styleKeybar.Render("read-only — set via TOOLBOX_"+strings.ToUpper(st.Key)))
	case hasEditorEscape(st.Key):
		b.WriteString("\n" + styleKeybar.Render("enter: edit   e: open in $EDITOR   r: reset"))
	default:
		b.WriteString("\n" + styleKeybar.Render("enter: edit   r: reset to default"))
	}
	return stylePanel.Width(40).Render(b.String())
}

func (m Model) renderEditor() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("edit "+m.ed.key) + "\n\n")
	switch m.ed.kind {
	case edString:
		b.WriteString(m.ed.input.View() + "\n\n")
		b.WriteString(styleKeybar.Render("enter: save   esc: cancel"))
	case edEnum, edTri:
		for i, opt := range m.ed.options {
			cursor := "  "
			if i == m.ed.cursor {
				cursor = "> "
				opt = styleSelected.Render(opt)
			}
			b.WriteString(cursor + opt + "\n")
		}
		b.WriteString("\n" + styleKeybar.Render("↑/↓: choose   enter: save   esc: cancel"))
	case edMulti:
		for i, opt := range m.ed.options {
			cursor := "  "
			if i == m.ed.cursor {
				cursor = "> "
			}
			box := "[ ]"
			if m.ed.selected[opt] {
				box = "[x]"
			}
			line := fmt.Sprintf("%s%s %s", cursor, box, opt)
			if i == m.ed.cursor {
				line = styleSelected.Render(line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + styleKeybar.Render("space: toggle   enter: save   esc: cancel"))
	case edRows:
		b.WriteString(m.renderRows())
	}
	return b.String()
}

func (m Model) renderRows() string {
	var b strings.Builder
	if len(m.ed.rows) == 0 {
		b.WriteString(styleKeybar.Render("(no entries)") + "\n")
	}
	for i, r := range m.ed.rows {
		cursor := "  "
		if i == m.ed.cursor && !m.ed.adding {
			cursor = "> "
		}
		var line string
		if m.ed.rowPair {
			line = fmt.Sprintf("%s%s = %s", cursor, r[0], r[1])
		} else {
			line = cursor + r[0]
		}
		if i == m.ed.cursor && !m.ed.rowEdit {
			line = styleSelected.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if m.ed.rowEdit {
		label := "value"
		if m.ed.rowPair && m.ed.field == 0 {
			label = "key"
		}
		b.WriteString("\n" + label + ": " + m.ed.input.View() + "\n")
		b.WriteString("\n" + styleKeybar.Render("enter: next/commit   esc: cancel field"))
	} else {
		b.WriteString("\n" + styleKeybar.Render("a: add   enter: edit   d: delete   s: save   esc: cancel"))
	}
	return b.String()
}

// renderPreview shows the YAML that would be written for the pending edit, so
// the change is visible before it is saved.
func (m Model) renderPreview() string {
	doc := m.previewDoc()
	title := styleTitle.Render("pending YAML") + "\n\n"
	if doc == nil {
		return stylePanel.Width(38).Render(title + styleKeybar.Render("# "+m.ed.key+": (unset — key removed)"))
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return stylePanel.Width(38).Render(title + styleErr.Render("preview unavailable"))
	}
	return stylePanel.Width(38).Render(title + strings.TrimRight(string(out), "\n"))
}

// previewDoc builds the marshalable document fragment for the editor's current
// pending value. A nil return means the edit removes the key.
func (m Model) previewDoc() any {
	key := m.ed.key
	switch m.ed.kind {
	case edEnum:
		return map[string]any{key: m.ed.options[m.ed.cursor]}
	case edString:
		v := strings.TrimSpace(m.ed.input.Value())
		if v == "" {
			return nil
		}
		return map[string]any{key: v}
	case edTri:
		b := triValue(m.ed.options[m.ed.cursor])
		if b == nil {
			return nil
		}
		return map[string]any{key: *b}
	case edMulti:
		var vals []string
		for _, opt := range m.ed.options {
			if m.ed.selected[opt] {
				vals = append(vals, opt)
			}
		}
		if len(vals) == 0 {
			return nil
		}
		return map[string]any{key: vals}
	case edRows:
		return m.rowsPreviewDoc()
	}
	return nil
}

func (m Model) rowsPreviewDoc() any {
	switch m.ed.key {
	case "env":
		pairs := rowsToPairs(m.ed.rows)
		if len(pairs) == 0 {
			return nil
		}
		return map[string]any{"env": pairs}
	case "shells":
		pairs := rowsToPairs(m.ed.rows)
		if len(pairs) == 0 {
			return nil
		}
		inner := map[string]any{}
		for name, path := range pairs {
			inner[name] = map[string]any{"path": path}
		}
		return map[string]any{"shells": inner}
	case "worktree":
		vals := rowsToValues(m.ed.rows)
		if len(vals) == 0 {
			return nil
		}
		return map[string]any{"worktree": map[string]any{"seed": vals}}
	}
	return nil
}

func (m Model) renderStatus() string {
	if strings.HasPrefix(m.status, "save blocked") || strings.HasPrefix(m.status, "reset failed") {
		return styleErr.Render(m.status)
	}
	return styleKeybar.Render(m.status)
}

func (m Model) renderKeybar() string {
	return styleKeybar.Render("↑/↓: key   tab: scope   enter: edit   r: reset   q: quit")
}
