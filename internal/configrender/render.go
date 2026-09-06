// Package configrender owns the resolved-config YAML renderer behind
// `toolbox config show` (with optional git-config-style origin annotations).
// It is a peer of internal/configexample: configexample renders the annotated
// *template* (a docs artefact), this renders the *live resolved state*. Keeping
// it here reduces cmd/config.go to flag parsing + dispatch, matching every
// sibling command.
package configrender

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configedit"
)

// Resolved renders c as a deterministic YAML document covering every config
// key, in config.Keys() order and with each key's shape taken from its row.
//
// It stays hand-rolled rather than marshalled through yaml.v3 because the
// document is not a serialisation of Config: unset scalars render their
// effective fallback, a nil tri-state renders `auto`, an empty scalar renders
// the explicit `""` token, and --origin appends a provenance label as bare
// trailing text (`mounts_root: /tmp/root (./.toolbox.yaml)`) — not a YAML
// comment, so no marshaller emits it. Users pipe this output, so it stays
// deterministic (map keys sorted) and annotation-free unless --origin asks
// (Resolved passes nil provenance).
func Resolved(w io.Writer, c *config.Config) error {
	return ResolvedWithOrigin(w, c, nil, "")
}

// errWriter is the "errors are values" sticky writer. This renderer emits ~30
// lines in sequence and a failure on any of them aborts the whole document, so
// checking each Fprintf inline would bury the YAML shape under error plumbing.
// The first error is retained and every later write becomes a no-op.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, a ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, a...)
}

// fail records the first structural error (a key the renderer has no shape
// for), so it surfaces as a returned error rather than a missing line.
func (e *errWriter) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// quoteIfEmpty renders an empty scalar as the explicit `""` token (matching
// the mounts_root convention) so an unset key reads as deliberately blank
// rather than a dangling `key:`.
func quoteIfEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

// boolPtrStr renders a tri-state *bool config toggle: nil (unset) reads as
// `auto`, otherwise the literal bool. `config show` shows the declared state,
// not a host-derived resolution the renderer can't compute.
func boolPtrStr(p *bool) string {
	if p == nil {
		return "auto"
	}
	if *p {
		return "true"
	}
	return "false"
}

// writeSortedMap renders a string-keyed map as a YAML block with keys sorted
// for determinism: `key: {}` when empty, else `key:` followed by two-space
// `k: v` entries. ann is the origin annotation appended to the header.
func writeSortedMap(e *errWriter, key, ann string, m map[string]string) {
	if len(m) == 0 {
		e.printf("%s: {}%s\n", key, ann)
		return
	}
	e.printf("%s:%s\n", key, ann)
	for _, k := range slices.Sorted(maps.Keys(m)) {
		e.printf("  %s: %s\n", k, m[k])
	}
}

// writeYAMLSlice renders a string slice as a YAML block at the given indent
// depth (0 = top level): `key: []` when empty, else `key:` followed by
// `- item` entries one level deeper. ann is appended to the header.
func writeYAMLSlice(e *errWriter, indent int, key, ann string, items []string) {
	pad := strings.Repeat("  ", indent)
	if len(items) == 0 {
		e.printf("%s%s: []%s\n", pad, key, ann)
		return
	}
	e.printf("%s%s:%s\n", pad, key, ann)
	for _, item := range items {
		e.printf("%s  - %s\n", pad, item)
	}
}

// annotator returns the per-key origin suffix. A nil provenance yields the
// empty string, so the annotated and plain renderers share one code path.
func annotator(prov configedit.Provenance, explicitPath string) func(string) string {
	return func(key string) string {
		if prov == nil {
			return ""
		}
		return " " + prov[key].LabelWithPath(explicitPath)
	}
}

// ResolvedWithOrigin is Resolved plus optional per-key origin annotations
// (git-config --show-origin style). With a nil prov the output is identical to
// the historical renderer.
func ResolvedWithOrigin(w io.Writer, c *config.Config, prov configedit.Provenance, explicitPath string) error {
	if c == nil {
		return fmt.Errorf("config not initialised")
	}
	e := &errWriter{w: w}
	ann := annotator(prov, explicitPath)

	for _, k := range config.Keys() {
		writeKey(e, k, c, ann)
	}
	return e.err
}

