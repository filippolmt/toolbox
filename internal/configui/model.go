package configui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

// editorKind is the per-key editor the detail pane shows while editing.
type editorKind int

const (
	edNone   editorKind = iota
	edEnum              // bounded value list (pull / agent / shell)
	edString            // free text (image / registry_mirror / mounts_root)
	edTri               // unset / true / false (bridge / proximo / managed_statusline)
	edMulti             // catalog multi-select (inherit_host_auth)
	edRows              // add/edit/remove rows (env / shells / worktree.seed)
)

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
	scoped, err := ScopeStates(target)
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

// openEditor opens the editor matched to the selected key's type. Env-sourced
// keys are read-only. The default branch is a defensive guard: every current
// UI key has an explicit editor, but a config key added without a matching
// branch here surfaces a status message instead of silently doing nothing.
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

	if opts := EnumOptions(key); opts != nil {
		cur := StringValue(m.cfg, key)
		m.ed = editor{key: key, kind: edEnum, options: opts, current: cur, def: EnumDefault(key), cursor: indexOf(opts, cur)}
		m.editing = true
		return
	}
	switch key {
	case "image", "registry_mirror", "mounts_root":
		ti := textinput.New()
		ti.SetValue(StringValue(m.cfg, key))
		ti.Focus()
		m.ed = editor{key: key, kind: edString, input: ti}
		m.editing = true
	case "bridge", "proximo", "managed_statusline":
		cur := triState(BoolValue(m.cfg, key))
		// Tri-state default is "unset" (auto) — omitting the key is the built-in.
		m.ed = editor{key: key, kind: edTri, options: triChoices, current: cur, def: triChoices[0], cursor: indexOf(triChoices, cur)}
		m.editing = true
	case "inherit_host_auth":
		opts := HostAuthOptions()
		sel := map[string]bool{}
		for _, v := range ListValue(m.cfg, key) {
			sel[v] = true
		}
		m.ed = editor{key: key, kind: edMulti, options: opts, selected: sel}
		m.editing = true
	case "env":
		m.openRowsEditor(key, true, pairsToRows(m.cfg.Env))
	case "shells":
		m.openRowsEditor(key, true, pairsToRows(ShellPaths(m.cfg)))
	case "worktree":
		m.openRowsEditor(key, false, valuesToRows(ListValue(m.cfg, key)))
	case "sdd":
		m.ed = editor{key: key, kind: edMulti, options: SDDOptions(), selected: EnabledSDD(m.cfg)}
		m.editing = true
	case "mounts":
		m.ed = editor{key: key, kind: edMulti, options: DefaultMountNames(), selected: DisabledMounts(m.cfg)}
		m.editing = true
	default:
		m.status = fmt.Sprintf("%s has no interactive editor yet", key)
	}
}

// hasEditorEscape reports whether a key offers the "open in $EDITOR" hatch —
// the complex keys whose structured editor does not cover every case.
func hasEditorEscape(key string) bool {
	return key == "mounts" || key == "sdd"
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
	if _, err := configedit.ApplyChecked(m.target, m.cwd, configedit.Remove(st.Key)); err != nil {
		m.status = "reset failed: " + err.Error()
		return
	}
	m.status = fmt.Sprintf("reset %s to default in %s", st.Key, m.scope)
	m.reload()
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
// a change the writer would not make. A nil return means the key has no
// mutation (no editor is open, or a key was given an editor without a writer).
//
// Keys whose mutation is more than "write the value" are matched by key first;
// everything else falls through to its editor kind.
func (m Model) pendingMutator() configedit.Mutator {
	switch m.ed.key {
	case "sdd":
		return configedit.SDDEnabled(m.ed.selected)
	case "mounts":
		// The checkboxes name the defaults to *disable*, not to keep.
		return configedit.MountsDisabled(m.ed.selected)
	case "shells":
		return configedit.Shells(m.shellEntries())
	case "env":
		return configedit.StringMap("env", rowsToPairs(m.ed.rows))
	case "worktree":
		return configedit.WorktreeSeed(rowsToValues(m.ed.rows))
	}
	switch m.ed.kind {
	case edEnum:
		return configedit.Scalar(m.ed.key, m.ed.options[m.ed.cursor])
	case edString:
		return configedit.Scalar(m.ed.key, strings.TrimSpace(m.ed.input.Value()))
	case edTri:
		return configedit.Bool(m.ed.key, triValue(m.ed.options[m.ed.cursor]))
	case edMulti:
		return configedit.StringList(m.ed.key, m.selectedOptions())
	}
	return nil
}

// selectedOptions lists the checked multi-select options in option order.
func (m Model) selectedOptions() []string {
	var vals []string
	for _, opt := range m.ed.options {
		if m.ed.selected[opt] {
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
	_, err := configedit.ApplyChecked(m.target, m.cwd, mut)
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
