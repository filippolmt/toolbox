package configui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/filippolmt/toolbox/internal/configedit"
)

// Palette: one colour per provenance layer so a key's source is legible at a
// glance, matching the git-config-style origin vocabulary of config show.
var (
	colDefault = lipgloss.Color("#6C6C6C")
	colGlobal  = lipgloss.Color("#5FAFFF")
	colProject = lipgloss.Color("#5FD75F")
	colMixed   = lipgloss.Color("#D7AF5F")
	colEnv     = lipgloss.Color("#D75F87")
	colAccent  = lipgloss.Color("#AF87FF")
	colErr     = lipgloss.Color("#FF5F5F")

	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleTabOn    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(colAccent).Padding(0, 1)
	styleTabOff   = lipgloss.NewStyle().Foreground(colDefault).Padding(0, 1)
	styleKeybar   = lipgloss.NewStyle().Foreground(colDefault)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleErr      = lipgloss.NewStyle().Foreground(colErr)
	stylePanel    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

	// Diff sides in the pending-change panel. Reusing the project/error hues
	// keeps added/removed reading as the familiar green/red without a second
	// palette to keep in sync.
	stylePreviewAdd = lipgloss.NewStyle().Foreground(colProject)
	stylePreviewDel = lipgloss.NewStyle().Foreground(colErr)
)

func originColor(st KeyState) color.Color {
	switch {
	case st.FromEnv:
		return colEnv
	case st.Mixed:
		return colMixed
	}
	switch st.Origin {
	case configedit.OriginGlobal:
		return colGlobal
	case configedit.OriginProject:
		return colProject
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
	fmt.Fprintf(&b, "%s  %s\n", styleTitle.Render("toolbox config"), m.renderTabs())
	fmt.Fprintf(&b, "%s\n\n", styleKeybar.Render("target: "+m.target))

	body := lipgloss.JoinHorizontal(lipgloss.Top, m.renderList(), "  ", m.renderDetail())
	fmt.Fprintf(&b, "%s\n", body)

	if m.status != "" {
		fmt.Fprintf(&b, "\n%s\n", m.renderStatus())
	}
	fmt.Fprintf(&b, "\n%s", m.renderKeybar())
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
	fmt.Fprintf(&b, "%s\n", styleTitle.Render(st.Key))
	if st.Description != "" {
		fmt.Fprintf(&b, "%s\n", styleKeybar.Render(st.Description))
	}
	b.WriteByte('\n')
	// One provenance mention only: fold the old "source:" line into the
	// effective value as a badge, so "default"/"built-in" is not restated thrice.
	badge := lipgloss.NewStyle().Foreground(originColor(st)).Render(originLabel(st))
	fmt.Fprintf(&b, "effective: %s · %s\n", st.Display, badge)
	if st.Default != "" {
		fmt.Fprintf(&b, "default:   %s\n", st.Default)
	}
	scopeLine := st.ScopeDisplay
	if !st.ScopeSet {
		scopeLine = fmt.Sprintf("(unset — inherits %s)", originLabel(st))
	}
	fmt.Fprintf(&b, "in %s: %s\n", m.scope, scopeLine)
	if items := detailEntries(m.cfg, st.Key); len(items) > 0 {
		fmt.Fprintf(&b, "%s\n", styleKeybar.Render("entries: "+strings.Join(items, ", ")))
	}
	switch {
	case st.FromEnv:
		fmt.Fprintf(&b, "\n%s", styleKeybar.Render("read-only — set via TOOLBOX_"+strings.ToUpper(st.Key)))
	case st.ReadOnly:
		fmt.Fprintf(&b, "\n%s", styleKeybar.Render("read-only — only one supported value"))
	case hasEditorEscape(st.Key):
		fmt.Fprintf(&b, "\n%s", styleKeybar.Render("enter: edit   e: open in $EDITOR   r: reset"))
	default:
		fmt.Fprintf(&b, "\n%s", styleKeybar.Render("enter: edit   r: reset to default"))
	}
	return stylePanel.Width(40).Render(b.String())
}

func (m Model) renderEditor() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", styleTitle.Render("edit "+m.ed.key))
	if desc := m.states[m.cursor].Description; desc != "" {
		fmt.Fprintf(&b, "%s\n", styleKeybar.Render(desc))
	}
	b.WriteByte('\n')
	switch m.ed.kind {
	case edString:
		fmt.Fprintf(&b, "%s\n\n", m.ed.input.View())
		b.WriteString(styleKeybar.Render("enter: save   esc: cancel"))
	case edEnum, edTri:
		for i, opt := range m.ed.options {
			cursor := "  "
			label := opt + optionTags(opt, m.ed.current, m.ed.def)
			if i == m.ed.cursor {
				cursor = "> "
				label = styleSelected.Render(label)
			}
			fmt.Fprintf(&b, "%s%s\n", cursor, label)
		}
		fmt.Fprintf(&b, "\n%s", styleKeybar.Render("↑/↓: choose   enter: save   esc: cancel"))
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
			fmt.Fprintf(&b, "%s\n", line)
		}
		fmt.Fprintf(&b, "\n%s", styleKeybar.Render("space: toggle   enter: save   esc: cancel"))
	case edRows:
		b.WriteString(m.renderRows())
	}
	return b.String()
}

