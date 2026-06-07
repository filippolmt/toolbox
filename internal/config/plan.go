// Package config — Plan + Merge are the external seams that own configuration
// resolution end-to-end. Plan handles the filesystem side (walk-up + file IO);
// Merge is the pure-inspection sibling that layers global / project / explicit
// YAML byte buffers into a fully-validated *Config without touching the
// filesystem. Sections in this file are laid out in the order Plan calls them
// internally (Seam → Walk-Up → Defaults Seeding → File Load → Validation →
// Helpers) so reading top-to-bottom matches the runtime call graph.
package config

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/catalog"
)

// =============================================================================
// Seam (Plan + Merge)
// =============================================================================

// Plan resolves the full configuration pipeline and returns a fully-validated
// *Config. searchFrom is the directory walk-up starts from (typically CWD).
// explicitOverride is the resolved --config flag value ("" when unset).
//
// Errors propagate; no os.Exit inside the Seam — the caller in cmd/ decides
// how to surface failure. Plan owns walk-up + file IO (via LoadLayers); Merge
// (the pure-inspection sibling) owns the byte-level merge mechanics.
func Plan(searchFrom string, explicitOverride string) (*Config, error) {
	global, project, explicit, _, err := LoadLayers(searchFrom, explicitOverride)
	if err != nil {
		return nil, err
	}
	return Merge(global, project, explicit)
}

// LoadLayers performs Plan's filesystem half: it loads the raw global /
// project / explicit YAML byte buffers (any of which may be nil) plus the
// discovered project-file path, without merging or validating. An explicit
// --config override short-circuits global + project loading, mirroring the
// File Load stage in Merge. Exposed so provenance computation and
// `config path` reuse the single load implementation.
func LoadLayers(searchFrom string, explicitOverride string) (global, project, explicit []byte, projectPath string, err error) {
	if explicitOverride != "" {
		b, rerr := os.ReadFile(explicitOverride)
		if rerr != nil {
			return nil, nil, nil, "", fmt.Errorf("read --config %q: %w", explicitOverride, rerr)
		}
		return nil, nil, b, "", nil
	}

	// Global ~/.toolbox.yaml — best-effort. Read AND parse failures are
	// non-fatal: commands that don't need configuration (e.g. `stop --all`)
	// still run when the global file is broken. Errors go to stderr so the
	// user notices the broken file even though startup keeps going.
	if home, herr := os.UserHomeDir(); herr == nil && home != "" {
		globalPath := filepath.Join(home, ".toolbox.yaml")
		b, rerr := os.ReadFile(globalPath)
		switch {
		case rerr == nil:
			if perr := dryParseYAML(b); perr != nil {
				fmt.Fprintf(os.Stderr,
					"toolbox: skipping global config %q: %v\n",
					globalPath, perr)
			} else {
				global = b
			}
		case os.IsNotExist(rerr):
			// OK — global is optional.
		default:
			fmt.Fprintf(os.Stderr,
				"toolbox: skipping global config %q: %v\n",
				globalPath, rerr)
		}
	}

	// Project .toolbox.yaml via walk-up — optional.
	if path := walkUp(searchFrom); path != "" {
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, nil, nil, "", fmt.Errorf("read project config %q: %w", path, rerr)
		}
		project = b
		projectPath = path
	}

	return global, project, nil, projectPath, nil
}

