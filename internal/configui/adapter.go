package configui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	yaml "gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/configio"
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
	case "shell", "agent", "pull":
		// Fallback-bearing scalars derive from the one config.EffectiveValue
		// seam so the TUI can't drift from `config show` on effective values.
		v, _ := config.EffectiveValue(cfg, key)
		return v
	case "image":
		return orHint(cfg.Image, "(default)")
	case "registry_mirror":
		return orHint(cfg.RegistryMirror, "(none)")
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
// the editor marks "(default)". "" for non-enum keys. The default itself comes
// from config.KeyDocs (the single source for per-key defaults), so this never
// re-hardcodes the key→default mapping config already owns.
func EnumDefault(key string) string {
	if EnumOptions(key) == nil {
		return ""
	}
	return config.KeyDocs()[key].Default
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

// ShellEntry is one desired shells: entry for configedit.Shells — the rows
// editor's output shape, aliased so the UI does not have to name the configedit
// package to build one.
type ShellEntry = configedit.ShellEntry

// ShellPaths flattens the effective shells map to name→path for the structured
// editor (per-shell env overlays are preserved on save, not edited here).
func ShellPaths(cfg *config.Config) map[string]string {
	out := make(map[string]string, len(cfg.Shells))
	for name, s := range cfg.Shells {
		out[name] = s.Path
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
// rejected yaml reconcile rolls back before any fence is touched.
func SaveSDD(target, cwd string, enabled map[string]bool) error {
	if err := apply(target, cwd, configedit.SDDEnabled(enabled)); err != nil {
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

// Unset removes a key from the target file — the shared path behind tri-state
// "unset" and "reset to default".
func Unset(target, cwd, key string) error {
	return apply(target, cwd, configedit.Remove(key))
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
func apply(target, cwd string, mutate configedit.Mutator) error {
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
