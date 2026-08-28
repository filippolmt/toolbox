package configui

import (
	"maps"
	"slices"
	"strings"

	yaml "gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/configio"
)

// keyDescriptor is everything this package knows about one config key, in one
// place. The same question — "what shape is this key?" — used to be answered by
// a switch per axis (effective display, per-scope display, collection noun,
// detail entries, enum options, typed accessors, editor kind, writer), so adding
// a schema key meant finding every one of them, and a missed switch surfaced as
// a blank row or a runtime status message with a green suite. It is one row
// here now, and TestKeyDescriptorsCoverEveryKey demands that row for every key
// the UI presents.
//
// The row carries presentation facts only — what the value looks like and which
// editor it opens. Resolution, validation and writing stay in config /
// configedit; configui is a presentation layer over them.
type keyDescriptor struct {
	// kind is the editor `enter` opens on the key.
	kind editorKind
	// options is the fixed option set of an edEnum / edMulti editor.
	options func() []string
	// str is the current effective value of a free-text or enum scalar.
	str func(*config.Config) string
	// tri is the current effective value of a tri-state bool.
	tri func(*config.Config) *bool
	// list is the current effective value of a string-list key.
	list func(*config.Config) []string
	// pairs is the current effective value of a key→value collection.
	pairs func(*config.Config) map[string]string
	// selected is the checked set an edMulti editor opens with.
	selected func(*config.Config) map[string]bool
	// escape marks a key whose structured editor does not cover every case, so
	// the UI also offers the "open in $EDITOR" hatch.
	escape bool

	// mutator builds the pending mutation from the open editor (and the resolved
	// config, which only a rename-aware writer needs) — the one value the
	// preview renders and the save writes.
	mutator func(*editor, *config.Config) configedit.Mutator

	// noun is the singular noun a collection's entries are counted with, shared
	// by the effective display and the per-scope display so the two cannot
	// drift. Empty for keys not rendered as a count.
	noun string
	// count overrides the counted entries (default: the entries listed below).
	count func(*config.Config) int
	// entries lists the effective entry names for the detail pane, sorted by
	// detailEntries. Defaults to list; nil for keys with no entries to name.
	entries func(*config.Config) []string
	// nodeCount counts the entries one config file's own node holds for the key.
	// Nil for keys whose file value is a scalar shown verbatim.
	nodeCount func(*yaml.Node) int
	// hint is the parenthesised placeholder a free-text scalar shows when it is
	// empty and has no default value to echo.
	hint string
	// display overrides the derived rendering for a key whose effective value
	// fits neither the counted-collection nor the scalar shape.
	display func(*config.Config) string
}

// keyDescriptors is the one per-key table, keyed by the config schema key.
// Adding a config key the UI presents is a row here (plus its case in
// TestPreviewMatchesWriterForEveryEditableKey).
var keyDescriptors = map[string]keyDescriptor{
	"mounts": {
		kind:     edMulti,
		options:  DefaultMountNames,
		selected: DisabledMounts,
		escape:   true,
		mutator:  func(e *editor, _ *config.Config) configedit.Mutator { return configedit.MountsDisabled(e.selected) },

		noun: "override",
		// Counted from the config, not from the names below: an unnamed override
		// is still an override, it just has no label the detail pane can show.
		count:     func(c *config.Config) int { return len(c.Mounts) },
		entries:   mountNames,
		nodeCount: seqEntries,
	},
	"inherit_host_auth": {
		kind:     edMulti,
		options:  HostAuthOptions,
		list:     func(c *config.Config) []string { return c.InheritHostAuth },
		selected: func(c *config.Config) map[string]bool { return setOf(c.InheritHostAuth) },
		mutator:  listFromSelection,

		noun:      "auth entry",
		nodeCount: seqEntries,
		// The effective value names the CLIs rather than counting them: the list
		// is short, and which CLI reads host credentials is the point of the key.
		display: func(c *config.Config) string {
			if len(c.InheritHostAuth) == 0 {
				return "(none)"
			}
			return strings.Join(c.InheritHostAuth, ", ")
		},
	},
	"shells": {
		kind:    edRows,
		pairs:   ShellPaths,
		mutator: func(e *editor, cfg *config.Config) configedit.Mutator { return configedit.Shells(e.shellEntries(cfg)) },

		noun:      "shell",
		entries:   func(c *config.Config) []string { return slices.Collect(maps.Keys(c.Shells)) },
		nodeCount: mapEntries,
	},
	// shell / agent / pull carry a fallback, so their effective display comes
	// from the one config.EffectiveValue seam (guarded by TestRendererParity) —
	// str is the raw value the editor prefills with.
	"shell": {
		kind:    edEnum,
		options: func() []string { return config.SupportedShells },
		str:     func(c *config.Config) string { return c.Shell },
		mutator: scalarFromChoice,
	},
	"agent": {
		kind:    edEnum,
		options: func() []string { return config.SupportedAgents },
		str:     func(c *config.Config) string { return c.Agent },
		mutator: scalarFromChoice,
	},
	"pull": {
		kind:    edEnum,
		options: func() []string { return config.SupportedPullPolicies },
		str:     func(c *config.Config) string { return c.Pull },
		mutator: scalarFromChoice,
	},
	"image": {
		kind:    edString,
		str:     func(c *config.Config) string { return c.Image },
		mutator: scalarFromInput,

		hint: "(default)",
	},
	"registry_mirror": {
		kind:    edString,
		str:     func(c *config.Config) string { return c.RegistryMirror },
		mutator: scalarFromInput,

		hint: "(none)",
	},
	"mounts_root": {
		kind:    edString,
		str:     func(c *config.Config) string { return c.MountsRoot },
		mutator: scalarFromInput,

		hint: "(~/.toolbox)",
	},
	"sdd": {
		kind:     edMulti,
		options:  SDDOptions,
		selected: EnabledSDD,
		escape:   true,
		mutator:  func(e *editor, _ *config.Config) configedit.Mutator { return configedit.SDDEnabled(e.selected) },

		noun: "pack",
		// Every declared pack, enabled or not — the flag lives on the entry.
		entries:   func(c *config.Config) []string { return slices.Collect(maps.Keys(c.SDD)) },
		nodeCount: mapEntries,
	},
	"bridge": {
		kind:    edTri,
		tri:     func(c *config.Config) *bool { return c.Bridge },
		mutator: boolFromChoice,
	},
	"proximo": {
		kind:    edTri,
		tri:     func(c *config.Config) *bool { return c.Proximo },
		mutator: boolFromChoice,
	},
	"managed_statusline": {
		kind:    edTri,
		tri:     func(c *config.Config) *bool { return c.ManagedStatusline },
		mutator: boolFromChoice,
	},
	// peer_messaging is a plain bool, not a tri-state: the pointer is always
	// non-nil, so the editor shows true/false and never "auto". Choosing
	// "unset" removes the key, which reads back as true (the default seeded
	// in config.Merge) — the same value as true, written the shorter way.
	"peer_messaging": {
		kind:    edTri,
		tri:     func(c *config.Config) *bool { return &c.PeerMessaging },
		mutator: boolFromChoice,
	},
	"env": {
		kind:  edRows,
		pairs: func(c *config.Config) map[string]string { return c.Env },
		mutator: func(e *editor, _ *config.Config) configedit.Mutator {
			return configedit.StringMap(e.key, rowsToPairs(e.rows))
		},

		noun:      "var",
		entries:   func(c *config.Config) []string { return slices.Collect(maps.Keys(c.Env)) },
		nodeCount: mapEntries,
	},
	"worktree": {
		kind: edRows,
		list: func(c *config.Config) []string { return c.Worktree.Seed },
		mutator: func(e *editor, _ *config.Config) configedit.Mutator {
			return configedit.WorktreeSeed(rowsToValues(e.rows))
		},

		noun:      "seed path",
		nodeCount: seedEntries,
	},
}

