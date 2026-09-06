package configui

import (
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// TestEveryCountedKeyCountsItsScopeEntries: a key rendered as a count of
// entries must count them in a scope file too — a countable key whose
// descriptor points the count at the wrong node in that file would show a bare
// "0" in the "in <scope>" line. One entry each, so the singular label is
// exercised, and worktree is the case that proves a nested list is reached.
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
	got, err := scopeStates(path)
	if err != nil {
		t.Fatalf("scopeStates: %v", err)
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

// TestNestedCollectionCountsTheFieldItPresents: worktree is the key whose
// entries do not live on its own node — the UI presents its seed list, and the
// row says so with scopeEntries. One entry each is not enough to catch a count
// taken from the wrong node: `worktree: {seed: [x]}` holds one pair *and* one
// seed, so both readings agree. Two seeds under the one mapping key tell them
// apart.
func TestNestedCollectionCountsTheFieldItPresents(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".toolbox.yaml")
	writeFile(t, path, "worktree:\n  seed: [.env.local, .env.test]\n")

	got, err := scopeStates(path)
	if err != nil {
		t.Fatalf("scopeStates: %v", err)
	}
	want := countLabel(2, keyDescriptors["worktree"].noun)
	if got["worktree"].display != want {
		t.Errorf("per-scope display of worktree = %q, want %q — the count must come "+
			"from the seed list, not from the worktree mapping", got["worktree"].display, want)
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
// this test now. The sweep runs in the repo scope — the one scope every key is
// writable in; the global layer's per-key refusal is
// TestOpenEditorRefusesWorkspaceOnlyKeyInGlobalScope's to cover.
func TestEveryEditableKeyOpensAnEditor(t *testing.T) {
	for _, key := range Keys() {
		m := press(browsing(ScopeRepo, &config.Config{},
			KeyState{Key: key, ReadOnly: ReadOnlyKey(key)}), "enter")
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

// TestEveryEditorKindIsSeededDrawnAndDriven: an editor kind is declared in
// three places — the seed that opens it (editorSeeds, indexed by kind), the
// pane that draws it (renderEditor) and the reducer that takes its keys
// (updateEditing). Only the first is a table; the other two are switches, so a
// kind added without its case opens an editor that draws a bare title or
// swallows every key, with a green suite. This is that guard.
//
// The pane is proved by its footer: every kind's renderer emits one naming
// `esc`, and nothing else on screen does while an editor is open. The reducer
// is proved by the kind's own key, which each row below names — a new kind
// fails the completeness check first and has to declare one.
func TestEveryEditorKindIsSeededDrawnAndDriven(t *testing.T) {
	// key: a key whose descriptor declares the kind. press: a key press that
	// kind's reducer must act on. after: what the pane shows once it has.
	drivers := map[editorKind]struct{ key, press, after string }{
		edEnum:   {"pull", "down", "> always"},
		edString: {"image", "x", "x"},
		edTri:    {"bridge", "down", "> true"},
		edMulti:  {"sdd", "space", "[x]"},
		edRows:   {"env", "a", "key:"},
	}
	if len(drivers) != len(editorSeeds) {
		t.Fatalf("editorSeeds holds %d kinds and this table %d — a new editor kind needs a row here, "+
			"and cases in renderEditor and updateEditing", len(editorSeeds), len(drivers))
	}
	for kind, d := range drivers {
		if _, ok := editorSeeds[kind]; !ok {
			t.Errorf("kind %d has no seed", kind)
			continue
		}
		// The kind is the row's, not the descriptor's: internal/config declares
		// which editor a key opens, and this table has to drive that one.
		row, ok := config.KeyByName(d.key)
		if !ok {
			t.Fatalf("key %q is not a schema key", d.key)
		}
		if row.Editor != kind {
			t.Fatalf("key %q declares kind %d, not the %d this row drives", d.key, row.Editor, kind)
		}
		m := press(browsing(ScopeRepo, &config.Config{}, KeyState{Key: d.key, ScopeSet: true}), "enter")
		if !m.editing {
			t.Errorf("kind %d opened no editor for %q (status %q)", kind, d.key, m.status)
			continue
		}
		// A kind missing from renderEditor draws the title and nothing else.
		wantOnScreen(t, m, "esc")
		wantOnScreen(t, press(m, d.press), d.after)
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
		m := press(browsing(ScopeRepo, &config.Config{}, KeyState{Key: key}), "enter")
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
