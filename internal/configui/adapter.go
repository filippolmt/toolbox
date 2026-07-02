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
const deprecatedKey = "browser_bridge"

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
// effective value (as a short display string) and the layer that supplies it.
type KeyState struct {
	Key     string
	Origin  configedit.Origin
	FromEnv bool // value comes from a TOOLBOX_* env var, so it is read-only here
	Display string
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
		st := KeyState{
			Key:     key,
			Origin:  originFor(prov, key),
			Display: displayValue(cfg, key),
		}
		if st.Origin == configedit.OriginDefault && config.EnvVarSet(key) {
			st.FromEnv = true
		}
		states = append(states, st)
	}
	return cfg, states, nil
}

// originFor returns the provenance origin for a top-level key. shells and
// mounts are attributed per entry (shells.<name> / mounts.<name>), so their
// container row credits the highest origin among the matching entries.
func originFor(prov configedit.Provenance, key string) configedit.Origin {
	if key != "shells" && key != "mounts" {
		return prov[key]
	}
	best := configedit.OriginDefault
	for k, o := range prov {
		if k == key || strings.HasPrefix(k, key+".") {
			if o > best {
				best = o
			}
		}
	}
	return best
}

// displayValue renders a short, list-friendly summary of a key's effective
// value. Collections show a count; scalars show the value (or a parenthesised
// default hint when empty); tri-state bools show unset/true/false.
func displayValue(cfg *config.Config, key string) string {
	switch key {
	case "mounts":
		return countLabel(len(cfg.Mounts), "override")
	case "inherit_host_auth":
		if len(cfg.InheritHostAuth) == 0 {
			return "(none)"
		}
		return strings.Join(cfg.InheritHostAuth, ", ")
	case "shells":
		return countLabel(len(cfg.Shells), "shell")
	case "shell":
		return orDefault(cfg.Shell, config.SupportedShells[0])
	case "agent":
		return orDefault(cfg.Agent, config.DefaultAgent)
	case "image":
		return orDefault(cfg.Image, "(default)")
	case "registry_mirror":
		return orDefault(cfg.RegistryMirror, "(none)")
	case "pull":
		return orDefault(cfg.Pull, config.PullAuto)
	case "mounts_root":
		return orDefault(cfg.MountsRoot, "(~/.toolbox)")
	case "sdd":
		return countLabel(len(cfg.SDD), "pack")
	case "bridge":
		return triState(cfg.Bridge)
	case "proximo":
		return triState(cfg.Proximo)
	case "managed_statusline":
		return triState(cfg.ManagedStatusline)
	case "env":
		return countLabel(len(cfg.Env), "var")
	case "worktree":
		return countLabel(len(cfg.Worktree.Seed), "seed path")
	}
	return ""
}

func countLabel(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func orDefault(v, def string) string {
	if v == "" {
		return def + " (default)"
	}
	return v
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