// The mutation shapes shared by every key whose edit is just "write the value";
// a key whose write is more than that names its own constructor in the table.
// They read the open editor only — the config parameter is there for the one
// writer (shells) that must look up what it is renaming.

// scalarFromChoice writes the highlighted option of a bounded list.
func scalarFromChoice(e *editor, _ *config.Config) configedit.Mutator {
	return configedit.Scalar(e.key, e.options[e.cursor])
}

// scalarFromInput writes the trimmed contents of a free-text editor.
func scalarFromInput(e *editor, _ *config.Config) configedit.Mutator {
	return configedit.Scalar(e.key, strings.TrimSpace(e.input.Value()))
}

// boolFromChoice writes the tri-state choice (unset removes the key).
func boolFromChoice(e *editor, _ *config.Config) configedit.Mutator {
	return configedit.Bool(e.key, triValue(e.options[e.cursor]))
}

// listFromSelection writes the checked options of a multi-select, in option order.
func listFromSelection(e *editor, _ *config.Config) configedit.Mutator {
	return configedit.StringList(e.key, e.selectedOptions())
}

// displayOf renders a key's effective value: an explicit override first, then
// the counted-collection and tri-state shapes, then the shared fallback
// accessor (so the TUI cannot drift from `config show` on the keys that have
// one), and finally the raw scalar with its hint.
func (d keyDescriptor) displayOf(cfg *config.Config, key string) string {
	switch {
	case d.display != nil:
		return d.display(cfg)
	case d.noun != "":
		return countLabel(d.countOf(cfg), d.noun)
	case d.tri != nil:
		return triState(d.tri(cfg))
	}
	if v, ok := config.EffectiveValue(cfg, key); ok {
		return v
	}
	if d.str != nil {
		return orHint(d.str(cfg), d.hint)
	}
	return ""
}

// countOf is how many entries a collection holds: its own counter when the key
// has one, otherwise the entries it can name.
func (d keyDescriptor) countOf(cfg *config.Config) int {
	if d.count != nil {
		return d.count(cfg)
	}
	return len(d.entriesOf(cfg))
}

// entriesOf lists a collection's effective entry names, unsorted. A key whose
// entries are exactly its list value needs no separate accessor.
func (d keyDescriptor) entriesOf(cfg *config.Config) []string {
	if d.entries != nil {
		return d.entries(cfg)
	}
	if d.list != nil {
		return d.list(cfg)
	}
	return nil
}

// mountNames lists the named mount overrides; an unnamed entry patches nothing
// the detail pane can label, so it is counted but not listed.
func mountNames(cfg *config.Config) []string {
	var names []string
	for _, m := range cfg.Mounts {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names
}

// setOf turns a list of values into the checked set an edMulti editor opens with.
func setOf(vals []string) map[string]bool {
	out := make(map[string]bool, len(vals))
	for _, v := range vals {
		out[v] = true
	}
	return out
}

// mapEntries counts a mapping node's key/value pairs.
func mapEntries(node *yaml.Node) int { return len(node.Content) / 2 }

// seqEntries counts a sequence node's items.
func seqEntries(node *yaml.Node) int { return len(node.Content) }

// seedEntries counts the worktree key's nested seed list — the only entries the
// UI presents for it.
func seedEntries(node *yaml.Node) int {
	seed := configio.ChildValue(node, "seed")
	if seed == nil {
		return 0
	}
	return len(seed.Content)
}
