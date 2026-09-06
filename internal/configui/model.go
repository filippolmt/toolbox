package configui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

// editorKind is the per-key editor the detail pane shows while editing. Which
// editor a key opens is declared once, in that key's config.Key row; these are
// the local spellings the pane switches on.
type editorKind = config.Editor

const (
	edNone   = config.EditorNone
	edEnum   = config.EditorChoice // bounded value list (pull / agent / shell)
	edString = config.EditorText   // free text (image / registry_mirror / mounts_root)
	edTri    = config.EditorTri    // unset / true / false (bridge / proximo / managed_statusline)
	edMulti  = config.EditorSet    // catalog multi-select (inherit_host_auth)
	edRows   = config.EditorRows   // add/edit/remove rows (env / shells / worktree.seed)
)

// editorSeeds is the state each editor kind opens with — the single dispatch on
// the kind a key's config.Key row declares. openEditor looks the seed up by that
// kind instead of switching on it a second time: it used to carry a copy of this
// switch, re-answering per key what the row had already said, so a kind added
// there and forgotten here (or the reverse) produced an editor pane with
// nothing in it. A kind absent from this table opens nothing, which is how
// edNone stays inert without a branch of its own.
//
// A seed reads the current value through the row's typed reader and its option
// set through the key's descriptor — the two halves openEditor hands it. The
// table lives here, in the tea half, and not beside keyDescriptors: what a kind
// opens with is UI state built out of bubbles widgets, and the adapter half has
// no business holding a textinput.
var editorSeeds = map[editorKind]func(config.Key, keyDescriptor, *config.Config) editor{
	edEnum: func(row config.Key, d keyDescriptor, cfg *config.Config) editor {
		// A missing option set opens an empty chooser rather than panicking, so
		// TestEveryEditableKeyOpensAnEditor names the key that lacks one.
		opts, cur := optionsOf(d), row.Str(cfg)
		return editor{key: row.Name, kind: edEnum, options: opts, current: cur,
			def: EnumDefault(row.Name), cursor: indexOf(opts, cur)}
	},
	edString: func(row config.Key, _ keyDescriptor, cfg *config.Config) editor {
		ti := textinput.New()
		ti.SetValue(row.Str(cfg))
		ti.Focus()
		return editor{key: row.Name, kind: edString, input: ti}
	},
	edTri: func(row config.Key, _ keyDescriptor, cfg *config.Config) editor {
		cur := triState(row.Tri(cfg))
		// Tri-state default is "unset" (auto) — omitting the key is the built-in.
		return editor{key: row.Name, kind: edTri, options: triChoices, current: cur,
			def: triChoices[0], cursor: indexOf(triChoices, cur)}
	},
	edMulti: func(row config.Key, d keyDescriptor, cfg *config.Config) editor {
		return editor{key: row.Name, kind: edMulti, options: optionsOf(d), selected: selectionOf(d, cfg)}
	},
	edRows: func(row config.Key, _ keyDescriptor, cfg *config.Config) editor {
		// Pair editors carry key→value rows; the rest are single-column lists. A
		// row with neither opens empty rather than panicking, so the sweep names
		// the key instead of the TUI dying on enter.
		switch {
		case row.Pairs != nil:
			return rowsEditorState(row.Name, true, pairsToRows(row.Pairs(cfg)))
		case row.List != nil:
			return rowsEditorState(row.Name, false, valuesToRows(row.List(cfg)))
		default:
			return rowsEditorState(row.Name, false, nil)
		}
	},
}

// triChoices is the fixed option order for tri-state editors; index maps to the
// persisted value via triValue.
var triChoices = []string{"unset", "true", "false"}

func triValue(choice string) *bool {
	switch choice {
	case "true":
		t := true
		return &t
	case "false":
		f := false
		return &f
	default:
		return nil
	}
}

