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
	"slices"
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
// EnvBoundKeys are the only config keys resolvable from a TOOLBOX_* environment
// variable. viper's AutomaticEnv resolves an env var only for a key already in
// its key set, so a key resolves from env iff it is seeded/bound in Merge (the
// loop below is driven by this list). Every other key silently ignores its
// TOOLBOX_* var at Unmarshal, so consumers reasoning about env provenance (e.g.
// configui's read-only marking) MUST consult this set rather than assuming
// every key is env-bindable.
var EnvBoundKeys = []string{"bridge", "image", "registry_mirror", "pull", "peer_messaging"}

// EnvVarSet reports whether key is env-bindable and its TOOLBOX_* variable is
// currently set — the single source of truth for "this key's effective value
// comes from the environment".
func EnvVarSet(key string) bool {
	return slices.Contains(EnvBoundKeys, key) && os.Getenv(envVarName(key)) != ""
}

func envVarName(key string) string { return "TOOLBOX_" + strings.ToUpper(key) }

// be nil) into a fully-validated *Config. Pure: no filesystem side-effects.
// Each invocation uses a fresh *viper.Viper so callers see no cross-call state.
func Merge(global, project, explicit []byte) (*Config, error) {
	vp := viper.New()
	vp.SetConfigType("yaml")

	seedEnvBoundKeys(vp)

	warnLegacyTools(global, project, explicit)
	warnLegacyBrowserBridge(global, project, explicit)

	if err := mergeFileLayers(vp, global, project, explicit); err != nil {
		return nil, err
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

	// viper lowercases every key it unmarshals, silently corrupting
	// case-sensitive environment-variable names (env: {FOO: bar} -> foo=bar).
	// Re-read the env maps from the raw layers with their original case.
	restoreEnvKeyCase(cfg, global, project, explicit)

	fillDefaultsBackstop(cfg)

	// Validation tail.
	if err := applyValidationTail(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// seedEnvBoundKeys seeds the defaults for the env-bindable keys. Per-tool
// toggles no longer exist. The image-selection keys are seeded with their zero
// value not for the value itself but so they land in viper's key set —
// AutomaticEnv only resolves TOOLBOX_* for keys it already knows, so without
// these seeds TOOLBOX_IMAGE / TOOLBOX_REGISTRY_MIRROR / TOOLBOX_PULL would be
// silently ignored at Unmarshal. `bridge` gets BindEnv instead of SetDefault: a
// seeded default surfaces through Unmarshal as a non-nil *bool, which would
// shadow the deprecated browser_bridge fallback in fillDefaultsBackstop (the
// default lives there instead). Same reason browser_bridge itself is neither
// seeded nor bound: non-nil must mean "the user wrote it". Explicit env name:
// SetEnvPrefix runs later in Merge, and BindEnv with a single argument captures
// the prefix at call time. `peer_messaging` is seeded with its real shipped
// value (true) rather than a zero: it is a plain bool, so the seed is the only
// place "absent" and "explicitly false" can be told apart — the file layers are
// merged as raw maps before a single Unmarshal, so a written `peer_messaging:
// false` still wins over the default. Seeding it also makes it env-resolvable,
// which is why it belongs in EnvBoundKeys. Derived from EnvBoundKeys so the
// env-resolvable set has a single source of truth (the same set configui
// consults for env provenance).
func seedEnvBoundKeys(vp *viper.Viper) {
	for _, k := range EnvBoundKeys {
		switch k {
		case "bridge":
			_ = vp.BindEnv(k, envVarName(k))
		case "peer_messaging":
			vp.SetDefault(k, true)
		default:
			vp.SetDefault(k, "")
		}
	}
}

// mergeFileLayers merges the YAML layers in precedence order. explicit
// short-circuits global + project.
func mergeFileLayers(vp *viper.Viper, global, project, explicit []byte) error {
	if len(explicit) > 0 {
		if err := vp.MergeConfig(bytes.NewReader(explicit)); err != nil {
			return fmt.Errorf("parse --config bytes: %w", err)
		}
		return nil
	}
	if len(global) > 0 {
		if err := vp.MergeConfig(bytes.NewReader(global)); err != nil {
			return fmt.Errorf("parse global config bytes: %w", err)
		}
	}
	if len(project) > 0 {
		if err := vp.MergeConfig(bytes.NewReader(project)); err != nil {
			return fmt.Errorf("parse project config bytes: %w", err)
		}
	}
	return nil
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

// legacyBrowserBridgeWarnOnce mirrors legacyToolsWarnOnce for the deprecated
// browser_bridge key.
var legacyBrowserBridgeWarnOnce sync.Once

// DeprecatedBridgeKey is the canonical name of the deprecated `browser_bridge`
// spelling that folds into `bridge` (see fillDefaultsBackstop). It is the single
// source of truth for the key string so that node-level consumers outside this
// package (e.g. internal/configui) fold the same key this package does, and any
// future change to the deprecation is a compile-visible edit here rather than a
// silent drift between packages. Must match the BrowserBridge mapstructure tag.
const DeprecatedBridgeKey = "browser_bridge"

// DeprecatedAliases maps each deprecated config key to the live key it folds
// into. fillDefaultsBackstop performs the fold on load, so a file that sets
// only the alias resolves as if it had set the live key — consumers that must
// mirror that fold (config ui's per-scope view) read the relation here rather
// than hard-coding the pair a second time.
func DeprecatedAliases() map[string]string {
	return map[string]string{DeprecatedBridgeKey: "bridge"}
}

// FoldDeprecatedAliases performs the fold, once, for every carrier of a
// config's contents: for each pair in DeprecatedAliases the live key wins, and
// the alias fills in only when the live key is absent. The pair table, the
// precedence and the loop live here; a carrier passes what it alone can answer.
//
// isSet reports whether the carrier sets a key; fold moves the alias's value
// onto the live key. That split is the irreducible part, and it is why one
// function cannot serve both callers outright: the load path asks a *decoded*
// Config whether a tri-state pointer is nil, while a per-file reader
// (configedit.FileValues) asks a *parsed document* whether the key is written
// at all. The two answers differ on purpose — a file writing a bare `bridge:`
// with no value sets the key for the reader and leaves a nil pointer for the
// load path — and the load path depends on its own reading: seedEnvBoundKeys
// leaves `bridge` unseeded precisely so that non-nil means "the user wrote it".
func FoldDeprecatedAliases(isSet func(key string) bool, fold func(alias, live string)) {
	for alias, live := range DeprecatedAliases() {
		if isSet(alias) && !isSet(live) {
			fold(alias, live)
		}
	}
}

// fieldByTag returns the Config field carrying the given mapstructure tag — the
// bridge between the string-keyed vocabulary the schema tables speak (schema
// keys, alias pairs) and the struct that holds the values. Addressable, so a
// caller can write through it.
func fieldByTag(cfg *Config, tag string) (reflect.Value, bool) {
	v := reflect.ValueOf(cfg).Elem()
	for f := range reflect.TypeFor[Config]().Fields() {
		if f.Tag.Get("mapstructure") == tag {
			return v.FieldByName(f.Name), true
		}
	}
	return reflect.Value{}, false
}

// writtenInConfig is the load path's answer to "does this carrier set the key":
// the field is a tri-state pointer and it is not nil. Every deprecated alias is
// such a pointer today — that is what lets an omitted key be told from an
// explicit one — and a future alias on a plain field has no such marker, so it
// reads as unset rather than being folded on a guess.
func writtenInConfig(cfg *Config, key string) bool {
	f, ok := fieldByTag(cfg, key)
	return ok && f.Kind() == reflect.Pointer && !f.IsNil()
}

// copyAliasField moves the alias field's value onto the live one, by tag, so a
// new pair in DeprecatedAliases needs no second edit here.
func copyAliasField(cfg *Config, alias, live string) {
	from, fromOK := fieldByTag(cfg, alias)
	to, toOK := fieldByTag(cfg, live)
	if fromOK && toOK && to.CanSet() && from.Type() == to.Type() {
		to.Set(from)
	}
}

// warnLegacyBrowserBridge emits a deprecation warning the first time any
// input buffer carries a top-level `browser_bridge:` key. The key still
// works (fillDefaultsBackstop folds it into Bridge) — this is a rename
// nudge, not a removal notice.
func warnLegacyBrowserBridge(buffers ...[]byte) {
	for _, b := range buffers {
		if len(b) == 0 {
			continue
		}
		if hasTopLevelKey(b, DeprecatedBridgeKey) {
			legacyBrowserBridgeWarnOnce.Do(func() {
				fmt.Fprintln(os.Stderr,
					"toolbox: warning: 'browser_bridge:' is deprecated — rename the key to 'bridge:'.")
			})
			return
		}
	}
}

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
						"        See: https://github.com/filippolmt/toolbox/blob/main/docs/internals/image-build.md#tools-removal")
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
						"        See: https://github.com/filippolmt/toolbox/blob/main/docs/internals/image-build.md#tools-removal")
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

// rawCaseLayer captures only the maps whose keys are case-sensitive domain data
// (environment-variable names). viper lowercases every unmarshalled key, so
// these are re-read from the raw YAML to recover the original case.
type rawCaseLayer struct {
	Env    map[string]string `yaml:"env"`
	Shells map[string]struct {
		Env map[string]string `yaml:"env"`
	} `yaml:"shells"`
}

// restoreEnvKeyCase overwrites cfg's env maps — the top-level env: and each
// shells.<name>.env — with case-correct keys parsed straight from the raw
// layers, in the precedence viper merged them (explicit alone; else project
// over global, per-key). Without it viper's key-lowercasing would inject
// `foo=bar` for `env: {FOO: bar}`, breaking case-sensitive variable names. Only
// keys were affected; values already survived viper intact. Broken layers are
// skipped silently here — LoadLayers already surfaced the parse error upstream.
func restoreEnvKeyCase(cfg *Config, global, project, explicit []byte) {
	layers := [][]byte{global, project}
	if len(explicit) > 0 {
		layers = [][]byte{explicit}
	}

	topEnv, shellEnv := collectCaseCorrectEnv(layers)

	if len(topEnv) > 0 {
		cfg.Env = topEnv
	}
	// cfg.Shells is keyed by viper's lowercased shell name; match raw names by
	// the same lowering so the case-correct env replaces the corrupted one.
	for rawName, env := range shellEnv {
		key := strings.ToLower(rawName)
		if sh, ok := cfg.Shells[key]; ok {
			sh.Env = env
			cfg.Shells[key] = sh
		}
	}
}

// collectCaseCorrectEnv re-parses the raw layers and returns the case-correct
// top-level env plus the per-shell env, later layers winning per key. A layer
// that fails to parse is skipped silently — LoadLayers already surfaced that
// error upstream.
func collectCaseCorrectEnv(layers [][]byte) (topEnv map[string]string, shellEnv map[string]map[string]string) {
	topEnv = map[string]string{}
	shellEnv = map[string]map[string]string{}
	for _, b := range layers {
		var raw rawCaseLayer
		if len(b) == 0 || yaml.Unmarshal(b, &raw) != nil {
			continue
		}
		maps.Copy(topEnv, raw.Env)
		mergeShellEnv(shellEnv, raw)
	}
	return topEnv, shellEnv
}

// mergeShellEnv folds one layer's per-shell env maps into dst.
func mergeShellEnv(dst map[string]map[string]string, raw rawCaseLayer) {
	for name, sh := range raw.Shells {
		if len(sh.Env) == 0 {
			continue
		}
		if dst[name] == nil {
			dst[name] = map[string]string{}
		}
		maps.Copy(dst[name], sh.Env)
	}
}

// =============================================================================
// Validation
// =============================================================================

// ValidateKey validates one config key's raw scalar value — the per-key half of
// the validation tail, for a surface holding a single key/value pair before any
// Config exists (the `config set` flags today). It reads the key's row, so a
// presentation layer never owns its own flag→validator mapping to drift from
// the load path.
//
// A key whose row declares no Scalar verdict returns nil: a bool toggle has no
// invalid value, and the structural keys (mounts, shells, env, sdd, worktree,
// inherit_host_auth) are validated over the whole resolved Config by the tail —
// which every write goes through anyway, via configedit.ApplyChecked. So does
// an unknown key: this is a fail-fast convenience, not the authority on what a
// config may contain.
func ValidateKey(key, value string) error {
	if k, ok := KeyByName(key); ok && k.Scalar != nil {
		return k.Scalar(value)
	}
	return nil
}

// applyValidationTail defaults the pull/shell scalars, then runs every key
// row's verdict in Config declaration order (first failure wins).
func applyValidationTail(cfg *Config) error {
	// Side-effecting defaults stay explicit — they mutate cfg, so they are not
	// pure validators and don't belong in the key rows.
	if cfg.Pull == "" {
		cfg.Pull = PullAuto
	}
	if cfg.Shell == "" {
		cfg.Shell = "zsh"
	}
	for _, k := range Keys() {
		if err := k.check(cfg); err != nil {
			return err
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

// fillDefaultsBackstop nil-guards cfg.Shells and seeds cfg.Bridge to its
// default value when omitted. Viper SetDefault values do not always surface
// through Unmarshal for map/pointer types, so the Go-side backstop keeps the
// contract explicit. The deprecated spellings fold into their live keys here —
// through FoldDeprecatedAliases, which owns the rule, so the fold this package
// performs on load and the one configedit.FileValues reports per file are the
// same one and cannot drift. Naming the pair by hand here is what made "the
// same way as config.Merge does" a thing another package had to write out.
func fillDefaultsBackstop(cfg *Config) {
	if cfg.Shells == nil {
		cfg.Shells = map[string]NamedShell{}
	}
	FoldDeprecatedAliases(
		func(key string) bool { return writtenInConfig(cfg, key) },
		func(alias, live string) { copyAliasField(cfg, alias, live) },
	)
	if cfg.Bridge == nil {
		v := true
		cfg.Bridge = &v
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
