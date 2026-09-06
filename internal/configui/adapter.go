package configui

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

// Scope selects which config layer every read and write targets.
type Scope int

const (
	// ScopeGlobal targets ~/.toolbox.yaml.
	ScopeGlobal Scope = iota
	// ScopeRepo targets the walked-up project .toolbox.yaml (created in cwd on
	// first save when none is found).
	ScopeRepo
)

// String renders the scope as the label shown in the UI scope tabs.
func (s Scope) String() string {
	if s == ScopeRepo {
		return "Repo"
	}
	return "Global"
}

func (s Scope) where() configedit.Where {
	if s == ScopeRepo {
		return configedit.WhereLocal
	}
	return configedit.WhereGlobal
}

// Keys returns the top-level keys the UI presents, in schema order, with every
// deprecated alias omitted — an alias is surfaced through the live key it folds
// into (config.Merge already performs that fold), never as a row of its own.
func Keys() []string {
	aliases := config.DeprecatedAliases()
	var out []string
	for _, k := range config.SchemaKeys() {
		if _, deprecated := aliases[k]; deprecated {
			continue
		}
		out = append(out, k)
	}
	return out
}

// KeyState is one row of the provenance-annotated key list: the resolved
// effective value (as a short display string), the layer that supplies it, and
// the selected scope's own view of the key — whether that layer's file sets it
// and, if so, a short display of the file's own (unmerged) value. The scope
// fields are filled by the model after resolving the write target; Snapshot
// leaves them zero.
type KeyState struct {
	Key     string
	Origin  configedit.Origin
	Mixed   bool // a collection whose entries span more than one non-default layer
	FromEnv bool // value comes from a TOOLBOX_* env var, so it is read-only here
	Display string

	Description string // one-line "what this key does" (the key's config.Key row)
	Default     string // human-readable built-in default (the key's config.Key row)
	ReadOnly    bool   // the key admits a single supported value — no editor to open

	ScopeSet     bool   // the currently selected scope's file sets this key
	ScopeDisplay string // the scope file's own value (empty when ScopeSet is false)
}

// Snapshot resolves every UI key to its effective value and owning layer by
// reusing config.Plan (resolution) and configedit.Compute (provenance). A key
// still at its built-in default but overridden by a TOOLBOX_* env var is
// marked FromEnv (Compute attributes only file layers, so env surfaces as a
// default-origin key). env eligibility is delegated to config.EnvVarSet — only
// config's actual env-bound keys (config.EnvBoundKeys) can be FromEnv, and a
// key set in a file is credited to that file layer, never to env (env sits
// below the file layers, so a file value wins and stays editable).
func Snapshot(cwd, explicitOverride string) (*config.Config, []KeyState, error) {
	cfg, err := config.Plan(cwd, explicitOverride)
	if err != nil {
		return nil, nil, err
	}
	prov, err := configedit.Compute(cwd, explicitOverride)
	if err != nil {
		return nil, nil, err
	}
	states := make([]KeyState, 0, len(Keys()))
	for _, key := range Keys() {
		origin, mixed := originFor(prov, key)
		row, _ := config.KeyByName(key)
		st := KeyState{
			Key:         key,
			Origin:      origin,
			Mixed:       mixed,
			Display:     displayValue(cfg, key),
			Description: row.Summary,
			Default:     row.Default,
			ReadOnly:    ReadOnlyKey(key),
		}
		if st.Origin == configedit.OriginDefault && config.EnvVarSet(key) {
			st.FromEnv = true
		}
		states = append(states, st)
	}
	return cfg, states, nil
}

// scopeState is one config file's own view of a key: whether the file sets it
// and a short display of the file's unmerged value.
type scopeState struct {
	set     bool
	display string
}

// scopeStates reports, per UI key, whether one layer's file sets it and a short
// display of that file's own value — the data behind the TUI's per-scope
// "in <scope>" line. Whether a file sets a key (and how a deprecated alias
// folds into the live one) is a provenance question configedit owns, so it is
// asked there rather than answered here by parsing the file a second time; this
// keeps only the rendering, which is presentation. A missing file yields an
// all-unset map, so switching to a scope with no file cleanly shows every key
// as inherited.
func scopeStates(path string) (map[string]scopeState, error) {
	vals, err := configedit.FileValues(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]scopeState, len(Keys()))
	for _, key := range Keys() {
		if _, set := vals[key]; !set {
			continue
		}
		out[key] = scopeState{set: true, display: keyDescriptors[key].scopeDisplay(vals, key)}
	}
	return out, nil
}

// originFor returns the provenance origin for a top-level key and whether it is
// mixed. Which keys are attributed per entry (shells.<name> / mounts.<name>) is
// configedit's own fact, asked rather than restated: their container row credits
// the highest origin among the matching entries and reports mixed=true when
// those entries span more than one non-default layer (a single badge colour
// cannot represent that honestly).
func originFor(prov configedit.Provenance, key string) (origin configedit.Origin, mixed bool) {
	if !configedit.PerEntryKey(key) {
		return prov[key], false
	}
	best := configedit.OriginDefault
	seen := map[configedit.Origin]bool{}
	for k, o := range prov {
		if k == key || strings.HasPrefix(k, key+".") {
			if o != configedit.OriginDefault {
				seen[o] = true
			}
			if o > best {
				best = o
			}
		}
	}
	return best, len(seen) > 1
}

