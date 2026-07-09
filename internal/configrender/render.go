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
func writeSortedMap[V any](w io.Writer, key, ann string, m map[string]V, val func(V) string) error {
	if len(m) == 0 {
		_, err := fmt.Fprintf(w, "%s: {}%s\n", key, ann)
		return err
	}
	if _, err := fmt.Fprintf(w, "%s:%s\n", key, ann); err != nil {
		return err
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if _, err := fmt.Fprintf(w, "  %s: %s\n", k, val(m[k])); err != nil {
			return err
		}
	}
	return nil
}

// writeYAMLSlice renders a string slice as a YAML block at the given indent
// depth (0 = top level): `key: []` when empty, else `key:` followed by
// `- item` entries one level deeper. ann is appended to the header.
func writeYAMLSlice(w io.Writer, indent int, key, ann string, items []string) error {
	pad := strings.Repeat("  ", indent)
	if len(items) == 0 {
		_, err := fmt.Fprintf(w, "%s%s: []%s\n", pad, key, ann)
		return err
	}
	if _, err := fmt.Fprintf(w, "%s%s:%s\n", pad, key, ann); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := fmt.Fprintf(w, "%s  - %s\n", pad, item); err != nil {
			return err
		}
	}
	return nil
}

// ResolvedWithOrigin is Resolved plus optional per-key origin annotations
// (git-config --show-origin style). With a nil prov the output is identical to
// the historical renderer.
func ResolvedWithOrigin(w io.Writer, c *config.Config, prov configedit.Provenance, explicitPath string) error {
	if c == nil {
		return fmt.Errorf("config not initialised")
	}
	ann := func(key string) string {
		if prov == nil {
			return ""
		}
		return " " + prov[key].LabelWithPath(explicitPath)
	}

	// shell, agent and pull render their effective (post-fallback) value straight
	// from config.EffectiveValue — the single seam for "what an unset key
	// resolves to" — so `config show` and the config UI cannot drift on the
	// fallback. (shell/pull are already normalized by config.Plan, so on live
	// output this is a no-op; routing them keeps the seam the sole owner.)
	shell, _ := config.EffectiveValue(c, "shell")
	if _, err := fmt.Fprintf(w, "shell: %s%s\n", shell, ann("shell")); err != nil {
		return err
	}

	agent, _ := config.EffectiveValue(c, "agent")
	if _, err := fmt.Fprintf(w, "agent: %s%s\n", agent, ann("agent")); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "image: %s%s\n", quoteIfEmpty(c.Image), ann("image")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "registry_mirror: %s%s\n", quoteIfEmpty(c.RegistryMirror), ann("registry_mirror")); err != nil {
		return err
	}
	pull, _ := config.EffectiveValue(c, "pull")
	if _, err := fmt.Fprintf(w, "pull: %s%s\n", pull, ann("pull")); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "mounts_root: %s%s\n", quoteIfEmpty(c.MountsRoot), ann("mounts_root")); err != nil {
		return err
	}

	// Tri-state toggles: nil renders as `auto` (the resolved effective value is
	// host-derived and can't be computed from *Config alone). The deprecated
	// browser_bridge alias is intentionally not rendered — only the canonical
	// bridge key is shown (browser_bridge is still tracked in provenance).
	if _, err := fmt.Fprintf(w, "bridge: %s%s\n", boolPtrStr(c.Bridge), ann("bridge")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "proximo: %s%s\n", boolPtrStr(c.Proximo), ann("proximo")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "managed_statusline: %s%s\n", boolPtrStr(c.ManagedStatusline), ann("managed_statusline")); err != nil {
		return err
	}

	if err := writeSortedMap(w, "sdd", ann("sdd"), c.SDD, func(s config.SDDSkill) string {
		return fmt.Sprintf("%t", s.Enabled)
	}); err != nil {
		return err
	}
	if err := writeSortedMap(w, "env", ann("env"), c.Env, func(v string) string { return v }); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "worktree:%s\n", ann("worktree")); err != nil {
		return err
	}
	if err := writeYAMLSlice(w, 1, "seed", "", c.Worktree.Seed); err != nil {
		return err
	}

	if err := writeYAMLSlice(w, 0, "inherit_host_auth", ann("inherit_host_auth"), c.InheritHostAuth); err != nil {
		return err
	}

	if len(c.Shells) == 0 {
		if _, err := fmt.Fprintf(w, "shells: {}%s\n", ann("shells")); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "shells:%s\n", ann("shells")); err != nil {
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(c.Shells)) {
			s := c.Shells[name]
			if _, err := fmt.Fprintf(w, "  %s:%s\n", name, ann(configedit.ShellKey(name))); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "    path: %s\n", s.Path); err != nil {
				return err
			}
			if len(s.Env) > 0 {
				if _, err := fmt.Fprintln(w, "    env:"); err != nil {
					return err
				}
				for _, k := range slices.Sorted(maps.Keys(s.Env)) {
					if _, err := fmt.Fprintf(w, "      %s: %s\n", k, s.Env[k]); err != nil {
						return err
					}
				}
			}
		}
	}

	if len(c.Mounts) == 0 {
		_, err := fmt.Fprintf(w, "mounts: []%s\n", ann("mounts"))
		return err
	}
	if _, err := fmt.Fprintln(w, "mounts:"); err != nil {
		return err
	}
	for _, m := range c.Mounts {
		if _, err := fmt.Fprintf(w, "  - name: %s%s\n", m.Name, ann(configedit.MountKey(m.Name))); err != nil {
			return err
		}
		if m.Source != "" {
			if _, err := fmt.Fprintf(w, "    source: %s\n", m.Source); err != nil {
				return err
			}
		}
		if m.Target != "" {
			if _, err := fmt.Fprintf(w, "    target: %s\n", m.Target); err != nil {
				return err
			}
		}
		if m.ReadOnly {
			if _, err := fmt.Fprintln(w, "    readonly: true"); err != nil {
				return err
			}
		}
		if m.CreateIfMissing {
			if _, err := fmt.Fprintln(w, "    create_if_missing: true"); err != nil {
				return err
			}
		}
		if m.SymlinkFrom != "" {
			if _, err := fmt.Fprintf(w, "    symlink_from: %s\n", m.SymlinkFrom); err != nil {
				return err
			}
		}
		if m.Disabled {
			if _, err := fmt.Fprintln(w, "    disabled: true"); err != nil {
				return err
			}
		}
	}
	return nil
}