// editor holds the transient state of the detail pane while a key is edited.
type editor struct {
	key      string
	kind     editorKind
	options  []string        // enum / tri / multi option labels
	current  string          // enum / tri option matching the current effective value
	def      string          // enum / tri option that is the built-in default (marked "(default)")
	cursor   int             // option cursor / row cursor
	selected map[string]bool // multi-select toggles
	input    textinput.Model // string / row-field editor

	// rows editor (env / shells / worktree.seed)
	rows     [][2]string // [key, value]; single-column lists use [0]
	orig     []string    // per-row original column-0 name at open ("" once added); index-aligned with rows, carries a renamed shell's env
	rowPair  bool        // true = key→value pairs (env, shells)
	rowEdit  bool        // true = typing into a field, false = navigating rows
	field    int         // active column while editing (0 or 1)
	fieldKey string      // buffered key column while entering a new pair
	adding   bool        // the row being edited is a freshly added one
}

// Model is the bubbletea model for `toolbox config ui`.
type Model struct {
	cwd string

	scope  Scope
	cfg    *config.Config
	states []KeyState
	target string
	cursor int

	editing bool
	ed      editor

	// previewBase is the target document as it stood when the editor opened —
	// the "before" side the preview diffs against. Read once per editor session:
	// the panel repaints on every keystroke, so re-reading there would put file
	// I/O on the render path. It goes stale only against an edit made to the
	// file from outside while the editor is open, and the save's own Doctor pass
	// still validates the real result.
	previewBase baseDoc

	status   string
	loadErr  error
	quitting bool
	width    int
	height   int
}

// New builds the initial model, resolving the first snapshot for the Global
// scope. A resolution error is held on the model and shown in View.
func New(cwd string) Model {
	m := Model{cwd: cwd, scope: ScopeGlobal}
	m.reload()
	return m
}

// reload re-resolves the snapshot and write target for the current scope. The
// UI edits the global/repo layers only, so it resolves with no explicit
// override (empty) — its view matches exactly the layers it can write.
func (m *Model) reload() {
	cfg, states, err := Snapshot(m.cwd, "")
	if err != nil {
		m.loadErr = err
		return
	}
	target, err := TargetPath(m.scope, m.cwd)
	if err != nil {
		m.loadErr = err
		return
	}
	// Enrich the (scope-agnostic) effective snapshot with the selected scope's
	// own view of each key, so the detail pane can show "in <scope>" and the tab
	// visibly changes what it reports.
	scoped, err := scopeStates(target)
	if err != nil {
		m.loadErr = err
		return
	}
	for i := range states {
		if s, ok := scoped[states[i].Key]; ok {
			states[i].ScopeSet = s.set
			states[i].ScopeDisplay = s.display
		}
	}
	m.loadErr = nil
	m.cfg = cfg
	m.states = states
	m.target = target
	if m.cursor >= len(m.states) {
		m.cursor = len(m.states) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case editorClosedMsg:
		if msg.err != nil {
			m.status = "$EDITOR exited with error: " + msg.err.Error()
		} else {
			m.status = "reloaded after $EDITOR"
		}
		m.reload()
		return m, nil
	case tea.KeyPressMsg:
		if m.editing {
			return m.updateEditing(msg)
		}
		return m.updateBrowsing(msg)
	}
	return m, nil
}

func (m Model) updateBrowsing(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.states)-1 {
			m.cursor++
		}
	case "tab", "left", "right", "h", "l":
		m.toggleScope()
	case "enter":
		m.openEditor()
	case "e":
		if len(m.states) > 0 && hasEditorEscape(m.states[m.cursor].Key) {
			return m.openInEditor()
		}
	case "r":
		m.resetToDefault()
	}
	return m, nil
}

func (m *Model) toggleScope() {
	if m.scope == ScopeGlobal {
		m.scope = ScopeRepo
	} else {
		m.scope = ScopeGlobal
	}
	m.status = ""
	m.reload()
}

