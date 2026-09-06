package configui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/fsx"
)

// previewLines renders the pending edit's diff against an existing document —
// the assertion surface for what the preview panel claims.
func previewLines(t *testing.T, m Model, base []byte) []previewLine {
	t.Helper()
	lines, err := previewDiff("preview.yaml", baseDoc{bytes: base, exists: true}, m.pendingMutator())
	if err != nil {
		t.Fatalf("previewDiff: %v", err)
	}
	return lines
}

// previewText is previewLines flattened into `- `/`+ ` marked text.
func previewText(t *testing.T, m Model, base []byte) string {
	t.Helper()
	return markedText(previewLines(t, m, base))
}

func markedText(lines []previewLine) string {
	var b strings.Builder
	for _, l := range lines {
		marker := "- "
		if l.Added {
			marker = "+ "
		}
		b.WriteString(marker + l.Text + "\n")
	}
	return b.String()
}

// editorFor opens key's editor by key press over cfg, so a preview is always
// taken from editor state the program can actually be in. The target is left
// empty: these tests supply the "before" document themselves.
func editorFor(t *testing.T, key string, cfg *config.Config) Model {
	t.Helper()
	m := press(browsing(ScopeRepo, cfg, KeyState{Key: key, ScopeSet: true}), "enter")
	if !m.editing {
		t.Fatalf("%q opened no editor (status %q)", key, m.status)
	}
	return m
}

// disabledMount is the default mount the multi-select tests check. Taken from
// the option set the UI offers rather than spelled out, so the fixture cannot
// name a mount that is not on the list.
func disabledMount() string { return DefaultMountNames()[0] }

// mountsEditor is the editor state reached by checking the first default mount
// in the multi-select — opened and toggled through the key stream.
func mountsEditor(t *testing.T) Model {
	t.Helper()
	m := editorFor(t, "mounts", &config.Config{})
	return press(chooseOption(t, m, disabledMount()), "space")
}

// The mounts editor's checkboxes select the default mounts to *disable*
// (openEditor seeds them from DisabledMounts), and the writer records that as a
// `{name, disabled: true}` patch. A preview that rendered the selection as a
// bare `mounts:` list claimed the inverse of the pending edit — that the checked
// mount was the only one kept.
func TestPreviewMountsShowsDisablePatch(t *testing.T) {
	lines := previewLines(t, mountsEditor(t), []byte("pull: never\n"))

	got := markedText(lines)
	if !strings.Contains(got, "name: "+disabledMount()) || !strings.Contains(got, "disabled: true") {
		t.Errorf("preview does not show the disable patch the writer produces:\n%s", got)
	}
	// A bare sequence item is the inverting shape. Compare on the trimmed line
	// rather than a marked substring: the item's own indentation depends on the
	// encoder, and baking it into the needle is how this assertion goes inert.
	for _, l := range lines {
		if l.Added && strings.TrimSpace(l.Text) == "- "+disabledMount() {
			t.Errorf("preview lists %s as a kept mount, inverting the edit:\n%s", disabledMount(), got)
		}
	}
}

// A disable patch merged into a richer user entry is the case a hand-built
// fragment could never describe: the result depends on what the file already
// holds, so the preview has to render the mutation against the real document.
func TestPreviewMountsKeepsRicherUserEntry(t *testing.T) {
	base := []byte("mounts:\n  - name: " + disabledMount() + "\n    source: /host/keep\n    target: /in/box\n")

	got := previewText(t, mountsEditor(t), base)
	if strings.Contains(got, "- source: /host/keep") {
		t.Errorf("preview claims the user's source override is dropped:\n%s", got)
	}
	if !strings.Contains(got, "disabled: true") {
		t.Errorf("preview must show disabled: true added to the existing entry:\n%s", got)
	}
}

// Re-selecting the value already on disk writes nothing, and the panel has to
// say so: an unchanged fragment would imply a pending change that does not exist.
func TestPreviewReportsNoChangeForAnIdenticalEdit(t *testing.T) {
	base := []byte("pull: never\n")
	m := chooseOption(t, editorFor(t, "pull", &config.Config{Pull: config.PullNever}), "never")

	if got := previewText(t, m, base); got != "" {
		t.Errorf("re-selecting the active value must diff to nothing, got:\n%s", got)
	}
}