// Merge layers global / project / explicit YAML byte buffers (any of which may
// be nil) into a fully-validated *Config. Pure: no filesystem side-effects.
// Each invocation uses a fresh *viper.Viper so callers see no cross-call state.
func Merge(global, project, explicit []byte) (*Config, error) {
	vp := viper.New()
	vp.SetConfigType("yaml")

	// Defaults seeding. Per-tool toggles no longer exist; only top-level
	// feature flags (browser_bridge) get seeded.
	vp.SetDefault("browser_bridge", true)

	warnLegacyTools(global, project, explicit)

	// File Load stage. explicit short-circuits global + project.
	if len(explicit) > 0 {
		if err := vp.MergeConfig(bytes.NewReader(explicit)); err != nil {
			return nil, fmt.Errorf("parse --config bytes: %w", err)
		}
	} else {
		if len(global) > 0 {
			if err := vp.MergeConfig(bytes.NewReader(global)); err != nil {
				return nil, fmt.Errorf("parse global config bytes: %w", err)
			}
		}
		if len(project) > 0 {
			if err := vp.MergeConfig(bytes.NewReader(project)); err != nil {
				return nil, fmt.Errorf("parse project config bytes: %w", err)
			}
		}
	}

	// Env-prefix overrides — applied to this instance only.
	vp.SetEnvPrefix("TOOLBOX")
	vp.AutomaticEnv()
	warnLegacyToolsEnv()

	// Unmarshal. The custom hook handles the sdd.<key> bool shorthand; the
	// other two re-add viper's defaults, which a custom DecodeHook option
	// replaces rather than extends.
	cfg := &Config{}
	if err := vp.Unmarshal(cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		sddDecodeHook,
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))); err != nil {
		return nil, err
	}

	fillDefaultsBackstop(cfg)

	// Validation tail.
	if err := applyValidationTail(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// =============================================================================
// Walk-Up
// =============================================================================

// WalkUpProjectConfig exposes walk-up discovery for callers outside the load
// path — config-writing commands (`--where local`) need to find the project
// file to patch without loading any layer. Returns the first .toolbox.yaml
// found walking up from start, or "" when none exists.
func WalkUpProjectConfig(start string) string { return walkUp(start) }

// walkUp searches upward from start for a .toolbox.yaml file and returns the
// first match's absolute path. Search stops at the user's HOME directory (so
// the global ~/.toolbox.yaml is not re-read as a project file) and at the
// filesystem root. Returns "" when no project config is found.
//
// HOME-resolution failures are swallowed deliberately: a misconfigured HOME
// must not prevent walk-up from reaching the filesystem root. The home == ""
// short-circuit at the top of the loop becomes a no-op in that case.
func walkUp(start string) string {
	home, _ := os.UserHomeDir()
	cur := filepath.Clean(start)
	for {
		if home != "" && cur == home {
			return ""
		}
		candidate := filepath.Join(cur, ".toolbox.yaml")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// =============================================================================
// Legacy `tools:` Warning
// =============================================================================

// legacyToolsWarnOnce ensures the deprecation warning fires at most once per
// process — Plan is called by almost every cmd/* subcommand (shell, stop,
// config, sdd, …) and spamming the warning on every CLI invocation creates
// scripting noise.
var legacyToolsWarnOnce sync.Once

// warnLegacyToolsEnv detects `TOOLBOX_TOOLS_<KEY>=...` env vars left over
// from the legacy opt-out and emits the same deprecation warning. Shares
// legacyToolsWarnOnce with warnLegacyTools so a user with both a yaml block
// and env vars sees a single line.
func warnLegacyToolsEnv() {
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TOOLBOX_TOOLS_") {
			legacyToolsWarnOnce.Do(func() {
				fmt.Fprintln(os.Stderr,
					"toolbox: warning: TOOLBOX_TOOLS_* env vars are no longer supported and have been removed.\n"+
						"        All bundled CLIs are now installed unconditionally.\n"+
						"        Unset them to silence this warning.\n"+
						"        See: https://github.com/filippolmt/toolbox/blob/main/docs/runtime-notes.md#tools-removal")
			})
			return
		}
	}
}

// warnLegacyTools emits the deprecation warning the first time any input
// buffer carries a top-level `tools:` key. Process-wide one-shot.
func warnLegacyTools(buffers ...[]byte) {
	for _, b := range buffers {
		if len(b) == 0 {
			continue
		}
		if hasTopLevelKey(b, "tools") {
			legacyToolsWarnOnce.Do(func() {
				fmt.Fprintln(os.Stderr,
					"toolbox: warning: 'tools:' is no longer supported and has been removed.\n"+
						"        All bundled CLIs are now installed unconditionally.\n"+
						"        Remove the block from your config to silence this warning.\n"+
						"        See: https://github.com/filippolmt/toolbox/blob/main/docs/runtime-notes.md#tools-removal")
			})
			return
		}
	}
}

// hasTopLevelKey reports whether b decodes to a YAML mapping that contains
// key at the top level. Robust against malformed input: a parse error
// returns false rather than propagating.
func hasTopLevelKey(b []byte, key string) bool {
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// =============================================================================
// Validation
// =============================================================================

// applyValidationTail runs ValidateMountsRoot, the shell default fallback,
// ValidateShell, and InheritHostAuth validation.
func applyValidationTail(cfg *Config) error {
	if err := ValidateMountsRoot(cfg.MountsRoot); err != nil {
		return err
	}
	if cfg.Shell == "" {
		cfg.Shell = "zsh"
	}
	if err := ValidateShell(cfg.Shell); err != nil {
		return err
	}
	if err := validateInheritHostAuth(cfg.InheritHostAuth); err != nil {
		return err
	}
	if err := ValidateSDD(cfg.SDD); err != nil {
		return err
	}
	if err := ValidateEnv(cfg.Env); err != nil {
		return err
	}
	for name, s := range cfg.Shells {
		if err := ValidateEnv(s.Env); err != nil {
			return fmt.Errorf("shells.%s.%w", name, err)
		}
	}
	return nil
}

// validateInheritHostAuth rejects unknown CLI keys, ineligible CLIs (no
// stable host credential path), and duplicate keys. The error message
// enumerates valid eligible keys so the user can copy-paste a fix.
func validateInheritHostAuth(keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	eligible := catalog.HostAuthEligibleKeys()
	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			return fmt.Errorf("inherit_host_auth: duplicate CLI key %q", k)
		}
		seen[k] = struct{}{}
		entry, ok := catalog.Find(k)
		if !ok {
			return fmt.Errorf(
				"inherit_host_auth: unknown CLI %q; valid keys: %s",
				k, strings.Join(eligible, ", "))
		}
		if entry.HostAuthMount == nil {
			return fmt.Errorf(
				"inherit_host_auth: %q does not support host inheritance; valid keys: %s",
				k, strings.Join(eligible, ", "))
		}
	}
	return nil
}

