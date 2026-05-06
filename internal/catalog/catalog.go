// Package catalog owns the canonical declaration of every bundled tool: the
// Tool Catalog. One typed table (Entries) replaces the parallel KnownTools
// slice + ToolBuildArg map that previously lived in internal/config/tools.go.
//
// The package exposes a small surface — Entries, Keys, BuildArg, Defaults,
// IsDefault, WriteCanonical, WriteCanonicalEntries — that the rest of the
// codebase consumes. internal/build uses it for Dockerfile build args and
// the local image hash; internal/config uses it to derive default-tools and
// IsDefault helpers. No back-compat re-exports of the legacy KnownTools /
// ToolBuildArg names live here (D-05): callers migrate to the typed
// accessors.
//
// The Tool Catalog exists to collapse three fan-outs (the KnownTools slice,
// the ToolBuildArg map, and a future Phase 10 init manifest carrying tool
// metadata such as Description / InitScript / SmokeTest) into a single
// declaration. Hash-invalidation invariant: ADDING (or removing) an entry
// here invalidates the `toolbox:local-<hash>` tag for every user with any
// non-default tools config — the hash is computed over the sorted Tools
// map, so a new key shifts the digest even when the user never set it.
// Their next `toolbox shell` will see an "Image not found locally —
// building …" line and rebuild once. Document this in the release notes
// when bumping this list. Optional fields (Description / InitScript /
// SmokeTest) are EXCLUDED from the canonical encoding (D-09, D-10) so a
// future Phase 10 contributor populating them does not shift the image
// hash for existing users.
package catalog

import (
	"fmt"
	"io"
	"strconv"
)

// Entry is a single bundled tool declaration. Key, Default, and BuildArg
// are the load-bearing fields consumed today; Description, InitScript, and
// SmokeTest are reserved for Phase 09/10 (init manifest + smoke-test
// assertions) and are intentionally zero-valued in Phase 07. They MUST NOT
// appear in the canonical encoding (D-10).
type Entry struct {
	Key         string // tool key in .toolbox.yaml `tools:` map
	Default     bool   // default-on/off
	BuildArg    string // Dockerfile ARG name, e.g. "INSTALL_GH"
	Description string // Phase 10: init manifest copy. Phase 07 leaves "".
	InitScript  string // Phase 10: relative path under init.d/. Phase 07 leaves "".
	SmokeTest   string // Phase 09/10: smoke-test assertion key. Phase 07 leaves "".
}

// Entries is the canonical, alphabetical-by-Key declaration of every
// bundled tool. The slice ordering is itself part of the public contract:
// callers that iterate Entries get deterministic order without re-sorting.
//
// Hash-invalidation: adding or removing an entry here shifts the local
// image hash for users with non-default tools maps (see package doc).
//
// Optional fields (Description / InitScript / SmokeTest) are zero-valued
// in Phase 07; Phase 10 populates them. Per D-10, the canonical encoding
// (WriteCanonical / WriteCanonicalEntries) MUST NOT include those fields,
// so populating them in a future phase is hash-neutral.
var Entries = []Entry{
	{Key: "azure", Default: true, BuildArg: "INSTALL_AZURE"},
	{Key: "bat", Default: true, BuildArg: "INSTALL_BAT"},
	{Key: "bun", Default: true, BuildArg: "INSTALL_BUN"},
	{Key: "cf", Default: true, BuildArg: "INSTALL_CF"},
	{Key: "claude", Default: true, BuildArg: "INSTALL_CLAUDE_CODE"},
	{Key: "codex", Default: true, BuildArg: "INSTALL_CODEX_CLI"},
	{Key: "compose", Default: true, BuildArg: "INSTALL_COMPOSE"},
	{Key: "docker", Default: true, BuildArg: "INSTALL_DOCKER_CLI"},
	{Key: "gcloud", Default: true, BuildArg: "INSTALL_GCLOUD"},
	{Key: "gh", Default: true, BuildArg: "INSTALL_GH"},
	{Key: "glab", Default: true, BuildArg: "INSTALL_GLAB"},
	{Key: "go", Default: true, BuildArg: "INSTALL_GO"},
	{Key: "goimports", Default: true, BuildArg: "INSTALL_GOIMPORTS"},
	{Key: "gopls", Default: true, BuildArg: "INSTALL_GOPLS"},
	{Key: "graphify", Default: true, BuildArg: "INSTALL_GRAPHIFY"},
	{Key: "gws", Default: true, BuildArg: "INSTALL_GWS"},
	{Key: "helm", Default: true, BuildArg: "INSTALL_HELM"},
	{Key: "jq", Default: true, BuildArg: "INSTALL_JQ"},
	{Key: "kubectl", Default: true, BuildArg: "INSTALL_KUBECTL"},
	{Key: "oci", Default: true, BuildArg: "INSTALL_OCI"},
	{Key: "playwright", Default: true, BuildArg: "INSTALL_PLAYWRIGHT"},
	{Key: "playwright_cli", Default: true, BuildArg: "INSTALL_PLAYWRIGHT_CLI"},
	{Key: "pnpm", Default: true, BuildArg: "INSTALL_PNPM"},
	{Key: "pyright", Default: true, BuildArg: "INSTALL_PYRIGHT"},
	{Key: "rtk", Default: true, BuildArg: "INSTALL_RTK"},
	{Key: "starship", Default: true, BuildArg: "INSTALL_STARSHIP"},
	{Key: "tofu", Default: true, BuildArg: "INSTALL_TOFU"},
	{Key: "uv", Default: true, BuildArg: "INSTALL_UV"},
	{Key: "yq", Default: true, BuildArg: "INSTALL_YQ"},
	{Key: "zsh", Default: true, BuildArg: "INSTALL_ZSH"},
}