// A target that does not exist yet is created carrying the docs header, so the
// preview has to account for it two ways: rendering the mutation alone
// under-reports the write by exactly those header lines, and rendering an empty
// document as the "before" side puts a `{}` removal in the panel that no file
// ever held.
func TestPreviewOnAbsentTargetShowsWholeCreate(t *testing.T) {
	m := chooseOption(t, editorFor(t, "pull", &config.Config{}), "never")

	lines, err := previewDiff("new.yaml", baseDoc{}, m.pendingMutator())
	if err != nil {
		t.Fatalf("previewDiff: %v", err)
	}
	var b strings.Builder
	for _, l := range lines {
		if !l.Added {
			t.Errorf("an absent target has no before side, got removal %q", l.Text)
		}
		b.WriteString(l.Text + "\n")
	}
	got := b.String()
	if !strings.Contains(got, "# .toolbox.yaml") {
		t.Errorf("preview must include the docs header the create writes:\n%s", got)
	}
	if !strings.Contains(got, "pull: never") {
		t.Errorf("preview must include the edit itself:\n%s", got)
	}
}

// A file holding only comments parses to a document with no keys, so the write
// drops those comments. The panel has to name the lines it actually removes: a
// re-rendered before side reported them as `- {}`, a token no file ever held,
// while silently omitting that the user's comments go away. This shape is
// production-reachable — EnsureTargetFile leaves exactly it behind when the
// $EDITOR escape creates the target.
func TestPreviewOnCommentOnlyTargetNamesTheLinesItRemoves(t *testing.T) {
	base := []byte("# hand-written notes\n# second line\n")
	m := chooseOption(t, editorFor(t, "pull", &config.Config{}), "never")

	got := previewText(t, m, base)
	if strings.Contains(got, "{}") {
		t.Errorf("preview must not describe a keyless document as {}:\n%s", got)
	}
	if !strings.Contains(got, "- # hand-written notes") {
		t.Errorf("preview must report the comment lines the write drops:\n%s", got)
	}
}

// The write replaces the file with the encoder's output wholesale, so
// normalisation is part of the edit: a blank line between blocks does not
// survive it. The panel has to report that. Re-rendering the before side would
// cancel it out and hide a change the user is about to get — which is the reason
// the before side is the file's own bytes.
func TestPreviewShowsNormalisationTheWritePerforms(t *testing.T) {
	base := []byte("# global\npull: always\n\nagent: claude\n")
	m := typeText(editorFor(t, "image", &config.Config{}), "ghcr.io/acme/box:v1")

	lines := previewLines(t, m, base)
	blankRemoved := false
	for _, l := range lines {
		if !l.Added && l.Text == "" {
			blankRemoved = true
		}
	}
	if !blankRemoved {
		t.Errorf("preview must report the blank line the write drops:\n%s", markedText(lines))
	}
	if !strings.Contains(markedText(lines), "+ image: ghcr.io/acme/box:v1") {
		t.Errorf("preview must show the edit itself:\n%s", markedText(lines))
	}
}

// The pending-change panel is the link every previewDiff test skips: it is what
// carries openEditor's reading of the target into the diff. With the tests all
// calling previewDiff directly and passing exists themselves, that link could be
// broken — or hardcoded — and the fresh-target header regression would come back
// on the only surface a user sees. So this one is read off the screen.
// Both target states, because only the pair pins the link: an absent target is
// created carrying the docs header, so the panel shows it, and an existing one
// is patched, so the panel must not. A baseline that ignored what openEditor
// read would still get the absent case right — it is the existing case that
// catches it.
func TestPreviewPanelCarriesTheTargetStateFromOpenEditor(t *testing.T) {
	tc := previewCase{key: "pull", open: chooseValue("never")}

	fresh, _ := openedEditor(t, tc, false)
	if fresh.previewBase.exists {
		t.Error("openEditor must report an absent target as not existing")
	}
	wantOnScreen(t, fresh, "pending change", "# .toolbox.yaml", "+ pull: never")

	existing, _ := openedEditor(t, tc, true)
	if !existing.previewBase.exists {
		t.Error("openEditor must report a present target as existing")
	}
	wantOnScreen(t, existing, "pending change", "+ pull: never")
	notOnScreen(t, existing, "# .toolbox.yaml")
}

// The panel's no-change branch. An editor opens with its cursor already on the
// current effective value, so saving straight away writes nothing — and the
// panel must say so rather than show an unchanged fragment.
func TestPreviewPanelReportsNoChangeForAnUntouchedEditor(t *testing.T) {
	// The fixture sets agent: claude, so the freshly opened cursor sits on it.
	m, _ := openedEditor(t, previewCase{key: "agent", open: leaveAsOpened}, true)

	wantOnScreen(t, m, "pending change", "no change")
	notOnScreen(t, m, "+ agent:")
}

// previewCase is one editable key: the editor state a user would reach, and the
// pending value it carries.
type previewCase struct {
	key string
	// open drives the editor to its pending value with key presses, so the
	// mutation compared below is one a user could actually have produced.
	open func(t *testing.T, m Model) Model
	// save performs the same edit through apply, naming the mutator and its
	// arguments by hand. It is the independent oracle: the preview comes from the
	// model's own dispatch, so comparing the two catches a dispatch that reaches
	// for the wrong mutation — the defect class behind the inverted mounts
	// preview.
	save    func(m Model) error
	inCfg   func(cfg *config.Config)        // seeds the effective config the editor reads
	prepare func(t *testing.T, home string) // host state Doctor requires for the value
}

