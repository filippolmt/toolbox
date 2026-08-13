package configui

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// TestKeyDescriptorsCoverEveryKey is the anti-drift guard the per-key switches
// never had: a schema key added without a descriptor row used to surface as a
// blank TUI row (or a runtime status message) with a green suite. It now fails
// here, naming the key.
func TestKeyDescriptorsCoverEveryKey(t *testing.T) {
	for _, key := range Keys() {
		if _, ok := keyDescriptors[key]; !ok {
			t.Errorf("key %q has no descriptor row — the TUI would render it blank", key)
		}
	}
	for key := range keyDescriptors {
		if !slices.Contains(Keys(), key) {
			t.Errorf("descriptor row %q is not a UI key", key)
		}
	}
}

// TestEveryCountedKeyCountsItsScopeEntries: a key rendered as a count of
// entries must count them in a scope file too — a countable key whose descriptor
// forgets how to count a yaml node would show the raw (empty) node value in the
// "in <scope>" line. One entry each, so the singular label is exercised.
func TestEveryCountedKeyCountsItsScopeEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".toolbox.yaml")
	writeFile(t, path, `mounts:
  - name: ssh
    disabled: true
inherit_host_auth: [claude]
shells:
  work: /bin/zsh
sdd:
  gsd: true
env:
  REGION: eu
worktree:
  seed: [.env.local]
`)
	got, err := ScopeStates(path)
	if err != nil {
		t.Fatalf("ScopeStates: %v", err)
	}
	for _, key := range Keys() {
		d := keyDescriptors[key]
		if d.noun == "" {
			continue
		}
		want := countLabel(1, d.noun)
		if got[key].display != want {
			t.Errorf("per-scope display of %q = %q, want %q", key, got[key].display, want)
		}
	}
}

// TestEveryCollectionKeyListsItsEntries: a key whose list row is a count must
// name those entries in the detail pane — a count with no names behind it is
// the row a missing descriptor field produces.
func TestEveryCollectionKeyListsItsEntries(t *testing.T) {
	cfg := &config.Config{
		Mounts:          []config.Mount{{Name: "ssh", Disabled: true}},
		InheritHostAuth: []string{"claude"},
		Shells:          map[string]config.NamedShell{"work": {Path: "/bin/zsh"}},
		SDD:             map[string]config.SDDSkill{"gsd": {Enabled: true}},
		Env:             map[string]string{"REGION": "eu"},
		Worktree:        config.WorktreeConfig{Seed: []string{".env.local"}},
	}
	want := map[string]string{
		"mounts": "ssh", "inherit_host_auth": "claude", "shells": "work",
		"sdd": "gsd", "env": "REGION", "worktree": ".env.local",
	}
	for _, key := range Keys() {
		if keyDescriptors[key].noun == "" {
			continue
		}
		got := detailEntries(cfg, key)
		if len(got) != 1 || got[0] != want[key] {
			t.Errorf("detailEntries(%q) = %v, want [%s]", key, got, want[key])
		}
	}
}

// TestEveryEditableKeyOpensAnEditor: enter on any key but the read-only one
// must open a seeded editor. The old key-switch answered a key it did not list
// with a status message ("no interactive editor yet") at runtime; that hole is
// this test now.
func TestEveryEditableKeyOpensAnEditor(t *testing.T) {
	for _, key := range Keys() {
		m := &Model{cfg: &config.Config{}, states: []KeyState{{Key: key, ReadOnly: ReadOnlyKey(key)}}}
		m.openEditor()
		if m.states[0].ReadOnly {
			if m.editing {
				t.Errorf("read-only key %q must not open an editor", key)
			}
			continue
		}
		if !m.editing || m.ed.kind == edNone {
			t.Errorf("key %q opened no editor (status %q)", key, m.status)
			continue
		}
		if m.ed.key != key {
			t.Errorf("editor opened for %q, want %q", m.ed.key, key)
		}
		switch m.ed.kind {
		case edEnum, edTri, edMulti:
			if len(m.ed.options) == 0 {
				t.Errorf("key %q opened a choice editor with no options", key)
			}
		}
		if m.ed.kind == edMulti && m.ed.selected == nil {
			t.Errorf("key %q opened a multi-select with a nil selection set", key)
		}
	}
}

// TestEveryOpenEditorHasAPendingMutation: an editor that opens must have a
// writer behind it, or the save reports "no writer for key" after the user has
// typed the edit. Preview and save both read this one value, so a key covered
// here cannot describe one change and write another.
func TestEveryOpenEditorHasAPendingMutation(t *testing.T) {
	for _, key := range Keys() {
		if ReadOnlyKey(key) {
			continue
		}
		m := &Model{cfg: &config.Config{}, states: []KeyState{{Key: key}}}
		m.openEditor()
		if m.pendingMutator() == nil {
			t.Errorf("key %q opens an editor with no pending mutation behind it", key)
		}
	}
}

// TestEveryKeyDisplaysSomething: the key list must never show an empty value
// cell, for a wholly unset config as much as for a populated one — an empty
// display is exactly what an unmatched key used to produce.
func TestEveryKeyDisplaysSomething(t *testing.T) {
	for name, cfg := range map[string]*config.Config{
		"unset":   {},
		"planned": {Shell: "zsh", Pull: config.PullAuto},
	} {
		for _, key := range Keys() {
			if got := displayValue(cfg, key); got == "" {
				t.Errorf("[%s] displayValue(%q) is empty", name, key)
			}
		}
	}
}
