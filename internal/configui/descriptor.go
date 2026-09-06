package configui

import (
	"maps"
	"slices"
	"strings"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

// keyDescriptor is what this package adds to a key's config.Key row: the option
// sets, the Pending Mutation constructor and the display facts that are
// presentation, not schema. The row itself carries the editor kind and the
// typed readers that seed it (Str / Tri / List / Pairs), so a key's shape is
// declared once, in internal/config, and the UI reads it.
//
// The row and this table used to be one switch per axis (effective display,
// per-scope display, collection noun, detail entries, enum options, typed
// accessors, editor kind, writer), so adding a schema key meant finding every
// one of them, and a missed switch surfaced as a blank row or a runtime status
// message with a green suite. The behavioural sweeps in descriptor_test.go are
// what fail now: every key displays something, every editable key opens a
// seeded editor, every open editor has a mutation behind it.
type keyDescriptor struct {
	// options is the fixed option set of an edEnum / edMulti editor. It stays
	// here rather than in the row because one of them (the default mount names)
	// comes from internal/mountplan, which imports config and so cannot be read
	// back from it.
	options func() []string
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
	// detailEntries. Defaults to the row's List; nil for keys with no entries to
	// name.
	entries func(*config.Config) []string
	// scopeEntries names the field, nested inside the key's own node, whose
	// entries the per-scope count reports. Empty means the key's node itself —
	// only worktree names one, because the UI presents its seed list alone.
	scopeEntries string
	// hint is the parenthesised placeholder a free-text scalar shows when it is
	// empty and has no default value to echo.
	hint string
	// display overrides the derived rendering for a key whose effective value
	// fits neither the counted-collection nor the scalar shape.
	display func(*config.Config) string
}

// sddKey is the Config Schema key for the SDD skill packs. Spelled once: the
// reset path dispatches this key's own artefact handling (the .gitignore
// fences) on it, and the descriptor row below must be the same key.
const sddKey = "sdd"

// keyDescriptors is the per-key table of what the row does not carry, keyed by
// the config schema key. Adding a config key the UI edits is its row in
// internal/config plus, when it needs an option set or a writer, an entry here
// (plus its case in TestPreviewMatchesWriterForEveryEditableKey).
var keyDescriptors = map[string]keyDescriptor{
	"mounts": {
		options:  DefaultMountNames,
		selected: DisabledMounts,
		escape:   true,
		mutator:  func(e *editor, _ *config.Config) configedit.Mutator { return configedit.MountsDisabled(e.selected) },

		noun: "override",
		// Counted from the config, not from the names below: an unnamed override
		// is still an override, it just has no label the detail pane can show.
		count:   func(c *config.Config) int { return len(c.Mounts) },
		entries: mountNames,
	},
	"inherit_host_auth": {
		options:  HostAuthOptions,
		selected: func(c *config.Config) map[string]bool { return setOf(c.InheritHostAuth) },
		mutator:  listFromSelection,

		noun: "auth entry",
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
		mutator: func(e *editor, cfg *config.Config) configedit.Mutator { return configedit.Shells(e.shellEntries(cfg)) },

		noun:    "shell",
		entries: func(c *config.Config) []string { return slices.Collect(maps.Keys(c.Shells)) },
	},
	// shell / agent / pull carry a fallback, so their effective display comes
	// from the one config.EffectiveValue seam (guarded by TestRendererParity);
	// the row's Str is the raw value the editor prefills with.
	"shell": {
		options: func() []string { return config.SupportedShells },
		mutator: scalarFromChoice,
	},
	"agent": {
		options: func() []string { return config.SupportedAgents },
		mutator: scalarFromChoice,
	},
	"pull": {
		options: func() []string { return config.SupportedPullPolicies },
		mutator: scalarFromChoice,
	},
	"image": {
		mutator: scalarFromInput,

		hint: "(default)",
	},
	"registry_mirror": {
		mutator: scalarFromInput,

		hint: "(none)",
	},
	"mounts_root": {
		mutator: scalarFromInput,

		hint: "(~/.toolbox)",
	},
	sddKey: {
		options:  SDDOptions,
		selected: EnabledSDD,
		escape:   true,
		mutator:  func(e *editor, _ *config.Config) configedit.Mutator { return configedit.SDDEnabled(e.selected) },

		noun: "pack",
		// Every declared pack, enabled or not — the flag lives on the entry.
		entries: func(c *config.Config) []string { return slices.Collect(maps.Keys(c.SDD)) },
	},
	"bridge":             {mutator: boolFromChoice},
	"proximo":            {mutator: boolFromChoice},
	"managed_statusline": {mutator: boolFromChoice},
	"image_reclaim":      {mutator: boolFromChoice},
	// peer_messaging is a plain bool, not a tri-state: the pointer is always
	// non-nil, so the editor shows true/false and never "auto". Choosing
	// "unset" removes the key, which reads back as true (the default seeded
	// in config.Merge) — the same value as true, written the shorter way.
	"peer_messaging": {mutator: boolFromChoice},
	"env": {
		mutator: func(e *editor, _ *config.Config) configedit.Mutator {
			return configedit.StringMap(e.key, rowsToPairs(e.rows))
		},

		noun:    "var",
		entries: func(c *config.Config) []string { return slices.Collect(maps.Keys(c.Env)) },
	},
	"worktree": {
		mutator: func(e *editor, _ *config.Config) configedit.Mutator {
			return configedit.WorktreeSeed(rowsToValues(e.rows))
		},

		noun:         "seed path",
		scopeEntries: "seed",
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
// the counted-collection and bool shapes, then the shared fallback accessor (so
// the TUI cannot drift from `config show` on the keys that have one), and
// finally the raw scalar with its hint. The value shapes come from the key's
// row; only the overrides are this package's.
func (d keyDescriptor) displayOf(cfg *config.Config, key string) string {
	row, _ := config.KeyByName(key)
	switch {
	case d.display != nil:
		return d.display(cfg)
	case d.noun != "":
		return countLabel(d.countOf(cfg, key), d.noun)
	case row.Tri != nil:
		return triState(row.Tri(cfg))
	}
	if v, ok := config.EffectiveValue(cfg, key); ok {
		return v
	}
	if row.Str != nil {
		return orHint(row.Str(cfg), d.hint)
	}
	return ""
}

// scopeDisplay renders one config file's own value for the key: a counted
// collection shows how many entries that file holds — with the same noun the
// effective display uses, so the two cannot drift — and everything else shows
// the scalar the file wrote. The values are configedit's reading of the file;
// this only chooses which of them the row shows.
func (d keyDescriptor) scopeDisplay(vals map[string]configedit.FileValue, key string) string {
	if d.noun == "" {
		return vals[key].Scalar
	}
	return countLabel(vals[d.scopeEntriesPath(key)].Entries, d.noun)
}

// scopeEntriesPath is where in the file the key's entries live: the key's own
// node, unless the row names a field nested inside it.
func (d keyDescriptor) scopeEntriesPath(key string) string {
	if d.scopeEntries == "" {
		return key
	}
	return key + "." + d.scopeEntries
}

// countOf is how many entries a collection holds: its own counter when the key
// has one, otherwise the entries it can name.
func (d keyDescriptor) countOf(cfg *config.Config, key string) int {
	if d.count != nil {
		return d.count(cfg)
	}
	return len(d.entriesOf(cfg, key))
}

// entriesOf lists a collection's effective entry names, unsorted. A key whose
// entries are exactly its row's list value needs no separate accessor.
func (d keyDescriptor) entriesOf(cfg *config.Config, key string) []string {
	if d.entries != nil {
		return d.entries(cfg)
	}
	if row, ok := config.KeyByName(key); ok && row.List != nil {
		return row.List(cfg)
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