// The four ways a user reaches a pending value, one per editor kind. They press
// keys and nothing else: a test that pokes m.ed directly proves the mutation is
// right for state the key stream may never produce.

// chooseValue walks a bounded list onto an option.
func chooseValue(want string) func(*testing.T, Model) Model {
	return func(t *testing.T, m Model) Model { return chooseOption(t, m, want) }
}

// typeValue clears a free-text field and types a new value into it.
func typeValue(text string) func(*testing.T, Model) Model {
	return func(_ *testing.T, m Model) Model {
		return typeText(eraseField(m, len(m.ed.input.Value())), text)
	}
}

// checkFirstOption ticks the first checkbox of a multi-select.
func checkFirstOption(t *testing.T, m Model) Model {
	return press(chooseOption(t, m, m.ed.options[0]), "space")
}

// addRow adds one row to a collection editor, one column per argument.
func addRow(cols ...string) func(*testing.T, Model) Model {
	return func(_ *testing.T, m Model) Model {
		m = press(m, "a")
		for _, col := range cols {
			m = press(typeText(m, col), "enter")
		}
		return m
	}
}

// leaveAsOpened is the empty edit: whatever the editor opened with.
func leaveAsOpened(_ *testing.T, m Model) Model { return m }

// requireHostAuthPath creates the host credential directory of an
// inherit_host_auth key, which Doctor requires to exist before the key may name
// it. The path comes from the catalog so the fixture cannot drift from the
// whitelist it is validated against.
func requireHostAuthPath(t *testing.T, home, key string) {
	t.Helper()
	entry, ok := catalog.Find(key)
	if !ok || entry.HostAuthMount == nil {
		t.Fatalf("catalog has no host-auth mount for %q", key)
	}
	mkdirAll(t, fsx.ExpandTilde(entry.HostAuthMount.HostPath, home))
}