// displayValue renders a short, list-friendly summary of a key's effective
// value. Collections show a count; scalars show the value (or a parenthesised
// default hint when empty); tri-state bools show unset/true/false. The shape
// comes from the key's descriptor, so a key with no row renders empty — which
// TestEveryKeyDisplaysSomething forbids, naming the key.
func displayValue(cfg *config.Config, key string) string {
	row, _ := config.KeyByName(key)
	return keyDescriptors[key].displayOf(cfg, row)
}

func countLabel(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// orHint shows a key's value, or a bare parenthetical hint when empty, for the
// keys with no meaningful default to echo (image / registry_mirror /
// mounts_root) — never the doubled "(hint) (default)". Fallback-bearing scalars
// go through config.EffectiveValue instead.
func orHint(v, hint string) string {
	if v == "" {
		return hint
	}
	return v
}

// detailEntries lists a collection key's effective entry names for the detail
// pane, so the actual contents are visible without opening the editor. Sorted
// here (on a copy) so a descriptor row can hand back the config's own slice.
// Returns nil for non-collection keys or an empty collection.
func detailEntries(cfg *config.Config, key string) []string {
	row, _ := config.KeyByName(key)
	return slices.Sorted(slices.Values(keyDescriptors[key].entriesOf(cfg, row)))
}

// triState renders an optional bool as its three distinct states.
func triState(b *bool) string {
	switch {
	case b == nil:
		return "unset"
	case *b:
		return "true"
	default:
		return "false"
	}
}

// TargetPath resolves the config-file path a save to scope writes, via the
// same configedit.Resolve the CLI writers use. A missing repo file resolves to
// ./.toolbox.yaml and is created on first save.
func TargetPath(scope Scope, cwd string) (string, error) {
	return configedit.Resolve(scope.where(), cwd)
}

// enumOptions returns the bounded valid values for an enum key, or nil when the
// key is not an enum. The option sets themselves stay in config — the descriptor
// only records which key offers which one.
func enumOptions(key string) []string {
	row, ok := config.KeyByName(key)
	if !ok || row.Editor != config.EditorChoice {
		return nil
	}
	return keyDescriptors[key].optionsOf()
}

// EnumDefault returns the value an enum key resolves to when unset — the option
// the editor marks "(default)". "" for non-enum keys. The default itself is the
// key row's own Default, so this never re-hardcodes the key→default mapping
// config already owns.
func EnumDefault(key string) string {
	if enumOptions(key) == nil {
		return ""
	}
	row, _ := config.KeyByName(key)
	return row.Default
}

// ReadOnlyKey reports whether a key admits a single supported value, so the UI
// shows it read-only rather than opening an editor with one meaningless choice
// (e.g. shell, whose only value is zsh). Generic: any future single-option enum
// gets the same treatment.
func ReadOnlyKey(key string) bool {
	return len(enumOptions(key)) == 1
}

// HostAuthOptions is the option set for the inherit_host_auth multi-select:
// exactly the catalog CLIs eligible for host-auth inheritance, so the UI can
// never drift from what the CLI supports.
func HostAuthOptions() []string { return catalog.HostAuthEligibleKeys() }

// ShellEntry is one desired shells: entry for configedit.Shells — the rows
// editor's output shape, aliased so the UI does not have to name the configedit
// package to build one.
type ShellEntry = configedit.ShellEntry

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SDDOptions returns the known SDD skill keys, sorted — the option set for the
// structured sdd editor. It is the same set configedit.SDDEnabled reconciles,
// so the checkboxes offered and the keys written cannot drift.
func SDDOptions() []string { return configedit.SDDKeys() }

// EnabledSDD reports which SDD skills are currently enabled.
func EnabledSDD(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	for key, s := range cfg.SDD {
		if s.Enabled {
			out[key] = true
		}
	}
	return out
}

// SaveSDD reconciles the SDD skill set through the configedit seam so the TUI
// produces the same .toolbox.yaml + .gitignore state as `toolbox sdd init`. The
// yaml reconcile stays transactional and Doctor-gated; only after that commit
// succeeds does it write the .gitignore fence for each enabled skill and remove
// it for each disabled one — fences are outside Doctor's contract, so a
// rejected yaml reconcile leaves every fence untouched.
func SaveSDD(target, cwd string, enabled map[string]bool) error {
	if _, _, err := configedit.ApplyChecked(target, cwd, configedit.SDDEnabled(enabled)); err != nil {
		return err
	}
	return configedit.ReconcileSDDGitignore(filepath.Join(cwd, ".gitignore"), enabled)
}

// DefaultMountNames returns the names of the built-in default mounts — the
// candidate set for the structured mounts (disable) editor, and the same set
// configedit.MountsDisabled reconciles.
func DefaultMountNames() []string { return configedit.DefaultMountNames() }

// DisabledMounts reports which default mounts the config currently disables.
func DisabledMounts(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	for _, m := range cfg.Mounts {
		if m.Name != "" && m.Disabled {
			out[m.Name] = true
		}
	}
	return out
}

// EnsureTargetFile creates the target config file (with the docs header) when it
// does not exist yet, so the $EDITOR escape opens a real file rather than a
// blank buffer at a path the editor may refuse to create.
func EnsureTargetFile(target string) error {
	return configedit.EnsureFileWithHeader(target)
}
