package configedit

import (
	"maps"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configio"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sdd"
)

// =============================================================================
// Pending mutations
// =============================================================================
//
// A Mutator is one pending config edit captured as a value: the constructors
// below close over their arguments, so a caller that must both *show* an edit
// and *perform* it holds a single object instead of re-deriving the mutation at
// each site. That re-derivation is what let `config ui`'s preview drift from
// its writers — the preview indexed on the editor kind while the writers
// indexed on the key, and the two disagreed wherever those axes failed to meet.
//
// This is the package's whole edit vocabulary: every edit any surface performs
// is one of these, applied by ApplyChecked at the caller's edge. There is no
// second, write-only spelling of the same node work — a family of typed
// writers used to describe the same six edits again, which left the CLI's edits
// unrenderable and so unable to grow a --dry-run, and let one rule (the mounts
// disable shape) be written twice and drift.

// Mutator edits the top-level document mapping of a config file in place. It is
// the callback shape ApplyChecked, Render and configio.RenderDocument
// all accept, so one value can be written to disk or rendered in memory.
//
// A Mutator is a snapshot: every constructor copies the collection it is handed,
// so the mutation cannot change meaning under a caller that keeps editing its
// own state (the config UI hands over live editor state on every repaint).
type Mutator func(doc *yaml.Node)

// Remove deletes a top-level key.
func Remove(key string) Mutator {
	return func(doc *yaml.Node) { configio.RemoveMapKey(doc, key) }
}

// Scalar writes a top-level scalar key. An empty value removes the key — the
// clean reset-to-default that leaves nothing dangling behind.
func Scalar(key, value string) Mutator {
	if value == "" {
		return Remove(key)
	}
	return func(doc *yaml.Node) { configio.SetMapValue(doc, key, value) }
}

// ScalarEdit is one top-level scalar mutation: the key and the value to write,
// with an empty value meaning "remove" (see Scalar).
type ScalarEdit struct{ Key, Value string }

// Scalars applies several top-level scalar edits as one mutation, in the order
// given — so writing a handful of keys costs a single read-parse-validate-write
// cycle and a rejected value cannot leave a partial edit behind. Each edit is a
// Scalar, so the empty-value-removes rule is stated once.
func Scalars(edits []ScalarEdit) Mutator {
	muts := make([]Mutator, 0, len(edits))
	for _, e := range edits {
		muts = append(muts, Scalar(e.Key, e.Value))
	}
	return func(doc *yaml.Node) {
		for _, m := range muts {
			m(doc)
		}
	}
}

// Bool writes a tri-state bool key. A nil value removes the key so "unset"
// never persists as an explicit false — unset carries its own meaning (e.g.
// proximo unset = auto-detect).
func Bool(key string, v *bool) Mutator {
	if v == nil {
		return Remove(key)
	}
	value := *v
	return func(doc *yaml.Node) { configio.SetMapBool(doc, key, value) }
}

// StringList replaces a top-level string sequence; an empty list removes the key.
func StringList(key string, values []string) Mutator {
	if len(values) == 0 {
		return Remove(key)
	}
	values = slices.Clone(values)
	return func(doc *yaml.Node) {
		replaceSeq(configio.EnsureChildSeq(doc, key), values)
	}
}

// StringMap replaces a top-level string→string mapping (env), written in sorted
// key order for a deterministic file; an empty map removes the key.
func StringMap(key string, pairs map[string]string) Mutator {
	if len(pairs) == 0 {
		return Remove(key)
	}
	pairs = maps.Clone(pairs)
	return func(doc *yaml.Node) {
		node := configio.EnsureChildMap(doc, key)
		node.Content = node.Content[:0]
		for _, k := range slices.Sorted(maps.Keys(pairs)) {
			node.Content = append(node.Content, scalarNode(k), scalarNode(pairs[k]))
		}
	}
}