// optionTags annotates an editor option with " (current · default)" / " (current)"
// / " (default)" so the user sees both what is set now and what reset lands on.
func optionTags(opt, current, def string) string {
	var tags []string
	if opt == current {
		tags = append(tags, "current")
	}
	if opt == def {
		tags = append(tags, "default")
	}
	if len(tags) == 0 {
		return ""
	}
	return " (" + strings.Join(tags, " · ") + ")"
}

func (m Model) renderRows() string {
	var b strings.Builder
	if len(m.ed.rows) == 0 {
		fmt.Fprintf(&b, "%s\n", styleKeybar.Render("(no entries)"))
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
		fmt.Fprintf(&b, "%s\n", line)
	}
	if m.ed.rowEdit {
		label := "value"
		if m.ed.rowPair && m.ed.field == 0 {
			label = "key"
		}
		fmt.Fprintf(&b, "\n%s: %s\n", label, m.ed.input.View())
		fmt.Fprintf(&b, "\n%s", styleKeybar.Render("enter: next/commit   esc: cancel field"))
	} else {
		fmt.Fprintf(&b, "\n%s", styleKeybar.Render("a: add   enter: edit   d: delete   s: save   esc: cancel"))
	}
	return b.String()
}

// renderPreview shows the change the pending edit would make to the target
// document: the editor's own Mutator applied to the document as it stood when
// the editor opened, diffed against it. Rendering the real mutation is what
// keeps the panel from claiming a shape the writer does not produce.
func (m Model) renderPreview() string {
	title := styleTitle.Render("pending change") + "\n\n"
	body, err := m.previewBody()
	if err != nil {
		return stylePanel.Width(38).Render(title + styleErr.Render("preview unavailable: "+err.Error()))
	}
	return stylePanel.Width(38).Render(title + body)
}

func (m Model) previewBody() (string, error) {
	if m.previewBaseErr != nil {
		return "", m.previewBaseErr
	}
	mut := m.pendingMutator()
	if mut == nil {
		return "", fmt.Errorf("no pending change for %s", m.ed.key)
	}
	lines, err := previewDiff(m.target, m.previewBase, m.previewBaseExists, mut)
	if err != nil {
		return "", err
	}
	// An unchanged fragment would imply a pending change exists; say plainly
	// that re-selecting the active value writes nothing.
	if len(lines) == 0 {
		return styleKeybar.Render("no change"), nil
	}
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if l.Added {
			b.WriteString(stylePreviewAdd.Render("+ " + l.Text))
			continue
		}
		b.WriteString(stylePreviewDel.Render("- " + l.Text))
	}
	return b.String(), nil
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