// TestPreviewMatchesWriterForEveryEditableKey is the permanent net over the
// defect class: for every key with an editor, what the preview says will be
// written must be exactly what the key's own mutator puts on disk, and the
// model's own save path must land on the same bytes. The preview and the writers
// used to be independent models of the same mutation, indexed on different axes
// (editor kind vs key), and they diverged wherever those axes failed to
// coincide — mounts inverted the edit, sdd showed a list where a map is written,
// shells under-reported the env block the writer preserves.
//
// The read-only `shell` key is excluded: openEditor refuses it, so it has no
// pending mutation to preview.
func TestPreviewMatchesWriterForEveryEditableKey(t *testing.T) {
	cases := []previewCase{
		{
			key:  "agent",
			open: chooseValue("codex"),
			save: func(m Model) error { return apply(m.target, m.cwd, configedit.Scalar("agent", "codex")) },
		},
		{
			key:  "pull",
			open: chooseValue("never"),
			save: func(m Model) error { return apply(m.target, m.cwd, configedit.Scalar("pull", "never")) },
		},
		{
			key:  "image",
			open: typeValue("ghcr.io/acme/box:v1"),
			save: func(m Model) error { return apply(m.target, m.cwd, configedit.Scalar("image", "ghcr.io/acme/box:v1")) },
		},
		{
			key:  "registry_mirror",
			open: typeValue("mirror.example.com"),
			save: func(m Model) error {
				return apply(m.target, m.cwd, configedit.Scalar("registry_mirror", "mirror.example.com"))
			},
		},
		{
			key:  "mounts_root",
			open: typeValue("/tmp/roots"),
			save: func(m Model) error { return apply(m.target, m.cwd, configedit.Scalar("mounts_root", "/tmp/roots")) },
		},
		{
			key:  "bridge",
			open: chooseValue("false"),
			save: func(m Model) error { return apply(m.target, m.cwd, configedit.Bool("bridge", boolPtr(false))) },
		},
		{
			key:  "proximo",
			open: chooseValue("true"),
			save: func(m Model) error { return apply(m.target, m.cwd, configedit.Bool("proximo", boolPtr(true))) },
		},
		{
			key:  "managed_statusline",
			open: chooseValue("false"),
			save: func(m Model) error {
				return apply(m.target, m.cwd, configedit.Bool("managed_statusline", boolPtr(false)))
			},
		},
		{
			key:  "image_reclaim",
			open: chooseValue("false"),
			save: func(m Model) error {
				return apply(m.target, m.cwd, configedit.Bool("image_reclaim", boolPtr(false)))
			},
		},
		{
			key:  "peer_messaging",
			open: chooseValue("true"),
			save: func(m Model) error { return apply(m.target, m.cwd, configedit.Bool("peer_messaging", boolPtr(true))) },
		},
		{
			key:  "inherit_host_auth",
			open: checkFirstOption,
			save: func(m Model) error {
				return apply(m.target, m.cwd, configedit.StringList("inherit_host_auth", []string{HostAuthOptions()[0]}))
			},
			prepare: func(t *testing.T, home string) { requireHostAuthPath(t, home, HostAuthOptions()[0]) },
		},
		{
			key:  "sdd",
			open: checkFirstOption,
			save: func(m Model) error { return SaveSDD(m.target, m.cwd, map[string]bool{SDDOptions()[0]: true}) },
		},
		{
			key:  "mounts",
			open: checkFirstOption,
			save: func(m Model) error {
				return apply(m.target, m.cwd, configedit.MountsDisabled(map[string]bool{DefaultMountNames()[0]: true}))
			},
		},
		{
			key:  "env",
			open: addRow("REGION", "eu"),
			save: func(m Model) error {
				return apply(m.target, m.cwd, configedit.StringMap("env", map[string]string{"REGION": "eu"}))
			},
		},
		{
			key:  "worktree",
			open: addRow(".env.local"),
			save: func(m Model) error { return apply(m.target, m.cwd, configedit.WorktreeSeed([]string{".env.local"})) },
		},
		{
			key:   "shells",
			open:  addRow("prod", "/repo/prod"),
			inCfg: func(cfg *config.Config) { cfg.Shells = map[string]config.NamedShell{"prod": {Path: "/repo/old"}} },
			save: func(m Model) error {
				return apply(m.target, m.cwd, configedit.Shells([]ShellEntry{{Name: "prod", Path: "/repo/prod", OrigName: "prod"}}))
			},
		},
	}

	// Every editable key must be exercised, so a key that gains an editor
	// without a preview case is caught here rather than shipping unguarded.
	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.key] = true
	}
	for _, key := range Keys() {
		if ReadOnlyKey(key) || covered[key] {
			continue
		}
		t.Errorf("key %q has an editor but no preview case", key)
	}

	// Both target states, because they take different write paths: an absent
	// .toolbox.yaml is created carrying the docs header, so it is the one case
	// where the preview could under-report the write by whole lines.
	for _, state := range []struct {
		name   string
		exists bool
	}{
		{name: "existing-target", exists: true},
		{name: "fresh-target", exists: false},
	} {
		for _, tc := range cases {
			t.Run(tc.key+"/"+state.name, func(t *testing.T) {
				m, target := openedEditor(t, tc, state.exists)
				claimed, err := configedit.Render(target, m.previewBase.bytes, m.previewBase.exists, m.pendingMutator())
				if err != nil {
					t.Fatalf("render preview: %v", err)
				}

				// What the key's own mutation produces, driven with hand-written
				// arguments rather than the model's dispatch.
				if err := tc.save(m); err != nil {
					t.Fatalf("oracle mutation: %v", err)
				}
				if got := readFile(t, target); got != string(claimed) {
					t.Errorf("preview does not describe what the mutation writes.\npreview:\n%s\nwritten:\n%s", claimed, got)
				}

				// And the model's own save path must land on those same bytes.
				m2, target2 := openedEditor(t, tc, state.exists)
				if err := m2.saveEdit(); err != nil {
					t.Fatalf("saveEdit: %v", err)
				}
				if got := readFile(t, target2); got != string(claimed) {
					t.Errorf("saveEdit does not write what the preview shows.\npreview:\n%s\nsaveEdit:\n%s", claimed, got)
				}
			})
		}
	}
}

func boolPtr(v bool) *bool { return &v }

// openedEditor builds a Model over an isolated repo, opens tc.key's editor and
// applies the pending value. targetExists selects whether the repo already has a
// .toolbox.yaml — a fresh target is created with the docs header, which the
// preview has to account for.
func openedEditor(t *testing.T, tc previewCase, targetExists bool) (Model, string) {
	t.Helper()
	home := isolatedHome(t)
	if tc.prepare != nil {
		tc.prepare(t, home)
	}
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	if targetExists {
		writeFile(t, target, "# existing config\nagent: claude\n")
	}

	cfg, _, err := Snapshot(repo, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if tc.inCfg != nil {
		tc.inCfg(cfg)
	}

	m := press(Model{cwd: repo, scope: ScopeRepo, cfg: cfg, target: target,
		states: []KeyState{{Key: tc.key, ScopeSet: true}}}, "enter")
	if !m.editing {
		t.Fatalf("enter did not open an editor for %q (status: %s)", tc.key, m.status)
	}
	return tc.open(t, m), target
}
