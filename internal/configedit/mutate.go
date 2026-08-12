package configedit

import (
	"maps"
	"slices"

	"gopkg.in/yaml.v3"

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

// Mutator edits the top-level document mapping of a config file in place. It is
// the callback shape Upsert, configio.UpsertFile and configio.RenderDocument
// all accept, so one value can be written to disk or rendered in memory.
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
	return func(doc *yaml.Node) {
		seq := configio.EnsureChildSeq(doc, key)
		seq.Content = seq.Content[:0]
		for _, v := range values {
			seq.Content = append(seq.Content, scalarNode(v))
		}
	}
}

// StringMap replaces a top-level string→string mapping (env), written in sorted
// key order for a deterministic file; an empty map removes the key.
func StringMap(key string, pairs map[string]string) Mutator {
	if len(pairs) == 0 {
		return Remove(key)
	}
	return func(doc *yaml.Node) {
		node := configio.EnsureChildMap(doc, key)
		node.Content = node.Content[:0]
		for _, k := range slices.Sorted(maps.Keys(pairs)) {
			node.Content = append(node.Content, scalarNode(k), scalarNode(pairs[k]))
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

// Shells reconciles the shells: block to entries: it removes any shell not
// named by an entry and writes each entry's .path. For an unchanged name the
// existing env block is left untouched (its formatting and comments survive);
// for a rename (Name != OrigName) the carried Env overlay is written under the
// new name so it is not lost. An empty set removes the block.
func Shells(entries []ShellEntry) Mutator {
	if len(entries) == 0 {
		return Remove("shells")
	}
	return func(doc *yaml.Node) {
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
				for _, k := range slices.Sorted(maps.Keys(e.Env)) {
					configio.SetMapValue(env, k, e.Env[k])
				}
			}
		}
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
	return func(doc *yaml.Node) {
		wt := configio.EnsureChildMap(doc, "worktree")
		seq := configio.EnsureChildSeq(wt, "seed")
		seq.Content = seq.Content[:0]
		for _, v := range seed {
			seq.Content = append(seq.Content, scalarNode(v))
		}
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
	return func(doc *yaml.Node) {
		for _, key := range SDDKeys() {
			SetSDDEnabled(doc, key, enabled[key])
		}
	}
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
	return func(doc *yaml.Node) {
		seq := configio.EnsureChildSeq(doc, "mounts")
		for _, name := range DefaultMountNames() {
			idx, entry := configio.FindSeqEntryByName(seq, name)
			switch {
			case disabled[name]:
				if entry != nil {
					configio.SetMapBool(entry, "disabled", true)
					continue
				}
				patch := &yaml.Node{Kind: yaml.MappingNode}
				configio.SetMapValue(patch, "name", name)
				configio.SetMapBool(patch, "disabled", true)
				seq.Content = append(seq.Content, patch)
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

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}