// =============================================================================
// Helpers
// =============================================================================

// fillDefaultsBackstop nil-guards cfg.Shells and seeds cfg.BrowserBridge to
// its default value when omitted. Viper SetDefault values do not always
// surface through Unmarshal for map/pointer types, so the Go-side backstop
// keeps the contract explicit.
func fillDefaultsBackstop(cfg *Config) {
	if cfg.Shells == nil {
		cfg.Shells = map[string]NamedShell{}
	}
	if cfg.BrowserBridge == nil {
		v := true
		cfg.BrowserBridge = &v
	}
}

// sddDecodeHook normalises the two YAML shapes of an sdd: map entry onto
// SDDSkill before mapstructure decodes it:
//
//   - bool shorthand (`gsd: true`) → {"enabled": <bool>}
//   - object form (`gsd: {steps: [...]}`) → "enabled" defaults to true when
//     absent: writing the object at all is the opt-in, so requiring a
//     redundant `enabled: true` line would make every steps override two
//     keys longer for no information.
//
// Returning a map (not a built SDDSkill) keeps mapstructure in charge of
// the field decoding, so steps:-shape errors surface as normal decode
// errors instead of panics here.
func sddDecodeHook(from, to reflect.Type, data any) (any, error) {
	if to != reflect.TypeFor[SDDSkill]() {
		return data, nil
	}
	switch from.Kind() {
	case reflect.Bool:
		return map[string]any{"enabled": data}, nil
	case reflect.Map:
		m, ok := data.(map[string]any)
		if !ok {
			return data, nil
		}
		if _, has := m["enabled"]; has {
			return data, nil
		}
		out := maps.Clone(m)
		out["enabled"] = true
		return out, nil
	default:
		return data, nil
	}
}

// dryParseYAML reports whether b is parseable as YAML. Used by Plan to
// skip a malformed global ~/.toolbox.yaml without failing startup.
func dryParseYAML(b []byte) error {
	vp := viper.New()
	vp.SetConfigType("yaml")
	return vp.ReadConfig(bytes.NewReader(b))
}
