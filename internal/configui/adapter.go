package configui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	yaml "gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/configio"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sdd"
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

// deprecatedKey is folded into its live sibling and never shown as its own row.
// Sourced from config so this package folds exactly the key config.Merge does.
const deprecatedKey = config.DeprecatedBridgeKey

// Keys returns the top-level keys the UI presents, in schema order, with the
// deprecated browser_bridge omitted — its value is surfaced through bridge
// (config.Merge already folds it into Config.Bridge).
func Keys() []string {
	var out []string
	for _, k := range config.SchemaKeys() {
		if k == deprecatedKey {
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

	Description string // one-line "what this key does" (config.KeyDocs)
	Default     string // human-readable built-in default (config.KeyDocs)
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
	docs := config.KeyDocs()
	states := make([]KeyState, 0, len(Keys()))
	for _, key := range Keys() {
		origin, mixed := originFor(prov, key)
		st := KeyState{
			Key:         key,
			Origin:      origin,
			Mixed:       mixed,
			Display:     displayValue(cfg, key),
			Description: docs[key].Summary,
			Default:     docs[key].Default,
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

// ScopeStates parses one config file and reports, per UI key, whether that file
// sets it and a short display of the file's own value — the data behind the
// TUI's per-scope "in <scope>" line. A missing file yields an all-unset map, so
// switching to a scope with no file cleanly shows every key as inherited.
func ScopeStates(path string) (map[string]scopeState, error) {
	b, existed, err := readMaybe(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]scopeState, len(Keys()))
	if !existed {
		return out, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	doc := configio.EnsureDocumentMap(&root)
	for _, key := range Keys() {
		if node := scopeNode(doc, key); node != nil {
			out[key] = scopeState{set: true, display: nodeDisplay(node, key)}
		}
	}
	return out, nil
}

// scopeNode returns the file node for a key, folding the deprecated
// browser_bridge into bridge the same way config.Merge does (fillDefaultsBackstop),
// so a file that only sets browser_bridge still counts as setting bridge in that
// scope. Keep this fold in sync with that canonical one; both key off
// config.DeprecatedBridgeKey so a rename cannot drift them apart.
func scopeNode(doc *yaml.Node, key string) *yaml.Node {
	if n := configio.ChildValue(doc, key); n != nil {
		return n
	}
	if key == "bridge" {
		return configio.ChildValue(doc, deprecatedKey)
	}
	return nil
}

// nodeDisplay renders a scope file's own value for a key: collections show a
// count of their entries in that file; everything else shows the scalar value.
func nodeDisplay(node *yaml.Node, key string) string {
	switch key {
	case "env", "shells", "sdd":
		return countLabel(len(node.Content)/2, collectionNoun(key)) // mapping: key,value pairs
	case "mounts", "inherit_host_auth":
		return countLabel(len(node.Content), collectionNoun(key)) // sequence
	case "worktree":
		n := 0
		if seed := configio.ChildValue(node, "seed"); seed != nil {
			n = len(seed.Content)
		}
		return countLabel(n, collectionNoun(key))
	default:
		return node.Value
	}
}

// originFor returns the provenance origin for a top-level key and whether it is
// mixed. shells and mounts are attributed per entry (shells.<name> /
// mounts.<name>), so their container row credits the highest origin among the
// matching entries and reports mixed=true when those entries span more than one
// non-default layer (a single badge colour cannot represent that honestly).
func originFor(prov configedit.Provenance, key string) (origin configedit.Origin, mixed bool) {
	if key != "shells" && key != "mounts" {
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
// default hint when empty); tri-state bools show unset/true/false.
func displayValue(cfg *config.Config, key string) string {
	switch key {
	case "mounts":
		return countLabel(len(cfg.Mounts), collectionNoun(key))
	case "inherit_host_auth":
		if len(cfg.InheritHostAuth) == 0 {
			return "(none)"
		}
		return strings.Join(cfg.InheritHostAuth, ", ")
	case "shells":
		return countLabel(len(cfg.Shells), collectionNoun(key))
	case "shell":
		return orDefault(cfg.Shell, config.SupportedShells[0])
	case "agent":
		return orDefault(cfg.Agent, config.DefaultAgent)
	case "image":
		return orHint(cfg.Image, "(default)")
	case "registry_mirror":
		return orHint(cfg.RegistryMirror, "(none)")
	case "pull":
		return orDefault(cfg.Pull, config.PullAuto)
	case "mounts_root":
		return orHint(cfg.MountsRoot, "(~/.toolbox)")
	case "sdd":
		return countLabel(len(cfg.SDD), collectionNoun(key))
	case "bridge":
		return triState(cfg.Bridge)
	case "proximo":
		return triState(cfg.Proximo)
	case "managed_statusline":
		return triState(cfg.ManagedStatusline)
	case "env":
		return countLabel(len(cfg.Env), collectionNoun(key))
	case "worktree":
		return countLabel(len(cfg.Worktree.Seed), collectionNoun(key))
	}
	return ""
}

// collectionNoun is the singular noun a collection key's count is rendered with,
// shared by the effective display (displayValue) and the per-scope display
// (nodeDisplay) so the two never drift.
func collectionNoun(key string) string {
	switch key {
	case "mounts":
		return "override"
	case "shells":
		return "shell"
	case "sdd":
		return "pack"
	case "env":
		return "var"
	case "worktree":
		return "seed path"
	case "inherit_host_auth":
		return "auth entry"
	}
	return "entry"
}

func countLabel(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// orDefault echoes the built-in default value when v is empty. The detail pane
// now carries a dedicated "default:" line, so this no longer appends a
// "(default)" suffix — that duplicated the origin badge and the default line.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// orHint is orDefault's sibling for keys with no meaningful default value to
// echo (image / registry_mirror / mounts_root): it shows the value, or a bare
// parenthetical hint when empty — never the doubled "(hint) (default)".
func orHint(v, hint string) string {
	if v == "" {
		return hint
	}
	return v
}

// detailEntries lists a collection key's effective entry names for the detail
// pane, so the actual contents are visible without opening the editor. Returns
// "" for non-collection keys or an empty collection.
func detailEntries(cfg *config.Config, key string) []string {
	var items []string
	switch key {
	case "env":
		for k := range cfg.Env {
			items = append(items, k)
		}
	case "shells":
		for k := range cfg.Shells {
			items = append(items, k)
		}
	case "sdd":
		for k := range cfg.SDD {
			items = append(items, k)
		}
	case "inherit_host_auth":
		items = append(items, cfg.InheritHostAuth...)
	case "worktree":
		items = append(items, cfg.Worktree.Seed...)
	case "mounts":
		for _, m := range cfg.Mounts {
			if m.Name != "" {
				items = append(items, m.Name)
			}
		}
	default:
		return nil
	}
	sort.Strings(items)
	return items
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

// EnumOptions returns the bounded valid values for an enum key, or nil when the
// key is not an enum.
func EnumOptions(key string) []string {
	switch key {
	case "pull":
		return config.SupportedPullPolicies
	case "agent":
		return config.SupportedAgents
	case "shell":
		return config.SupportedShells
	}
	return nil
}

// EnumDefault returns the value an enum key resolves to when unset — the option
// the editor marks "(default)". "" for non-enum keys.
func EnumDefault(key string) string {
	switch key {
	case "pull":
		return config.PullAuto
	case "agent":
		return config.DefaultAgent
	case "shell":
		return config.SupportedShells[0]
	}
	return ""
}

// ReadOnlyKey reports whether a key admits a single supported value, so the UI
// shows it read-only rather than opening an editor with one meaningless choice
// (e.g. shell, whose only value is zsh). Generic: any future single-option enum
// gets the same treatment.
func ReadOnlyKey(key string) bool {
	return len(EnumOptions(key)) == 1
}

// HostAuthOptions is the option set for the inherit_host_auth multi-select:
// exactly the catalog CLIs eligible for host-auth inheritance, so the UI can
// never drift from what the CLI supports.
func HostAuthOptions() []string { return catalog.HostAuthEligibleKeys() }

// StringValue returns the current effective value of a scalar key, for
// prefilling its editor.
func StringValue(cfg *config.Config, key string) string {
	switch key {
	case "image":
		return cfg.Image
	case "registry_mirror":
		return cfg.RegistryMirror
	case "mounts_root":
		return cfg.MountsRoot
	case "pull":
		return cfg.Pull
	case "agent":
		return cfg.Agent
	case "shell":
		return cfg.Shell
	}
	return ""
}

// BoolValue returns the current effective value of a tri-state bool key.
func BoolValue(cfg *config.Config, key string) *bool {
	switch key {
	case "bridge":
		return cfg.Bridge
	case "proximo":
		return cfg.Proximo
	case "managed_statusline":
		return cfg.ManagedStatusline
	}
	return nil
}

// ListValue returns the current effective value of a string-list key.
func ListValue(cfg *config.Config, key string) []string {
	switch key {
	case "inherit_host_auth":
		return cfg.InheritHostAuth
	case "worktree":
		return cfg.Worktree.Seed
	}
	return nil
}

// SaveScalar writes a scalar key (empty value removes it — the clean
// reset-to-default), gated by Doctor validation.
func SaveScalar(target, cwd, key, value string) error {
	return apply(target, cwd, func(doc *yaml.Node) {
		if value == "" {
			configio.RemoveMapKey(doc, key)
			return
		}
		configio.SetMapValue(doc, key, value)
	})
}

// SaveBool writes a tri-state bool key. A nil value removes the key so "unset"
// never persists as an explicit false (unset carries distinct meaning — e.g.
// proximo unset = auto-detect).
func SaveBool(target, cwd, key string, v *bool) error {
	return apply(target, cwd, func(doc *yaml.Node) {
		if v == nil {
			configio.RemoveMapKey(doc, key)
			return
		}
		configio.SetMapBool(doc, key, *v)
	})
}

// SaveStringList replaces a top-level string sequence (empty removes the key),
// gated by Doctor validation.
func SaveStringList(target, cwd, key string, values []string) error {
	return apply(target, cwd, func(doc *yaml.Node) {
		if len(values) == 0 {
			configio.RemoveMapKey(doc, key)
			return
		}
		seq := configio.EnsureChildSeq(doc, key)
		seq.Content = seq.Content[:0]
		for _, v := range values {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
		}
	})
}

// SaveMap replaces a top-level string→string mapping (env), written in sorted
// key order for a deterministic file; an empty map removes the key. Gated by
// Doctor validation.
func SaveMap(target, cwd, key string, pairs map[string]string) error {
	return apply(target, cwd, func(doc *yaml.Node) {
		if len(pairs) == 0 {
			configio.RemoveMapKey(doc, key)
			return
		}
		node := configio.EnsureChildMap(doc, key)
		node.Content = node.Content[:0]
		for _, k := range sortedKeys(pairs) {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pairs[k]})
		}
	})
}

// ShellEntry is one desired shells: entry for SaveShells. OrigName is the name
// the row carried before editing ("" for a freshly added row); it lets a rename
// carry the source shell's Env overlay to the new name.
type ShellEntry struct {
	Name, Path, OrigName string
	Env                  map[string]string
}

// SaveShells reconciles the shells: block to entries: it removes any shell not
// named by an entry and writes each entry's .path. For an unchanged name the
// existing env block is left untouched (its formatting/comments survive); for a
// rename (Name != OrigName) the carried Env overlay is written under the new
// name so it is not lost. An empty set removes the block. Gated by Doctor.
func SaveShells(target, cwd string, entries []ShellEntry) error {
	return apply(target, cwd, func(doc *yaml.Node) {
		if len(entries) == 0 {
			configio.RemoveMapKey(doc, "shells")
			return
		}
		root := configio.EnsureChildMap(doc, "shells")
		want := make(map[string]bool, len(entries))
		for _, e := range entries {
			want[e.Name] = true
		}
		for _, name := range childKeys(root) {
			if !want[name] {
				configio.RemoveMapKey(root, name)
			}
		}
		for _, e := range entries {
			entry := configio.EnsureChildMap(root, e.Name)
			configio.SetMapValue(entry, "path", e.Path)
			if e.Name != e.OrigName && len(e.Env) > 0 {
				env := configio.EnsureChildMap(entry, "env")
				env.Content = env.Content[:0]
				for _, k := range sortedKeys(e.Env) {
					configio.SetMapValue(env, k, e.Env[k])
				}
			}
		}
	})
}

// SaveSeed writes worktree.seed (nested), creating/removing the worktree block
// as needed; an empty list removes the seed key. Gated by Doctor.
func SaveSeed(target, cwd string, seed []string) error {
	return apply(target, cwd, func(doc *yaml.Node) {
		if len(seed) == 0 {
			if wt := configio.ChildValue(doc, "worktree"); wt != nil && wt.Kind == yaml.MappingNode {
				configio.RemoveMapKey(wt, "seed")
				if len(wt.Content) == 0 {
					configio.RemoveMapKey(doc, "worktree")
				}
			}
			return
		}
		wt := configio.EnsureChildMap(doc, "worktree")
		seq := configio.EnsureChildSeq(wt, "seed")
		seq.Content = seq.Content[:0]
		for _, v := range seed {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v})
		}
	})
}

// ShellPaths flattens the effective shells map to name→path for the structured
// editor (per-shell env overlays are preserved on save, not edited here).
func ShellPaths(cfg *config.Config) map[string]string {
	out := make(map[string]string, len(cfg.Shells))
	for name, s := range cfg.Shells {
		out[name] = s.Path
	}
	return out
}

// childKeys returns the mapping keys of a mapping node in document order.
func childKeys(node *yaml.Node) []string {
	var out []string
	for i := 0; i+1 < len(node.Content); i += 2 {
		if k := node.Content[i]; k.Kind == yaml.ScalarNode {
			out = append(out, k.Value)
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SDDOptions returns the known SDD skill keys, sorted — the option set for the
// structured sdd editor, sourced from the sdd registry so it can't drift.
func SDDOptions() []string {
	out := make([]string, 0, len(sdd.Skills))
	for _, s := range sdd.Skills {
		out = append(out, s.Key)
	}
	sort.Strings(out)
	return out
}

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

// SaveSDD enables the selected SDD skills (as the `sdd.<key>: true` shorthand)
// and removes the rest. A key already carrying an explicit steps override is
// left untouched when it stays enabled, so custom steps survive a toggle. An
// empty selection removes the sdd block. Gated by Doctor.
func SaveSDD(target, cwd string, enabled map[string]bool) error {
	return apply(target, cwd, func(doc *yaml.Node) {
		root := configio.EnsureChildMap(doc, "sdd")
		for _, key := range SDDOptions() {
			switch {
			case !enabled[key]:
				configio.RemoveMapKey(root, key)
			case isObjectForm(configio.ChildValue(root, key)):
				// already enabled with a custom steps override — leave it.
			default:
				configio.SetMapBool(root, key, true)
			}
		}
		if len(root.Content) == 0 {
			configio.RemoveMapKey(doc, "sdd")
		}
	})
}

func isObjectForm(v *yaml.Node) bool {
	return v != nil && v.Kind == yaml.MappingNode
}

// DefaultMountNames returns the names of the built-in default mounts — the
// candidate set for the structured mounts (disable) editor.
func DefaultMountNames() []string {
	var out []string
	for _, m := range mountplan.Defaults() {
		if m.Name != "" {
			out = append(out, m.Name)
		}
	}
	sort.Strings(out)
	return out
}

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

// SaveMountDisabled reconciles per-default-mount disable state: it adds a
// `{name, disabled: true}` patch for each disabled default and drops the patch
// when re-enabled. Only pure disable patches are removed, so a user's richer
// patch/replace entry (edit those via the $EDITOR escape) is never clobbered.
// An emptied mounts list is dropped. Gated by Doctor.
func SaveMountDisabled(target, cwd string, disabled map[string]bool) error {
	return apply(target, cwd, func(doc *yaml.Node) {
		seq := configio.EnsureChildSeq(doc, "mounts")
		for _, name := range DefaultMountNames() {
			idx, entry := configio.FindSeqEntryByName(seq, name)
			switch {
			case disabled[name]:
				if entry != nil {
					configio.SetMapBool(entry, "disabled", true)
				} else {
					patch := &yaml.Node{Kind: yaml.MappingNode}
					configio.SetMapValue(patch, "name", name)
					configio.SetMapBool(patch, "disabled", true)
					seq.Content = append(seq.Content, patch)
				}
			case idx >= 0 && isPureDisable(entry):
				seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
			}
		}
		if len(seq.Content) == 0 {
			configio.RemoveMapKey(doc, "mounts")
		}
	})
}

// isPureDisable reports whether a mounts entry is only a disable patch (name +
// disabled), so re-enabling can safely drop it without discarding real overrides.
func isPureDisable(entry *yaml.Node) bool {
	if entry == nil || entry.Kind != yaml.MappingNode {
		return false
	}
	for _, k := range childKeys(entry) {
		if k != "name" && k != "disabled" {
			return false
		}
	}
	return true
}

// EnsureTargetFile creates the target config file (with the docs header) when it
// does not exist yet, so the $EDITOR escape opens a real file rather than a
// blank buffer at a path the editor may refuse to create.
func EnsureTargetFile(target string) error {
	return configedit.EnsureFileWithHeader(target)
}

// Unset removes a key from the target file — the shared path behind tri-state
// "unset" and "reset to default".
func Unset(target, cwd, key string) error {
	return apply(target, cwd, func(doc *yaml.Node) {
		configio.RemoveMapKey(doc, key)
	})
}

// apply mutates target through the comment-preserving writer, then validates
// with the config doctor scoped so the just-written file is the authoritative
// (explicit) layer — validating the edited file itself, not merely the merged
// result. That distinction matters: a lower layer's invalid value can be
// masked by a higher layer's override in the plain merge, so validating the
// merge alone would let an invalid value persist unnoticed in the file that was
// written. On failure it restores the file to its pre-edit state (original
// bytes, or removal when the edit created it) and returns the validation error,
// so a rejected edit never leaves invalid config on disk.
//
// ponytail: validation is write-then-doctor-then-rollback rather than building
// the candidate document in memory — it reuses Doctor (which loads from disk)
// with zero new validation logic, at the cost of a transient write a
// concurrent reader could observe. Fine for an interactive single-user TUI;
// revisit if config editing ever runs concurrently.
func apply(target, cwd string, mutate func(*yaml.Node)) error {
	orig, existed, err := readMaybe(target)
	if err != nil {
		return err
	}
	if _, err := configedit.Upsert(target, mutate); err != nil {
		return err
	}
	if findings := configedit.Doctor(cwd, target); configedit.HasErrors(findings) {
		if rbErr := rollback(target, orig, existed); rbErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", firstError(findings), rbErr)
		}
		return firstError(findings)
	}
	return nil
}

// readMaybe returns a file's bytes and whether it existed; a missing file is
// not an error (existed=false).
func readMaybe(path string) (data []byte, existed bool, err error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is a resolved config file
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// rollback restores target to its pre-edit state: the original bytes when it
// existed, or removal when the edit created it.
func rollback(target string, orig []byte, existed bool) error {
	if existed {
		return configio.AtomicWriteFile(target, orig, 0o600)
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// firstError returns the first error-severity finding as an error.
func firstError(findings []configedit.Finding) error {
	for _, f := range findings {
		if f.Severity == configedit.SeverityError {
			return fmt.Errorf("%s", f.Message)
		}
	}
	return fmt.Errorf("configuration invalid")
}
