package configui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The tea half is driven the way bubbletea drives it: key presses in through
// Update, rendered text out through View. Every test of a decision this half
// makes — the reset, the read-only and env refusals, which editor a key opens,
// the row state machine — goes through this harness, so a decision reachable
// only by calling a private method is a decision the running program cannot
// reach either.

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

// eraseField presses backspace enough times to clear the widest field the tests
// seed, so a rename types over nothing.
func eraseField(m Model, n int) Model {
	for range n {
		m = press(m, "backspace")
	}
	return m
}

// screen renders the model through View and strips the styling, so assertions
// match the text the user reads rather than the escapes around it.
func screen(m Model) string { return stripStyle(m.View().Content) }

// wantOnScreen asserts that the rendered view says each of want.
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
