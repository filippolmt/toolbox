package configui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
)

// previewText renders the pending edit's diff as plain text, marker included —
// the assertion surface for what the preview panel claims.
func previewText(t *testing.T, m Model, base []byte) string {
	t.Helper()
	lines, err := previewDiff("preview.yaml", base, m.pendingMutator())
	if err != nil {
		t.Fatalf("previewDiff: %v", err)
	}
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

// The mounts editor's checkboxes select the default mounts to *disable*
// (openEditor seeds them from DisabledMounts), and the writer records that as a
// `{name, disabled: true}` patch. A preview that rendered the selection as a
// bare `mounts:` list claimed the inverse of the pending edit — that the checked
// mount was the only one kept.
func TestPreviewMountsShowsDisablePatch(t *testing.T) {
	m := Model{ed: editor{
		key:      "mounts",
		kind:     edMulti,
		options:  []string{"claude", "gh"},
		selected: map[string]bool{"claude": true},
	}}

	got := previewText(t, m, nil)
	if !strings.Contains(got, "name: claude") || !strings.Contains(got, "disabled: true") {
		t.Errorf("preview does not show the disable patch the writer produces:\n%s", got)
	}
	if strings.Contains(got, "+ - claude") {
		t.Errorf("preview lists claude as a kept mount, inverting the edit:\n%s", got)
	}
}

// A disable patch merged into a richer user entry is the case a hand-built
// fragment could never describe: the result depends on what the file already
// holds, so the preview has to render the mutation against the real document.
func TestPreviewMountsKeepsRicherUserEntry(t *testing.T) {
	base := []byte("mounts:\n  - name: claude\n    source: /host/claude\n    target: /home/toolbox/.claude\n")
	m := Model{ed: editor{
		key:      "mounts",
		kind:     edMulti,
		options:  []string{"claude", "gh"},
		selected: map[string]bool{"claude": true},
	}}

	got := previewText(t, m, base)
	if strings.Contains(got, "- source: /host/claude") {
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
	m := Model{ed: editor{key: "pull", kind: edEnum, options: []string{"never", "always"}}}

	if got := previewText(t, m, base); got != "" {
		t.Errorf("re-selecting the active value must diff to nothing, got:\n%s", got)
	}
}

// previewCase is one editable key: the editor state a user would reach, and the
// pending value it carries.
type previewCase struct {
	key  string
	open func(m *Model)
	// save performs the same edit through the key's exported writer, named and
	// argued by hand. It is the independent oracle: the preview comes from the
	// model's own dispatch, so comparing the two catches a dispatch that reaches
	// for the wrong mutation — the defect class behind the inverted mounts
	// preview.
	save    func(m Model) error
	inCfg   func(cfg *config.Config)        // seeds the effective config the editor reads
	prepare func(t *testing.T, home string) // host state Doctor requires for the value
}

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
// written must be exactly what its writer puts on disk, and the model's own save
// path must land on the same bytes. The preview and the writers used to be
// independent models of the same mutation, indexed on different axes (editor
// kind vs key), and they diverged wherever those axes failed to coincide —
// mounts inverted the edit, sdd showed a list where a map is written, shells
// under-reported the env block the writer preserves.
//
// The read-only `shell` key is excluded: openEditor refuses it, so it has no
// pending mutation to preview.
func TestPreviewMatchesWriterForEveryEditableKey(t *testing.T) {
	cases := []previewCase{
		{
			key:  "agent",
			open: func(m *Model) { m.ed.cursor = indexOf(m.ed.options, "codex") },
			save: func(m Model) error { return SaveScalar(m.target, m.cwd, "agent", "codex") },
		},
		{
			key:  "pull",
			open: func(m *Model) { m.ed.cursor = indexOf(m.ed.options, "never") },
			save: func(m Model) error { return SaveScalar(m.target, m.cwd, "pull", "never") },
		},
		{
			key:  "image",
			open: func(m *Model) { m.ed.input.SetValue("ghcr.io/acme/box:v1") },
			save: func(m Model) error { return SaveScalar(m.target, m.cwd, "image", "ghcr.io/acme/box:v1") },
		},
		{
			key:  "registry_mirror",
			open: func(m *Model) { m.ed.input.SetValue("mirror.example.com") },
			save: func(m Model) error { return SaveScalar(m.target, m.cwd, "registry_mirror", "mirror.example.com") },
		},
		{
			key:  "mounts_root",
			open: func(m *Model) { m.ed.input.SetValue("/tmp/roots") },
			save: func(m Model) error { return SaveScalar(m.target, m.cwd, "mounts_root", "/tmp/roots") },
		},
		{
			key:  "bridge",
			open: func(m *Model) { m.ed.cursor = indexOf(m.ed.options, "false") },
			save: func(m Model) error { return SaveBool(m.target, m.cwd, "bridge", boolPtr(false)) },
		},
		{
			key:  "proximo",
			open: func(m *Model) { m.ed.cursor = indexOf(m.ed.options, "true") },
			save: func(m Model) error { return SaveBool(m.target, m.cwd, "proximo", boolPtr(true)) },
		},
		{
			key:  "managed_statusline",
			open: func(m *Model) { m.ed.cursor = indexOf(m.ed.options, "false") },
			save: func(m Model) error { return SaveBool(m.target, m.cwd, "managed_statusline", boolPtr(false)) },
		},
		{
			key:  "inherit_host_auth",
			open: func(m *Model) { m.ed.selected[m.ed.options[0]] = true },
			save: func(m Model) error {
				return SaveStringList(m.target, m.cwd, "inherit_host_auth", []string{HostAuthOptions()[0]})
			},
			prepare: func(t *testing.T, home string) { requireHostAuthPath(t, home, HostAuthOptions()[0]) },
		},
		{
			key:  "sdd",
			open: func(m *Model) { m.ed.selected[m.ed.options[0]] = true },
			save: func(m Model) error { return SaveSDD(m.target, m.cwd, map[string]bool{SDDOptions()[0]: true}) },
		},
		{
			key:  "mounts",
			open: func(m *Model) { m.ed.selected[m.ed.options[0]] = true },
			save: func(m Model) error {
				return SaveMountDisabled(m.target, m.cwd, map[string]bool{DefaultMountNames()[0]: true})
			},
		},
		{
			key:  "env",
			open: func(m *Model) { m.ed.rows = [][2]string{{"REGION", "eu"}} },
			save: func(m Model) error { return SaveMap(m.target, m.cwd, "env", map[string]string{"REGION": "eu"}) },
		},
		{
			key:  "worktree",
			open: func(m *Model) { m.ed.rows = [][2]string{{".env.local", ""}} },
			save: func(m Model) error { return SaveSeed(m.target, m.cwd, []string{".env.local"}) },
		},
		{
			key:   "shells",
			open:  func(m *Model) { m.ed.rows = [][2]string{{"prod", "/repo/prod"}} },
			inCfg: func(cfg *config.Config) { cfg.Shells = map[string]config.NamedShell{"prod": {Path: "/repo/old"}} },
			save: func(m Model) error {
				return SaveShells(m.target, m.cwd, []ShellEntry{{Name: "prod", Path: "/repo/prod", OrigName: "prod"}})
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

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			m, target := openedEditor(t, tc)
			base, _, err := readMaybe(target)
			if err != nil {
				t.Fatalf("read baseline: %v", err)
			}
			claimed, err := previewAfter(target, base, m.pendingMutator())
			if err != nil {
				t.Fatalf("render preview: %v", err)
			}

			// What the key's own writer produces, driven with hand-written
			// arguments rather than the model's dispatch.
			if err := tc.save(m); err != nil {
				t.Fatalf("writer: %v", err)
			}
			if got := readFile(t, target); got != string(claimed) {
				t.Errorf("preview does not describe what the writer writes.\npreview:\n%s\nwriter:\n%s", claimed, got)
			}

			// And the model's own save path must land on those same bytes.
			m2, target2 := openedEditor(t, tc)
			if err := m2.saveEdit(); err != nil {
				t.Fatalf("saveEdit: %v", err)
			}
			if got := readFile(t, target2); got != string(claimed) {
				t.Errorf("saveEdit does not write what the preview shows.\npreview:\n%s\nsaveEdit:\n%s", claimed, got)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }

// openedEditor builds a Model over an isolated repo whose .toolbox.yaml already
// exists (so the header a fresh file would gain is not mistaken for a
// divergence), opens tc.key's editor and applies the pending value.
func openedEditor(t *testing.T, tc previewCase) (Model, string) {
	t.Helper()
	home := isolatedHome(t)
	if tc.prepare != nil {
		tc.prepare(t, home)
	}
	repo := t.TempDir()
	target := filepath.Join(repo, ".toolbox.yaml")
	writeFile(t, target, "# existing config\nagent: claude\n")

	cfg, _, err := Snapshot(repo, "")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if tc.inCfg != nil {
		tc.inCfg(cfg)
	}

	m := Model{cwd: repo, scope: ScopeRepo, cfg: cfg, target: target,
		states: []KeyState{{Key: tc.key, ScopeSet: true}}}
	m.openEditor()
	if !m.editing {
		t.Fatalf("openEditor did not open an editor for %q (status: %s)", tc.key, m.status)
	}
	tc.open(&m)
	return m, target
}