// openEditor opens the editor the selected key's descriptor names, seeded from
// that row's typed accessor. Env-sourced and single-value keys open none. The
// editor a key gets is no longer a branch to remember: it is the row's kind,
// looked up in editorSeeds, which TestEveryEditableKeyOpensAnEditor demands for
// every key the UI lists.
func (m *Model) openEditor() {
	if len(m.states) == 0 {
		return
	}
	st := m.states[m.cursor]
	if st.FromEnv {
		m.status = fmt.Sprintf("%s is set via TOOLBOX_%s and is read-only here", st.Key, strings.ToUpper(st.Key))
		return
	}
	if st.ReadOnly {
		m.status = fmt.Sprintf("%s has a single supported value (%s) — nothing to edit", st.Key, st.Default)
		return
	}
	// A third refusal in the family above: a workspace-anchored key has no
	// coherent meaning in the global layer. configedit owns both the set and
	// the why — CONTEXT.md#config-scope, ADR 0011.
	if configedit.WorkspaceOnlyKey(st.Key) && m.scope == ScopeGlobal {
		m.status = fmt.Sprintf("%s is per-workspace and cannot be written to %s — press tab to switch to the repo scope", st.Key, m.scope)
		return
	}
	key := st.Key
	// Baseline for the preview diff, taken once for the whole editor session
	// (see Model.previewBase).
	m.previewBase = readBaseDoc(m.target)
	// Signal when the edit will fork a value into a scope that does not set it,
	// so creating an override is a deliberate act, not a surprise.
	if st.ScopeSet {
		m.status = ""
	} else {
		m.status = fmt.Sprintf("editing creates an override in %s", m.scope)
	}

	// The editor kind and the typed reader that seeds it come from the key's
	// row; the option sets and the writer behind them from its descriptor. One
	// dispatch, not two: the seed is looked up by the kind the row declares
	// rather than re-derived from the key.
	//
	// Defensive both ways — TestEveryEditableKeyOpensAnEditor forbids a UI key
	// without an editor, so this only fires for a key absent from the schema, or
	// for an editor kind added without a seed beside it.
	row, known := config.KeyByName(key)
	if !known {
		m.status = fmt.Sprintf("%s has no interactive editor yet", key)
		return
	}
	seed, ok := editorSeeds[row.Editor]
	if !ok {
		m.status = fmt.Sprintf("%s has no interactive editor yet", key)
		return
	}
	m.ed = seed(row, keyDescriptors[key], m.cfg)
	m.editing = true
}

// optionsOf and selectionOf read a multi-select's option set and checked set,
// tolerating a descriptor that carries neither: the editor then opens empty and
// TestEveryEditableKeyOpensAnEditor names the key, instead of a nil call
// panicking the TUI on enter.
func optionsOf(d keyDescriptor) []string {
	if d.options == nil {
		return nil
	}
	return d.options()
}

func selectionOf(d keyDescriptor, cfg *config.Config) map[string]bool {
	if d.selected == nil {
		return nil
	}
	return d.selected(cfg)
}

// hasEditorEscape reports whether a key offers the "open in $EDITOR" hatch —
// the complex keys whose structured editor does not cover every case.
func hasEditorEscape(key string) bool {
	return keyDescriptors[key].escape
}

// editorClosedMsg is delivered when the $EDITOR subprocess returns.
type editorClosedMsg struct{ err error }

// openInEditor suspends the TUI, opens the target file in the host $EDITOR
// (fallback vi), and reloads on return so hand-edits are picked up.
func (m Model) openInEditor() (tea.Model, tea.Cmd) {
	if err := EnsureTargetFile(m.target); err != nil {
		m.status = "open in $EDITOR failed: " + err.Error()
		return m, nil
	}
	// strings.Fields collapses all-whitespace to an empty slice, so guard on the
	// split result rather than the raw value ($EDITOR="  " passes name != "").
	parts := strings.Fields(os.Getenv("EDITOR"))
	if len(parts) == 0 {
		parts = []string{"vi"}
	}
	c := exec.Command(parts[0], append(parts[1:], m.target)...) //nolint:gosec // $EDITOR is the user's own choice
	return m, tea.ExecProcess(c, func(err error) tea.Msg { return editorClosedMsg{err} })
}

