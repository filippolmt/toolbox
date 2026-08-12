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
		if key == "mounts" {
			return mountsPreviewDoc(m.ed.options, m.ed.selected)
		}
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

// mountsPreviewDocEntry is the `{name, disabled}` patch shape SaveMountDisabled
// writes; a struct (not a map) so the two keys marshal in the writer's order.
type mountsPreviewDocEntry struct {
	Name     string `yaml:"name"`
	Disabled bool   `yaml:"disabled"`
}

// mountsPreviewDoc renders the mounts editor's selection the way the writer
// records it. The checkboxes name the default mounts to *disable*, so a bare
// `mounts:` list would claim the inverse — that the checked mounts are the only
// ones kept.
func mountsPreviewDoc(options []string, selected map[string]bool) any {
	var patches []mountsPreviewDocEntry
	for _, opt := range options {
		if selected[opt] {
			patches = append(patches, mountsPreviewDocEntry{Name: opt, Disabled: true})
		}
	}
	if len(patches) == 0 {
		return nil
	}
	return map[string]any{"mounts": patches}
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