// Shell writes shells.<name>.path and, when env is non-empty, that entry's env
// overlay — both in one mutation, on purpose. This is the shape `shells add
// --env` commits: one Mutator applied by one ApplyChecked writes both halves of
// the command or neither, where two mutations would be validated, and could be
// rejected, separately — leaving the path on disk and the overlay lost. Sibling
// keys of the entry are preserved, so an env block written earlier survives a
// path change. Callers validate env keys (config.ValidateEnv) beforehand.
func Shell(name, path string, env map[string]string) Mutator {
	overlay := ShellEnv(name, env)
	return func(doc *yaml.Node) {
		entry := configio.EnsureChildMap(configio.EnsureChildMap(doc, "shells"), name)
		configio.SetMapValue(entry, "path", path)
		overlay(doc)
	}
}

// ShellEnv upserts shells.<name>.env.<K>=<V> for every pair in env, applied in
// sorted key order so repeated runs render identically. An empty env writes
// nothing at all — no entry is created for it, which is what lets Shell take
// the same argument optionally. Callers validate keys (config.ValidateEnv)
// before writing.
func ShellEnv(name string, env map[string]string) Mutator {
	if len(env) == 0 {
		return func(*yaml.Node) {}
	}
	env = maps.Clone(env)
	return func(doc *yaml.Node) {
		entry := configio.EnsureChildMap(configio.EnsureChildMap(doc, "shells"), name)
		envMap := configio.EnsureChildMap(entry, "env")
		for _, k := range slices.Sorted(maps.Keys(env)) {
			configio.SetMapValue(envMap, k, env[k])
		}
	}
}

// RemoveShell deletes the shells.<name> entry. A shells: map left empty by the
// removal is dropped entirely; an unknown name changes nothing.
func RemoveShell(name string) Mutator {
	return func(doc *yaml.Node) {
		shells := configio.ChildValue(doc, "shells")
		if !configio.RemoveMapKey(shells, name) {
			return
		}
		if len(shells.Content) == 0 {
			configio.RemoveMapKey(doc, "shells")
		}
	}
}

// ShellEntry is one desired shells: entry for the Shells mutator. OrigName is
// the name the entry carried before editing ("" for a freshly added one); it
// lets a rename carry the source shell's Env overlay to the new name.
type ShellEntry struct {
	Name, Path, OrigName string
	Env                  map[string]string
}

// ShellKeyIn returns the key to write for name given the shell keys a file
// already carries: the existing spelling whenever one normalizes to the same
// key, otherwise the canonical form (config.NormalizeShellKey).
//
// Sole owner of the "which key does a shells: writer touch" rule, shared by
// this package's Shells mutator and cmd's file-at-a-time writers. Both halves
// matter: writing the canonical key keeps a new entry findable by the loader
// (which sees viper's lowercased key), and reusing an existing spelling stops
// an edit from adding a second key that collapses onto the first at load time.
func ShellKeyIn(existing []string, name string) string {
	key := config.NormalizeShellKey(name)
	for _, e := range existing {
		if config.NormalizeShellKey(e) == key {
			return e
		}
	}
	return key
}

// Shells reconciles the shells: block to entries: it removes any shell not
// named by an entry and writes each entry's .path. Names are matched by their
// normalized key (see ShellKeyIn), so an entry the file spells differently is
// edited in place rather than dropped and re-created — which would take its
// env block with it. For an unchanged name the existing env block is left
// untouched (its formatting and comments survive); for a rename the carried Env
// overlay is written under the new name so it is not lost. An empty set removes
// the block.
func Shells(entries []ShellEntry) Mutator {
	if len(entries) == 0 {
		return Remove("shells")
	}
	// Snapshot down to each entry's env overlay, so the mutation is fixed at the
	// moment it is described.
	entries = slices.Clone(entries)
	for i := range entries {
		entries[i].Env = maps.Clone(entries[i].Env)
	}
	return func(doc *yaml.Node) {
		root := configio.EnsureChildMap(doc, "shells")
		// Captured before the removals: the write pass resolves each entry
		// against how the file originally spelled its names.
		spelled := childKeys(root)
		removeUnwantedShells(root, spelled, entries)
		for _, e := range entries {
			writeShellEntry(root, spelled, e)
		}
	}
}

