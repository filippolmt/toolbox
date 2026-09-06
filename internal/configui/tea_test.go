package configui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The tea half is driven the way bubbletea drives it: key presses in through
// Update, rendered text out through View. Every test of a decision this half
// makes — the reset, the read-only and env-sourced refusals, which editor a key
// opens, the row state machine — goes through this harness, so a decision
// reachable only by calling a private method is a decision the running program
// cannot reach either.

// keyMsg builds the KeyPressMsg for a key named the way Update matches it
// (msg.String()), so the test says "esc" and the model sees the escape key.
func keyMsg(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space":
		// bubbletea v2 stringifies the spacebar as "space", not " ".
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	r := []rune(name)
	if len(r) != 1 {
		panic("keyMsg: no such key: " + name)
	}
	return tea.KeyPressMsg{Code: r[0], Text: name}
}

// press feeds key presses through Update in order and returns the model they
// produced.
func press(m Model, names ...string) Model {
	for _, name := range names {
		next, _ := m.Update(keyMsg(name))
		m = next.(Model)
	}
	return m
}

// typeText presses one key per character — how a text field is filled in.
func typeText(m Model, text string) Model {
	for _, r := range text {
		m = press(m, string(r))
	}
	return m
}

// eraseField presses backspace n times, so a rename types over nothing.
func eraseField(m Model, n int) Model {
	for range n {
		m = press(m, "backspace")
	}
	return m
}

// chooseOption walks the open choice editor's cursor onto want with the arrow
// keys, the way a user does. It reads the cursor to know which way to go —
// state, not a decision — and the step budget keeps a swallowed key press from
// spinning instead of failing.
func chooseOption(t *testing.T, m Model, want string) Model {
	t.Helper()
	target := indexOf(m.ed.options, want)
	if m.ed.options[target] != want {
		t.Fatalf("option %q is not on the list %v", want, m.ed.options)
	}
	for range len(m.ed.options) {
		switch {
		case m.ed.cursor > target:
			m = press(m, "up")
		case m.ed.cursor < target:
			m = press(m, "down")
		default:
			return m
		}
	}
	t.Fatalf("the cursor never reached %q; it sits on %q", want, m.ed.options[m.ed.cursor])
	return m
}

// screen renders the model through View and strips the styling, so assertions
// match the text the user reads rather than the escapes around it.
func screen(m Model) string { return stripStyle(m.View().Content) }

// statusArea returns the part of the screen the status puts there, and nothing
// else: the model is rendered twice, once as it stands and once with the status
// cleared, and what only the first has is the status area.
//
// Isolated by construction rather than by counting lines, because the failure
// #903 was filed about is an assertion that passes anyway. The view always draws
// a keybar naming `tab` and a detail pane naming the scope and the TOOLBOX_ var,
// so a whole-screen assertion on any of those says nothing about the decision
// under test — it matches the chrome.
func statusArea(m Model) string {
	quiet := m
	quiet.status = ""
	with, without := strings.Split(screen(m), "\n"), strings.Split(screen(quiet), "\n")
	head := 0
	for head < len(with) && head < len(without) && with[head] == without[head] {
		head++
	}
	tail := 0
	for tail < len(with)-head && tail < len(without)-head &&
		with[len(with)-1-tail] == without[len(without)-1-tail] {
		tail++
	}
	return strings.TrimSpace(strings.Join(with[head:len(with)-tail], "\n"))
}

// wantInStatus asserts the status area says each of want. A model with no status
// has an empty area, so "the status said this" and "the screen happened to
// contain this" cannot be confused.
func wantInStatus(t *testing.T, m Model, want ...string) {
	t.Helper()
	got := statusArea(m)
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("status does not say %q, status area is:\n%s", w, got)
		}
	}
}

// notInStatus is its negative, for the message a guard must not have produced.
func notInStatus(t *testing.T, m Model, unwanted ...string) {
	t.Helper()
	got := statusArea(m)
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("status must not say %q, status area is:\n%s", w, got)
		}
	}
}

// wantOnScreen asserts the rendered view shows each of want. For pane content
// only — a message the status carries goes through wantInStatus, or the
// assertion can be satisfied by chrome.
func wantOnScreen(t *testing.T, m Model, want ...string) {
	t.Helper()
	got := screen(m)
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("view does not say %q:\n%s", w, got)
		}
	}
}

// notOnScreen is its negative, for the row a delete removed or the editor a
// refusal never opened.
func notOnScreen(t *testing.T, m Model, unwanted ...string) {
	t.Helper()
	got := screen(m)
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("view must not say %q:\n%s", w, got)
		}
	}
}

// TestStripStyleKeepsTheTextBehindEveryEscapeShape: every assertion in this
// package reads the screen through stripStyle, so a shape it mishandles does not
// fail loudly — it deletes text, and the assertion that wanted that text reports
// a missing string. The SGR-only version ran from an escape to the next "m",
// which swallowed the rest of the line behind an OSC hyperlink or a cursor move.
func TestStripStyleKeepsTheTextBehindEveryEscapeShape(t *testing.T) {
	for name, in := range map[string]string{
		"sgr":            "\x1b[1;38;5;99mkept\x1b[0m",
		"cursor csi":     "\x1b[2Kkept\x1b[1G",
		"osc bel":        "\x1b]8;;https://example.com/menu\x07kept\x1b]8;;\x07",
		"osc st":         "\x1b]0;a title\x1b\\kept",
		"two-byte":       "\x1b7kept\x1b8",
		"truncated csi":  "kept\x1b[3",
		"bare escape":    "kept\x1b",
		"csi around sgr": "\x1b[2J\x1b[31mkept\x1b[0m",
	} {
		if got := stripStyle(in); got != "kept" {
			t.Errorf("%s: stripStyle(%q) = %q, want %q", name, in, got, "kept")
		}
	}
	if got := stripStyle("no escapes here"); got != "no escapes here" {
		t.Errorf("plain text must pass through untouched, got %q", got)
	}
}

// stripStyle removes the ANSI escape sequences lipgloss adds, so assertions can
// match the text the user reads rather than the styling around it.
//
// It knows the three shapes that reach a rendered view — CSI (the SGR colours,
// and any cursor motion), OSC (hyperlinks, terminated by BEL or ST) and the
// two-byte escapes. A stripper that only knew SGR would run from an escape to
// the next "m", so a single OSC or cursor sequence would eat the real text
// behind it and a broken assertion would read as a missing string.
func stripStyle(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i = skipEscape(s, i)
	}
	return b.String()
}

// skipEscape returns the index just past the escape sequence starting at i.
func skipEscape(s string, i int) int {
	i++ // past ESC
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[': // CSI: parameter bytes, then a final byte in @-~
		for i++; i < len(s) && (s[i] < '@' || s[i] > '~'); i++ {
		}
		return min(i+1, len(s))
	case ']': // OSC: runs to BEL, or to ST (ESC \)
		for i++; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return i
	default: // a two-byte escape
		return i + 1
	}
}
