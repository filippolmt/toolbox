// Package configrender owns the resolved-config YAML renderer behind
// `toolbox config show` (with optional git-config-style origin annotations).
// It is a peer of internal/configexample: configexample renders the annotated
// *template* (a docs artefact), this renders the *live resolved state*. Keeping
// it here reduces cmd/config.go to flag parsing + dispatch, matching every
// sibling command, and lets the renderer derive scalar fallbacks from the one
// config.EffectiveValue seam instead of re-hardcoding them.
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

// Resolved renders c as a deterministic YAML document covering every
// config.SchemaKeys() field (TestConfigShowCoversSchema guards that a new field
// can't silently go unrendered). Hand-rolled to avoid promoting the yaml v3
// module to a direct dependency. Users pipe this output: it stays deterministic
// (map keys sorted), so origin annotations live behind the --origin flag
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
// `k: <val(v)>` entries. ann is the origin annotation appended to the header.
func writeSortedMap[V any](e *errWriter, key, ann string, m map[string]V, val func(V) string) {
	if len(m) == 0 {
		e.printf("%s: {}%s\n", key, ann)
		return
	}
	e.printf("%s:%s\n", key, ann)
	for _, k := range slices.Sorted(maps.Keys(m)) {
		e.printf("  %s: %s\n", k, val(m[k]))
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

	writeScalars(e, c, ann)
	writeToggles(e, c, ann)
	writeCollections(e, c, ann)
	writeShells(e, c, ann)
	writeMounts(e, c, ann)

	return e.err
}

// writeScalars emits the plain string-valued keys. shell, agent and pull render
// their effective (post-fallback) value straight from config.EffectiveValue —
// the single seam for "what an unset key resolves to" — so `config show` and
// the config UI cannot drift on the fallback. (shell/pull are already
// normalized by config.Plan, so on live output this is a no-op; routing them
// keeps the seam the sole owner.)
func writeScalars(e *errWriter, c *config.Config, ann func(string) string) {
	shell, _ := config.EffectiveValue(c, "shell")
	e.printf("shell: %s%s\n", shell, ann("shell"))

	agent, _ := config.EffectiveValue(c, "agent")
	e.printf("agent: %s%s\n", agent, ann("agent"))

	e.printf("image: %s%s\n", quoteIfEmpty(c.Image), ann("image"))
	e.printf("registry_mirror: %s%s\n", quoteIfEmpty(c.RegistryMirror), ann("registry_mirror"))

	pull, _ := config.EffectiveValue(c, "pull")
	e.printf("pull: %s%s\n", pull, ann("pull"))

	e.printf("mounts_root: %s%s\n", quoteIfEmpty(c.MountsRoot), ann("mounts_root"))
}

// writeToggles emits the tri-state *bool keys: nil renders as `auto` (the
// resolved effective value is host-derived and can't be computed from *Config
// alone). The deprecated browser_bridge alias is intentionally not rendered —
// only the canonical bridge key is shown (browser_bridge is still tracked in
// provenance).
func writeToggles(e *errWriter, c *config.Config, ann func(string) string) {
	e.printf("bridge: %s%s\n", boolPtrStr(c.Bridge), ann("bridge"))
	e.printf("proximo: %s%s\n", boolPtrStr(c.Proximo), ann("proximo"))
	e.printf("managed_statusline: %s%s\n", boolPtrStr(c.ManagedStatusline), ann("managed_statusline"))
}

// writeCollections emits the map- and slice-valued keys that need no per-entry
// shaping beyond their generic writer.
func writeCollections(e *errWriter, c *config.Config, ann func(string) string) {
	writeSortedMap(e, "sdd", ann("sdd"), c.SDD, func(s config.SDDSkill) string {
		return fmt.Sprintf("%t", s.Enabled)
	})
	writeSortedMap(e, "env", ann("env"), c.Env, func(v string) string { return v })

	e.printf("worktree:%s\n", ann("worktree"))
	writeYAMLSlice(e, 1, "seed", "", c.Worktree.Seed)

	writeYAMLSlice(e, 0, "inherit_host_auth", ann("inherit_host_auth"), c.InheritHostAuth)
}

// writeShells emits the named-workspace block. Each shell carries its own
// origin annotation (they are individually settable keys) and an optional
// nested env map.
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