func (m *Model) resetToDefault() {
	if len(m.states) == 0 {
		return
	}
	st := m.states[m.cursor]
	if st.FromEnv {
		m.status = fmt.Sprintf("%s is env-sourced; unset TOOLBOX_%s on the host instead", st.Key, strings.ToUpper(st.Key))
		return
	}
	if !st.ScopeSet {
		m.status = fmt.Sprintf("%s is not set in %s (inherits %s) — nothing to reset", st.Key, m.scope, originLabel(st))
		return
	}
	// Removing the key is the same path as a tri-state "unset": the file simply
	// stops setting it, so the next load inherits the lower layer's value.
	if _, _, err := configedit.ApplyChecked(m.target, m.cwd, configedit.Remove(st.Key)); err != nil {
		m.status = "reset failed: " + err.Error()
		return
	}
	m.status = fmt.Sprintf("reset %s to default in %s", st.Key, m.scope)
	m.reload()
	m.reconcileArtefactsAfterReset(st.Key)
}

// reconcileArtefactsAfterReset brings the files the reset key owns beside
// .toolbox.yaml back in line once the key has been removed from the selected
// layer. Meaning and rationale: CONTEXT.md#config-scope, ADR 0011.
//
// It reconciles against what the layers now resolve to, not against the empty
// set: reset clears one layer, so a flag kept in the other still enables the
// skill — and a skill that still runs still needs its fence. It runs after the
// reload for that reason, and abstains when the reload could not answer.
//
// Only from the layer that owns the fences, though: the global file may be
// cleaned of a hand-written flag (openEditor refuses to *create* one there, not
// to clear it) while each fence stays with the workspace that wrote it.
func (m *Model) reconcileArtefactsAfterReset(key string) {
	if !configedit.WorkspaceOnlyKey(key) {
		return
	}
	if m.scope == ScopeGlobal {
		m.status += "; .gitignore fences left untouched (each belongs to its workspace)"
		return
	}
	if m.loadErr != nil {
		return
	}
	// Dispatched per key, never inherited from the predicate above: which layer
	// may write a key and which files that key owns are two different facts, and
	// a second workspace-only key reconciling sdd's fences would be a bug.
	if key != sddKey {
		return
	}
	if err := configedit.ReconcileSDDGitignore(filepath.Join(m.cwd, ".gitignore"), EnabledSDD(m.cfg)); err != nil {
		m.status = fmt.Sprintf("reset %s in %s, but the .gitignore fence cleanup failed: %v", key, m.scope, err)
	}
}

func (m Model) updateEditing(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Rows editors give esc a nuanced meaning (back out of a field vs close),
	// so they own the whole key stream.
	if m.ed.kind == edRows {
		return m.updateRows(msg)
	}
	if msg.String() == "esc" {
		m.closeEditor()
		return m, nil
	}
	switch m.ed.kind {
	case edEnum, edTri:
		return m.updateChoice(msg)
	case edMulti:
		return m.updateMulti(msg)
	case edString:
		return m.updateString(msg)
	}
	return m, nil
}

func (m *Model) closeEditor() {
	m.editing = false
	m.ed = editor{}
	m.previewBase = baseDoc{}
}

func (m Model) updateChoice(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.ed.cursor > 0 {
			m.ed.cursor--
		}
	case "down", "j":
		if m.ed.cursor < len(m.ed.options)-1 {
			m.ed.cursor++
		}
	case "enter":
		m.finishSave(m.saveEdit())
	}
	return m, nil
}