// Keys returns one string per Entry, in catalog (alphabetical) order. A
// fresh slice is allocated on each call so callers cannot alias the
// internal table. Memoisation is intentionally deferred until profiling
// shows it matters (D-04).
func Keys() []string {
	out := make([]string, len(Entries))
	for i, e := range Entries {
		out[i] = e.Key
	}
	return out
}

// BuildArg returns Entry.BuildArg for the Entry whose Key == key, or "" if
// no entry matches. Linear scan is acceptable at the current catalog size
// (D-04); revisit if the catalog grows by an order of magnitude.
func BuildArg(key string) string {
	for _, e := range Entries {
		if e.Key == key {
			return e.BuildArg
		}
	}
	return ""
}

// Defaults returns a fresh map[string]bool with one entry per Entry,
// key=Entry.Key value=Entry.Default. Replaces the body of the legacy
// config.DefaultTools helper.
func Defaults() map[string]bool {
	out := make(map[string]bool, len(Entries))
	for _, e := range Entries {
		out[e.Key] = e.Default
	}
	return out
}

// IsDefault reports whether the given user tools map matches the catalog
// defaults. A missing key is treated as enabled (the Viper default is
// `true`), so a config that never mentions `tools:` evaluates as default.
// Mirrors legacy config.IsDefaultTools semantics verbatim.
func IsDefault(m map[string]bool) bool {
	for _, e := range Entries {
		enabled, present := m[e.Key]
		if present && enabled != e.Default {
			return false
		}
	}
	return true
}

// WriteCanonicalEntries writes a deterministic byte stream representing
// the given entries under the user's enabled-bool overlay. Format:
//
//	tool:<key>|<resolved-bool>|<build-arg>\n
//
// where <resolved-bool> is enabled[e.Key] if present, otherwise e.Default.
// Iteration is in slice order — the caller is responsible for sorting;
// production callers use the package-level Entries which is already
// alphabetical by Key.
//
// Per D-09 / D-10, optional Entry fields (Description, InitScript,
// SmokeTest) MUST NOT appear in the output stream — the function body
// MUST NOT reference them. This is the testable seam exercised by the
// D-10 mutation test (TestCanonicalEncodingIsNeutralToOptionalFieldPopulation):
// populating those fields on a test-local []Entry MUST NOT shift the
// produced bytes.
//
// Returns the first non-nil error from fmt.Fprintf, or nil on success.
func WriteCanonicalEntries(w io.Writer, entries []Entry, enabled map[string]bool) error {
	for _, e := range entries {
		resolved := e.Default
		if v, ok := enabled[e.Key]; ok {
			resolved = v
		}
		if _, err := fmt.Fprintf(w, "tool:%s|%s|%s\n", e.Key, strconv.FormatBool(resolved), e.BuildArg); err != nil {
			return err
		}
	}
	return nil
}

// WriteCanonical writes the canonical byte stream for the package-level
// Entries table under the user's enabled overlay. Thin wrapper over
// WriteCanonicalEntries — production callers (internal/build) use this
// form; tests use either form.
func WriteCanonical(w io.Writer, enabled map[string]bool) error {
	return WriteCanonicalEntries(w, Entries, enabled)
}
