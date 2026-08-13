package configedit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// render applies a Mutator to src and returns the document it produces. The
// mutators are pure node edits, so they are testable on bytes alone — no temp
// file, no config layer, and none of the host-state fixtures the Doctor-gated
// write path needs before it will accept a value.
func render(t *testing.T, src string, m Mutator) string {
	t.Helper()
	out, err := Render("test.yaml", []byte(src), true, m)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(out)
}

func TestScalarWritesAndEmptyRemoves(t *testing.T) {
	got := render(t, "# keep me\npull: always\n", Scalar("agent", "codex"))
	for _, want := range []string{"# keep me", "pull: always", "agent: codex"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if got := render(t, "agent: codex\n", Scalar("agent", "")); strings.Contains(got, "agent") {
		t.Errorf("an empty value must remove the key:\n%s", got)
	}
}

// A tri-state bool distinguishes false from unset: "unset" carries its own
// meaning (proximo unset = auto-detect), so it must not persist as false.
func TestBoolTriState(t *testing.T) {
	no := false
	if got := render(t, "", Bool("bridge", &no)); !strings.Contains(got, "bridge: false") {
		t.Errorf("want bridge: false, got:\n%s", got)
	}
	if got := render(t, "bridge: false\n", Bool("bridge", nil)); strings.Contains(got, "bridge") {
		t.Errorf("nil must remove the key, got:\n%s", got)
	}
}

func TestStringListReplacesWholesaleAndEmptyRemoves(t *testing.T) {
	got := render(t, "inherit_host_auth:\n  - stale\n", StringList("inherit_host_auth", []string{"claude", "gh"}))
	if strings.Contains(got, "stale") {
		t.Errorf("the list must be replaced, not appended to:\n%s", got)
	}
	if !strings.Contains(got, "claude") || !strings.Contains(got, "gh") {
		t.Errorf("list entries missing:\n%s", got)
	}
	if got := render(t, got, StringList("inherit_host_auth", nil)); strings.Contains(got, "inherit_host_auth") {
		t.Errorf("an empty list must remove the key:\n%s", got)
	}
}

func TestStringMapIsSortedAndEmptyRemoves(t *testing.T) {
	got := render(t, "", StringMap("env", map[string]string{"FOO": "bar", "BAZ": "qux"}))
	if !strings.Contains(got, "FOO: bar") || !strings.Contains(got, "BAZ: qux") {
		t.Errorf("env pairs missing:\n%s", got)
	}
	if strings.Index(got, "BAZ") > strings.Index(got, "FOO") {
		t.Errorf("keys must be written in sorted order for a deterministic file:\n%s", got)
	}
	if got := render(t, got, StringMap("env", nil)); strings.Contains(got, "env") {
		t.Errorf("an empty map must remove the key:\n%s", got)
	}
}

// worktree exists only to hold seed today, so emptying seed must not leave an
// orphan block behind.
func TestWorktreeSeedNestsAndEmptyDropsTheBlock(t *testing.T) {
	got := render(t, "", WorktreeSeed([]string{".env", "openspec"}))
	if !strings.Contains(got, "worktree:") || !strings.Contains(got, "seed:") {
		t.Errorf("worktree.seed must be nested:\n%s", got)
	}
	if got := render(t, got, WorktreeSeed(nil)); strings.Contains(got, "worktree") {
		t.Errorf("an empty seed must drop the worktree block:\n%s", got)
	}
	// A sibling key means the block is still wanted.
	kept := render(t, "worktree:\n  seed:\n    - .env\n  other: keep\n", WorktreeSeed(nil))
	if !strings.Contains(kept, "other: keep") {
		t.Errorf("a worktree block with other keys must survive:\n%s", kept)
	}
}

func TestShellsPreservesEnvOfKeptEntryAndDropsRemoved(t *testing.T) {
	src := "shells:\n  infra:\n    path: /repo/infra\n    env:\n      REGION: eu\n  old:\n    path: /repo/old\n"
	got := render(t, src, Shells([]ShellEntry{{Name: "infra", Path: "/repo/infra", OrigName: "infra"}}))
	if !strings.Contains(got, "REGION: eu") {
		t.Errorf("a kept shell's env overlay must survive a path write:\n%s", got)
	}
	if strings.Contains(got, "old:") {
		t.Errorf("a shell no entry names must be removed:\n%s", got)
	}
	if got := render(t, src, Shells(nil)); strings.Contains(got, "shells") {
		t.Errorf("an empty entry set must remove the block:\n%s", got)
	}
}

// A rename cannot read the env overlay off the new name, so the caller carries
// it; without that, renaming a shell silently dropped its env.
func TestShellsRenameCarriesEnvToTheNewName(t *testing.T) {
	got := render(t, "shells:\n  infra:\n    path: /repo/infra\n    env:\n      REGION: eu\n",
		Shells([]ShellEntry{{Name: "prod", Path: "/repo/infra", OrigName: "infra", Env: map[string]string{"REGION": "eu"}}}))
	if strings.Contains(got, "infra:") {
		t.Errorf("the old name must be gone:\n%s", got)
	}
	if !strings.Contains(got, "prod:") || !strings.Contains(got, "REGION: eu") {
		t.Errorf("the rename must carry path and env across:\n%s", got)
	}
}

// The loaded config is keyed by viper's lowercased name, so an entry a file
// spells `Infra:` reaches the caller as `infra` and comes back as an entry
// under that name. Matching the two literally dropped the file's entry as
// "unwanted" and re-created it — silently taking its env overlay with it.
func TestShellsEditsAnExistingKeySpellingInPlace(t *testing.T) {
	src := "shells:\n  Infra:\n    path: /repo/infra\n    env:\n      REGION: eu\n"
	got := render(t, src, Shells([]ShellEntry{{Name: "infra", Path: "/repo/infra", OrigName: "infra"}}))

	if !strings.Contains(got, "REGION: eu") {
		t.Errorf("the env overlay of the entry being edited must survive:\n%s", got)
	}
	if strings.Contains(got, "\n  infra:") {
		t.Errorf("a second key must not appear beside Infra::\n%s", got)
	}
	if !strings.Contains(got, "Infra:") {
		t.Errorf("the file's own spelling must be kept:\n%s", got)
	}
}

// A new entry is written under the canonical key: the one the loader will look
// it up under, so `config ui` cannot create a shell its own next read misses.
func TestShellsWritesNewEntryUnderTheCanonicalKey(t *testing.T) {
	got := render(t, "", Shells([]ShellEntry{{Name: " Infra ", Path: "/repo/infra"}}))
	if !strings.Contains(got, "shells:\n  infra:\n") {
		t.Errorf("want the canonical key shells.infra:\n%s", got)
	}
}

func TestShellKeyIn(t *testing.T) {
	existing := []string{"Infra", "qa"}
	cases := []struct{ name, want string }{
		{"infra", "Infra"},   // existing spelling wins
		{" INFRA ", "Infra"}, // …however the caller typed it
		{"qa", "qa"},
		{"New Name", "new name"}, // unknown: canonical form
	}
	for _, c := range cases {
		if got := ShellKeyIn(existing, c.name); got != c.want {
			t.Errorf("ShellKeyIn(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMountsDisabledPatchesAndReEnablesCleanly(t *testing.T) {
	name := DefaultMountNames()[0]
	got := render(t, "", MountsDisabled(map[string]bool{name: true}))
	if !strings.Contains(got, "name: "+name) || !strings.Contains(got, "disabled: true") {
		t.Errorf("want a disable patch for %s:\n%s", name, got)
	}
	if got := render(t, got, MountsDisabled(map[string]bool{name: false})); strings.Contains(got, name) {
		t.Errorf("re-enabling must drop the pure disable patch entirely:\n%s", got)
	}
}

// Only a *pure* disable patch may be dropped on re-enable: a richer entry is the
// user's own override and re-enabling must not take it with them.
func TestMountsDisabledKeepsRicherUserEntry(t *testing.T) {
	name := DefaultMountNames()[0]
	src := "mounts:\n  - name: " + name + "\n    source: /custom/path\n"
	if got := render(t, src, MountsDisabled(map[string]bool{name: false})); !strings.Contains(got, "/custom/path") {
		t.Errorf("a source override must survive a re-enable:\n%s", got)
	}
	got := render(t, src, MountsDisabled(map[string]bool{name: true}))
	if !strings.Contains(got, "/custom/path") || !strings.Contains(got, "disabled: true") {
		t.Errorf("a disable must merge into the existing entry, not replace it:\n%s", got)
	}
}

func TestSDDEnabledWritesShorthandAndDropsEmptiedBlock(t *testing.T) {
	key := SDDKeys()[0]
	got := render(t, "", SDDEnabled(map[string]bool{key: true}))
	if !strings.Contains(got, key+": true") {
		t.Errorf("want the %s bool shorthand:\n%s", key, got)
	}
	if got := render(t, got, SDDEnabled(nil)); strings.Contains(got, "sdd") {
		t.Errorf("disabling every skill must drop the sdd block:\n%s", got)
	}
}

// An object-form entry already means enabled and carries the user's steps
// override, so the shorthand must not clobber it.
func TestSDDEnabledLeavesCustomStepsAlone(t *testing.T) {
	key := SDDKeys()[0]
	src := "sdd:\n  " + key + ":\n    steps:\n      - [echo, hi]\n"
	if got := render(t, src, SDDEnabled(map[string]bool{key: true})); !strings.Contains(got, "steps:") {
		t.Errorf("a custom steps override must survive staying enabled:\n%s", got)
	}
}

// A Mutator is documented as a snapshot: the constructors copy what they capture,
// so a caller that keeps editing its own state (the config UI hands over live
// editor state on every repaint) cannot change what an already-built mutation
// means.
func TestMutatorsSnapshotWhatTheyCapture(t *testing.T) {
	list := []string{"claude"}
	listMut := StringList("inherit_host_auth", list)
	list[0] = "mutated"

	pairs := map[string]string{"FOO": "bar"}
	mapMut := StringMap("env", pairs)
	pairs["FOO"] = "mutated"

	seed := []string{".env"}
	seedMut := WorktreeSeed(seed)
	seed[0] = "mutated"

	shellEnv := map[string]string{"REGION": "eu"}
	shellsMut := Shells([]ShellEntry{{Name: "prod", Path: "/p", OrigName: "old", Env: shellEnv}})
	shellEnv["REGION"] = "mutated"

	sddKey := SDDKeys()[0]
	enabled := map[string]bool{sddKey: true}
	sddMut := SDDEnabled(enabled)
	enabled[sddKey] = false

	mountName := DefaultMountNames()[0]
	disabled := map[string]bool{mountName: true}
	mountsMut := MountsDisabled(disabled)
	disabled[mountName] = false

	for name, tc := range map[string]struct {
		mut  Mutator
		want string
	}{
		"StringList":     {listMut, "claude"},
		"StringMap":      {mapMut, "FOO: bar"},
		"WorktreeSeed":   {seedMut, ".env"},
		"Shells":         {shellsMut, "REGION: eu"},
		"SDDEnabled":     {sddMut, sddKey + ": true"},
		"MountsDisabled": {mountsMut, "disabled: true"},
	} {
		got := render(t, "", tc.mut)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s did not snapshot its argument: want %q in:\n%s", name, tc.want, got)
		}
		if strings.Contains(got, "mutated") {
			t.Errorf("%s observed a later edit to the caller's collection:\n%s", name, got)
		}
	}
}

// Render must return exactly the bytes Upsert writes — the property the config
// UI's preview rests on. The header is the part that is easy to get wrong: it
// comes from the file's absence, not from its content, so an empty or
// comment-only file must NOT gain one.
func TestRenderReturnsTheBytesUpsertWrites(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		absent  bool
	}{
		{name: "absent", absent: true},
		{name: "empty", content: ""},
		{name: "whitespace only", content: "\n\n"},
		{name: "comments only", content: "# hand-written\n"},
		{name: "populated", content: "# keep me\npull: always\n"},
		{name: "hand indented", content: "mounts:\n    - name: claude\n      disabled: true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".toolbox.yaml")
			var src []byte
			if !tc.absent {
				if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
				src = []byte(tc.content)
			}

			want, err := Render(path, src, !tc.absent, Scalar("agent", "codex"))
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if _, err := Upsert(path, Scalar("agent", "codex")); err != nil {
				t.Fatalf("Upsert: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("Render and Upsert disagree.\nRender:\n%s\nUpsert:\n%s", want, got)
			}
			if hasHeader := strings.Contains(string(got), "Run 'toolbox config example'"); hasHeader != tc.absent {
				t.Errorf("header present = %v, want %v (only a created file gains one):\n%s", hasHeader, tc.absent, got)
			}
		})
	}
}