// removeUnwantedShells drops every shell the file spells that no entry names.
// Matching is by normalized key, so a differently-spelled name is recognised as
// the same shell rather than removed as a stranger.
func removeUnwantedShells(root *yaml.Node, spelled []string, entries []ShellEntry) {
	want := make(map[string]bool, len(entries))
	for _, e := range entries {
		want[config.NormalizeShellKey(e.Name)] = true
	}
	for _, name := range spelled {
		if !want[config.NormalizeShellKey(name)] {
			configio.RemoveMapKey(root, name)
		}
	}
}

// writeShellEntry writes one entry's .path and, only on a rename, its carried
// Env overlay under the new name — which would otherwise vanish with the old
// key. An unchanged name keeps its existing env block, formatting and comments
// included.
func writeShellEntry(root *yaml.Node, spelled []string, e ShellEntry) {
	entry := configio.EnsureChildMap(root, ShellKeyIn(spelled, e.Name))
	configio.SetMapValue(entry, "path", e.Path)

	renamed := config.NormalizeShellKey(e.Name) != config.NormalizeShellKey(e.OrigName)
	if !renamed || len(e.Env) == 0 {
		return
	}
	env := configio.EnsureChildMap(entry, "env")
	env.Content = env.Content[:0]
	for _, k := range slices.Sorted(maps.Keys(e.Env)) {
		configio.SetMapValue(env, k, e.Env[k])
	}
}

// WorktreeSeed writes worktree.seed (nested), creating or removing the
// worktree block as needed; an empty list removes the seed key.
func WorktreeSeed(seed []string) Mutator {
	if len(seed) == 0 {
		return func(doc *yaml.Node) {
			wt := configio.ChildValue(doc, "worktree")
			if wt == nil || wt.Kind != yaml.MappingNode {
				return
			}
			configio.RemoveMapKey(wt, "seed")
			if len(wt.Content) == 0 {
				configio.RemoveMapKey(doc, "worktree")
			}
		}
	}
	seed = slices.Clone(seed)
	return func(doc *yaml.Node) {
		wt := configio.EnsureChildMap(doc, "worktree")
		replaceSeq(configio.EnsureChildSeq(wt, "seed"), seed)
	}
}

// SDDKeys returns every registered SDD skill key, sorted — the candidate set
// SDDEnabled reconciles and the option list the config UI offers.
func SDDKeys() []string {
	out := make([]string, 0, len(sdd.Skills))
	for _, s := range sdd.Skills {
		out = append(out, s.Key)
	}
	slices.Sort(out)
	return out
}

// SDDEnabled reconciles the whole sdd: block to enabled — every registered
// skill's flag written or removed in one pass, so the yaml half of an SDD edit
// stays a single transactional mutation (a key carrying a custom steps override
// is left alone while it stays enabled; an emptied sdd block is dropped).
//
// The .gitignore fences are deliberately not part of it: they sit outside
// Doctor's contract and belong after the yaml commit survives validation — see
// ReconcileSDDGitignore.
func SDDEnabled(enabled map[string]bool) Mutator {
	enabled = maps.Clone(enabled)
	return func(doc *yaml.Node) {
		for _, key := range SDDKeys() {
			SetSDDEnabled(doc, key, enabled[key])
		}
	}
}

// Mount writes the replace/append form (name + source + target, optional
// readonly) to the mounts: sequence: an existing entry with the same name is
// replaced in place, otherwise the entry is appended — mirroring how
// mergeMounts reads the list. Callers validate the mount before writing.
func Mount(m config.Mount) Mutator {
	return func(doc *yaml.Node) {
		seq := configio.EnsureChildSeq(doc, "mounts")
		node := mountNode(m)
		if idx, _ := configio.FindSeqEntryByName(seq, m.Name); idx >= 0 {
			seq.Content[idx] = node
			return
		}
		seq.Content = append(seq.Content, node)
	}
}

