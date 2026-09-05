package configui

import (
	"os"
	"path/filepath"
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
	entries := m.ed.shellEntries(m.cfg)
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want 1", entries)
	}
	e := entries[0]
	if e.Name != "prod" || e.OrigName != "infra" || e.Env["REGION"] != "eu" {
		t.Errorf("rename entry must carry orig+env, got %+v", e)
	}
}

// TestOpenEditorRefusesWorkspaceOnlyKeyInGlobalScope: sdd's effect is anchored
// to the workspace, so the structured editor must not write it into the global
// layer. The row still displays (a hand-written global flag stays legal and
// visible, and the $EDITOR escape still reaches it) — enter refuses, and the
// status names both the reason and the way out.
func TestOpenEditorRefusesWorkspaceOnlyKeyInGlobalScope(t *testing.T) {
	m := &Model{scope: ScopeGlobal, states: []KeyState{{Key: "sdd"}}}
	m.openEditor()
	if m.editing {
		t.Error("sdd must not open an editor in the global scope")
	}
	if !strings.Contains(m.status, ScopeGlobal.String()) {
		t.Errorf("status should name the refused scope, got %q", m.status)
	}
	if !strings.Contains(m.status, "tab") {
		t.Errorf("status should point at the way out, got %q", m.status)
	}
}

// TestOpenEditorAllowsWorkspaceOnlyKeyInRepoScope is the other half: the guard
// is per-scope, not a blanket read-only marking of the key.
func TestOpenEditorAllowsWorkspaceOnlyKeyInRepoScope(t *testing.T) {
	m := &Model{scope: ScopeRepo, cfg: &config.Config{}, states: []KeyState{{Key: "sdd", ScopeSet: true}}}
	m.openEditor()
	if !m.editing {
		t.Errorf("sdd must stay editable in the repo scope, status = %q", m.status)
	}
}

// enabledSDDRepo builds a workspace with gsd enabled through the real save
// path, and returns its cwd and project-config path.
func enabledSDDRepo(t *testing.T) (cwd, target string) {
	t.Helper()
	cwd = t.TempDir()
	target = filepath.Join(cwd, ".toolbox.yaml")
	if err := SaveSDD(target, cwd, map[string]bool{"gsd": true}); err != nil {
		t.Fatalf("SaveSDD: %v", err)
	}
	return cwd, target
}

// TestResetWorkspaceOnlyKeyClearsFencesInRepoScope: dropping the flag without
// its .gitignore blocks would leave a fence nothing owns any more — the CLI has
// no uninstall, so an orphan is cleaned by hand or not at all.
func TestResetWorkspaceOnlyKeyClearsFencesInRepoScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd, target := enabledSDDRepo(t)
	if !strings.Contains(readFile(t, filepath.Join(cwd, ".gitignore")), configedit.GitignoreFenceStart("gsd")) {
		t.Fatal("fixture wrote no fence to reset")
	}

	m := &Model{cwd: cwd, target: target, scope: ScopeRepo, states: []KeyState{{Key: "sdd", ScopeSet: true}}}
	m.resetToDefault()

	if got := readFile(t, target); strings.Contains(got, "sdd") {
		t.Errorf("reset left the sdd flag behind:\n%s", got)
	}
	if got := readFile(t, filepath.Join(cwd, ".gitignore")); strings.Contains(got, configedit.GitignoreFenceStart("gsd")) {
		t.Errorf("reset orphaned the gsd fence:\n%s", got)
	}
}

// TestResetWorkspaceOnlyKeyKeepsFencesInGlobalScope: resetting a hand-written
// global flag is allowed (the guard is about creating one, not clearing it),
// but the fences belong to whichever workspaces wrote them — removing the
// current one's on behalf of a file that applies everywhere is the same
// asymmetry, reversed.
func TestResetWorkspaceOnlyKeyKeepsFencesInGlobalScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, _ := enabledSDDRepo(t)
	global := filepath.Join(home, ".toolbox.yaml")
	if err := os.WriteFile(global, []byte("sdd:\n  gsd: true\n"), 0o600); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	m := &Model{cwd: cwd, target: global, scope: ScopeGlobal, states: []KeyState{{Key: "sdd", ScopeSet: true}}}
	m.resetToDefault()

	if got := readFile(t, global); strings.Contains(got, "sdd") {
		t.Errorf("reset left the global sdd flag behind:\n%s", got)
	}
	if got := readFile(t, filepath.Join(cwd, ".gitignore")); !strings.Contains(got, configedit.GitignoreFenceStart("gsd")) {
		t.Errorf("a global reset must not touch the workspace fence:\n%s", got)
	}
	if !strings.Contains(m.status, "fence") {
		t.Errorf("status should say the fences were left alone, got %q", m.status)
	}
}

// TestResetInRepoScopeKeepsFencesAKeptGlobalFlagStillNeeds: reset removes the
// key from the selected layer only, so a hand-written global flag keeps the
// skill enabled — and a skill that still runs still needs its fence. Reset
// therefore reconciles against what the layers now resolve to, not against the
// empty set, or it produces the mirror of the orphan it exists to prevent.
func TestResetInRepoScopeKeepsFencesAKeptGlobalFlagStillNeeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd, target := enabledSDDRepo(t)
	if err := os.WriteFile(filepath.Join(home, ".toolbox.yaml"), []byte("sdd:\n  gsd: true\n"), 0o600); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	m := &Model{cwd: cwd, target: target, scope: ScopeRepo, states: []KeyState{{Key: "sdd", ScopeSet: true}}}
	m.resetToDefault()

	if got := readFile(t, target); strings.Contains(got, "sdd") {
		t.Errorf("reset left the repo sdd flag behind:\n%s", got)
	}
	if got := readFile(t, filepath.Join(cwd, ".gitignore")); !strings.Contains(got, configedit.GitignoreFenceStart("gsd")) {
		t.Errorf("gsd is still enabled by the global layer, so its fence must survive:\n%s", got)
	}
}