func (m Model) updateMulti(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.ed.cursor > 0 {
			m.ed.cursor--
		}
	case "down", "j":
		if m.ed.cursor < len(m.ed.options)-1 {
			m.ed.cursor++
		}
	case " ", "space": // bubbletea v2 stringifies the spacebar as "space", not " "
		if m.ed.selected == nil { // guard: a nil-map write would panic
			m.ed.selected = map[string]bool{}
		}
		opt := m.ed.options[m.ed.cursor]
		m.ed.selected[opt] = !m.ed.selected[opt]
	case "enter":
		m.finishSave(m.saveEdit())
	}
	return m, nil
}

// pendingMutator is the editor's current pending edit as a single value: the
// one mutation both the preview and the save use, so the panel cannot describe
// a change the writer would not make. The mutation comes from the key's own
// descriptor row, so the preview and the writer are keyed on the same axis —
// keying them differently is what once made the mounts panel render the inverse
// of the edit. A nil return means the key has no mutation (no editor is open, or
// a key reached the table without a writer).
func (m Model) pendingMutator() configedit.Mutator {
	d := keyDescriptors[m.ed.key]
	if d.mutator == nil {
		return nil
	}
	return d.mutator(&m.ed, m.cfg)
}

// selectedOptions lists the checked multi-select options in option order.
func (e *editor) selectedOptions() []string {
	var vals []string
	for _, opt := range e.options {
		if e.selected[opt] {
			vals = append(vals, opt)
		}
	}
	return vals
}

// saveEdit commits the editor's pending mutation — the same value the preview
// renders. sdd goes through SaveSDD because its edit carries a post-commit
// .gitignore fence reconcile on top of the yaml mutation.
func (m Model) saveEdit() error {
	if m.ed.key == "sdd" {
		return SaveSDD(m.target, m.cwd, m.ed.selected)
	}
	mut := m.pendingMutator()
	if mut == nil {
		// Unreachable today (openEditor only opens keys pendingMutator covers);
		// an explicit error stops a future key from reporting a false "saved"
		// with no write behind it.
		return fmt.Errorf("no writer for key %q", m.ed.key)
	}
	_, _, err := configedit.ApplyChecked(m.target, m.cwd, mut)
	return err
}

func (m Model) updateString(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "enter" {
		m.finishSave(m.saveEdit())
		return m, nil
	}
	var cmd tea.Cmd
	m.ed.input, cmd = m.ed.input.Update(msg)
	return m, cmd
}

// finishSave records the outcome of a save, closing the editor and reloading on
// success while keeping it open (with the error shown) on a rejected edit.
func (m *Model) finishSave(err error) {
	if err != nil {
		m.status = "save blocked: " + err.Error()
		return
	}
	key := m.ed.key
	m.closeEditor()
	m.reload()
	m.status = fmt.Sprintf("saved %s to %s", key, m.scope)
}

// indexOf returns the position of v in opts, or 0 when absent. 0-on-miss is an
// intentional safe default: every caller seeds it with the current effective
// value, which is always one of the options (enums come from validated config;
// tri-state maps through triState into triChoices), so a miss is unreachable —
// and preselecting the first option is the sane fallback if that ever changes.
func indexOf(opts []string, v string) int {
	for i, o := range opts {
		if o == v {
			return i
		}
	}
	return 0
}

// originLabel is a short provenance tag for the key list. A collection whose
// entries span more than one layer is tagged "mixed" — a single label cannot
// honestly name one winning layer for it. (OriginExplicit is unreachable here:
// the UI only ever resolves the global/repo layers it can write, never a
// --config override, so it collapses into the default tag.)
func originLabel(st KeyState) string {
	if st.FromEnv {
		return "env"
	}
	if st.Mixed {
		return "mixed"
	}
	switch st.Origin {
	case configedit.OriginGlobal:
		return "global"
	case configedit.OriginProject:
		return "repo"
	default:
		// "built-in", not "default": the badge names the *source layer*, and
		// reusing "default" collided with the key's default *value* shown below.
		return "built-in"
	}
}