// MountDisabled marks one name disabled — the single-name peer of
// MountsDisabled, which reconciles the whole default set. Callers validate the
// name against the merged mount list first: a patch naming a mount the merge
// does not know breaks the next config load.
func MountDisabled(name string) Mutator {
	return func(doc *yaml.Node) {
		disableMountIn(configio.EnsureChildSeq(doc, "mounts"), name)
	}
}

// RemoveMount deletes the user-list entry named name from the mounts:
// sequence. Defaults are not represented in the file, so this can only ever
// touch user entries; a mounts: list left empty is dropped entirely, and an
// unknown name changes nothing.
func RemoveMount(name string) Mutator {
	return func(doc *yaml.Node) {
		seq := configio.ChildValue(doc, "mounts")
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return
		}
		idx, _ := configio.FindSeqEntryByName(seq, name)
		if idx < 0 {
			return
		}
		seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
		if len(seq.Content) == 0 {
			configio.RemoveMapKey(doc, "mounts")
		}
	}
}

// disableMountIn marks name disabled inside a mounts: sequence: an existing
// entry gains disabled: true in place (its own fields survive), otherwise the
// `{name, disabled: true}` patch shape mergeMounts reads is appended. The one
// place that rule is written, shared by the single-name MountDisabled and the
// reconciling MountsDisabled.
func disableMountIn(seq *yaml.Node, name string) {
	if _, entry := configio.FindSeqEntryByName(seq, name); entry != nil {
		configio.SetMapBool(entry, "disabled", true)
		return
	}
	patch := &yaml.Node{Kind: yaml.MappingNode}
	configio.SetMapValue(patch, "name", name)
	configio.SetMapBool(patch, "disabled", true)
	seq.Content = append(seq.Content, patch)
}

// mountNode renders a config.Mount as the replace/append mapping shape
// mergeMounts reads. Zero-valued fields are omitted so the file stays
// minimal.
func mountNode(m config.Mount) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	configio.SetMapValue(n, "name", m.Name)
	if m.Source != "" {
		configio.SetMapValue(n, "source", m.Source)
	}
	if m.Target != "" {
		configio.SetMapValue(n, "target", m.Target)
	}
	if m.ReadOnly {
		configio.SetMapBool(n, "readonly", true)
	}
	return n
}

// DefaultMountNames returns the names of the built-in default mounts, sorted —
// the candidate set MountsDisabled reconciles and the option list the config
// UI's mounts editor offers.
func DefaultMountNames() []string {
	var out []string
	for _, m := range mountplan.Defaults() {
		if m.Name != "" {
			out = append(out, m.Name)
		}
	}
	slices.Sort(out)
	return out
}

// MountsDisabled reconciles per-default-mount disable state: it adds the
// `{name, disabled: true}` patch mergeMounts reads for each disabled default
// and drops the patch when the mount is re-enabled. Only pure disable patches
// are removed, so a user's richer patch/replace entry is never clobbered. An
// emptied mounts list is dropped.
func MountsDisabled(disabled map[string]bool) Mutator {
	disabled = maps.Clone(disabled)
	return func(doc *yaml.Node) {
		seq := configio.EnsureChildSeq(doc, "mounts")
		for _, name := range DefaultMountNames() {
			idx, entry := configio.FindSeqEntryByName(seq, name)
			switch {
			case disabled[name]:
				disableMountIn(seq, name)
			case idx >= 0 && isPureDisable(entry):
				seq.Content = append(seq.Content[:idx], seq.Content[idx+1:]...)
			}
		}
		if len(seq.Content) == 0 {
			configio.RemoveMapKey(doc, "mounts")
		}
	}
}

// isPureDisable reports whether a mounts entry is only a disable patch (name +
// disabled), so re-enabling can drop it without discarding real overrides.
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

// replaceSeq rewrites a sequence node to exactly values, reusing the node so an
// existing key keeps its position and comments.
func replaceSeq(seq *yaml.Node, values []string) {
	seq.Content = seq.Content[:0]
	for _, v := range values {
		seq.Content = append(seq.Content, scalarNode(v))
	}
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}