// writeKey emits one key, shaped by its row's Kind. The generic shapes read the
// row's accessors, so a new key of an existing shape renders with no edit here;
// only a structurally new shape (writeBlock's three) needs one. An unhandled
// Kind is an error rather than a silently missing line.
func writeKey(e *errWriter, k config.Key, c *config.Config, ann func(string) string) {
	switch k.Kind {
	case config.KindAlias:
		// A deprecated spelling is rendered only as the live key it folds into
		// (it is still tracked in provenance).
	case config.KindEnum, config.KindScalar:
		e.printf("%s: %s%s\n", k.Name, scalarOf(k, c), ann(k.Name))
	case config.KindTri:
		e.printf("%s: %s%s\n", k.Name, boolPtrStr(k.Tri(c)), ann(k.Name))
	case config.KindBool:
		// Same policy as the unhandled Kind below: a row that reads no bool is a
		// broken row, and saying so beats panicking mid-document.
		v := k.Tri(c)
		if v == nil {
			e.fail(fmt.Errorf("config show: bool key %q reads no value", k.Name))
			return
		}
		e.printf("%s: %t%s\n", k.Name, *v, ann(k.Name))
	case config.KindMap:
		writeSortedMap(e, k.Name, ann(k.Name), k.Pairs(c))
	case config.KindList:
		writeYAMLSlice(e, 0, k.Name, ann(k.Name), k.List(c))
	case config.KindBlock:
		writeBlock(e, k, c, ann)
	default:
		e.fail(fmt.Errorf("config show: key %q has no render shape", k.Name))
	}
}

// scalarOf is a scalar key's rendered value: its effective (post-fallback)
// value when the row declares one — straight from config.EffectiveValue, the
// single seam for "what an unset key resolves to", so `config show` and the
// config UI cannot drift — otherwise its raw value, with empty rendered as the
// explicit `""` token.
func scalarOf(k config.Key, c *config.Config) string {
	if v, ok := config.EffectiveValue(c, k.Name); ok {
		return v
	}
	return quoteIfEmpty(k.Str(c))
}

// writeBlock emits the three keys whose entries carry more than one field, so
// no generic shape fits: worktree nests its seed list, and shells and mounts
// annotate every entry individually (they are individually settable keys).
func writeBlock(e *errWriter, k config.Key, c *config.Config, ann func(string) string) {
	switch k.Name {
	case "worktree":
		e.printf("worktree:%s\n", ann("worktree"))
		writeYAMLSlice(e, 1, "seed", "", k.List(c))
	case "shells":
		writeShells(e, c, ann)
	case "mounts":
		writeMounts(e, c, ann)
	default:
		e.fail(fmt.Errorf("config show: block key %q has no render shape", k.Name))
	}
}

// writeShells emits the named-workspace block. Each shell carries its own
// origin annotation and an optional nested env map.
func writeShells(e *errWriter, c *config.Config, ann func(string) string) {
	if len(c.Shells) == 0 {
		e.printf("shells: {}%s\n", ann("shells"))
		return
	}
	e.printf("shells:%s\n", ann("shells"))
	for _, name := range slices.Sorted(maps.Keys(c.Shells)) {
		s := c.Shells[name]
		e.printf("  %s:%s\n", name, ann(configedit.ShellKey(name)))
		e.printf("    path: %s\n", s.Path)
		if len(s.Env) == 0 {
			continue
		}
		e.printf("    env:\n")
		for _, k := range slices.Sorted(maps.Keys(s.Env)) {
			e.printf("      %s: %s\n", k, s.Env[k])
		}
	}
}

// writeMounts emits the mount list. Every optional field is omitted at its zero
// value, so the output stays as short as the declaration was.
func writeMounts(e *errWriter, c *config.Config, ann func(string) string) {
	if len(c.Mounts) == 0 {
		e.printf("mounts: []%s\n", ann("mounts"))
		return
	}
	e.printf("mounts:\n")
	for _, m := range c.Mounts {
		e.printf("  - name: %s%s\n", m.Name, ann(configedit.MountKey(m.Name)))
		if m.Source != "" {
			e.printf("    source: %s\n", m.Source)
		}
		if m.Target != "" {
			e.printf("    target: %s\n", m.Target)
		}
		if m.ReadOnly {
			e.printf("    readonly: true\n")
		}
		if m.CreateIfMissing {
			e.printf("    create_if_missing: true\n")
		}
		if m.SymlinkFrom != "" {
			e.printf("    symlink_from: %s\n", m.SymlinkFrom)
		}
		if m.Disabled {
			e.printf("    disabled: true\n")
		}
	}
}
