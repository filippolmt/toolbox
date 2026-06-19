package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/filippolmt/toolbox/internal/sdd"
)

// Config is the top-level toolbox configuration.
//
// The runtime image defaults to the canonical
// `ghcr.io/filippolmt/toolbox:latest` (the image content is identical across
// users — there is no per-tool opt-out). The source can be relocated, opt-in,
// via Image / RegistryMirror; the pull behaviour via Pull. See build.ResolveImage.
//
// InheritHostAuth opts the listed CLIs into reading the host's standard
// credential path (read-only) instead of the isolated `~/.toolbox/<key>/`
// default. Whitelist lives on catalog.Entry.HostAuthMount.
type Config struct {
	Mounts          []Mount               `mapstructure:"mounts"`
	InheritHostAuth []string              `mapstructure:"inherit_host_auth"`
	Shells          map[string]NamedShell `mapstructure:"shells"`
	Shell           string                `mapstructure:"shell"`
	// Image overrides the container image reference verbatim (full host/path:tag
	// or digest). Empty = the canonical default. Highest-precedence image
	// selector — wins over RegistryMirror. Opt-in; a full override is a
	// pull-source concern, so a local `toolbox build` (which tags the canonical
	// ref) no longer satisfies it. See build.ResolveImage.
	Image string `mapstructure:"image"`
	// RegistryMirror relocates only the registry host of the canonical image —
	// the path+tag (`filippolmt/toolbox:latest`) is preserved — so a
	// pull-through cache / proxy hub (Harbor, Artifactory, Nexus, ECR
	// pull-through) serves the identical image. Empty = no relocation. Ignored
	// when Image is set. Value is `host[:port][/path]` with no scheme.
	RegistryMirror string `mapstructure:"registry_mirror"`
	// Pull controls the registry-sync behaviour on every `toolbox shell`:
	//   - "" / "auto" → best-effort TTL-cached refresh (default)
	//   - "always"    → force a pull every shell, bypassing the TTL cache
	//   - "never"     → skip the registry round-trip entirely (air-gapped);
	//     the hard local-presence check (imageplan.Ensure) still applies.
	Pull string `mapstructure:"pull"`
	// MountsRoot retargets every default mount whose Source lives under
	// ~/.toolbox/ to the given prefix. Useful when the user wants every
	// toolbox-managed credential / state dir to live somewhere other than
	// the host home (e.g. an encrypted volume). Empty = use ~/.toolbox/ as
	// before. Per-mount patches in mounts: still win — applied after the
	// root rewrite by mountplan.Merge, so a single override remains
	// possible.
	MountsRoot string `mapstructure:"mounts_root"`
	// SDD opts the workspace into one or more Spec-Driven-Development skill
	// packs (gsd, bmad, openspec, ...) installed repo-locally on every
	// `toolbox shell`. Each `sdd.<key>: true` flag toggles the matching
	// internal/sdd.Skill entry: sessionplan emits TOOLBOX_SDD_ENABLED plus
	// a per-skill spec env var; entrypoint.sh loops them and runs the
	// pinned installer in /workspace.
	//
	// Two YAML shapes per key (sddDecodeHook normalises the bool shorthand):
	//
	//	sdd:
	//	  gsd: true            # registry-default install steps
	//	  gsd:                 # explicit steps override (#317)
	//	    steps:
	//	      - ["--claude", "--global", "--config-dir", "./.claude"]
	SDD map[string]SDDSkill `mapstructure:"sdd"`
	// Bridge toggles the host-side ~/.toolbox/bridge RO mount in the
	// container and gates the `toolbox bridge install` command. When false,
	// the mount is omitted and the install command refuses. Default true.
	Bridge *bool `mapstructure:"bridge"`
	// BrowserBridge is the deprecated spelling of Bridge, kept as input-only
	// compat: fillDefaultsBackstop folds it into Bridge when `bridge:` is
	// absent. Consumers must read Bridge. Remove in a major release.
	BrowserBridge *bool `mapstructure:"browser_bridge"`
	// Proximo controls the proximo (https://github.com/filippolmt/proximo)
	// local-dev integration. Tri-state:
	//   - omitted (nil) → auto: enabled iff proximo is set up on this host
	//     (its root CA exists — path asked to proximo itself, with a
	//     ~/.proximo fallback; see proximo.CAPath). proximo installed →
	//     every toolbox shell reaches `.test` apps with no per-repo opt-in.
	//   - true  → force on (even if the CA is absent; the mount soft-skips).
	//   - false → force off.
	// When enabled, `toolbox shell` discovers every running container labelled
	// `proximo.hosts=…` and pins each routed hostname to the Docker
	// host-gateway (so https://<name>.<tld> reaches the host where proximo's
	// Traefik publishes :443 instead of the container's own loopback) and
	// bind-mounts proximo's root CA read-only. entrypoint.sh then trusts that
	// CA seamlessly for every in-container HTTPS client: update-ca-certificates
	// (curl/git/wget/python-ssl), certutil into ~/.pki/nssdb (Chromium, incl.
	// Playwright's browsers), and NODE_EXTRA_CA_CERTS (Node). TOOLBOX_PROXIMO_CA
	// is exported for the certifi gap (REQUESTS_CA_BUNDLE). Extra-hosts are
	// fixed at container creation, so re-run `toolbox shell` to pick up newly
	// routed hosts. See docs/proximo.md#proximo-integration.
	Proximo *bool `mapstructure:"proximo"`
	// Env injects arbitrary K=V pairs into every shell spawned by the
	// container, emitted after the curated TOOLBOX_* / PWD entries by
	// sessionplan. Hash-neutral (lives outside the removed tools: block) so
	// flipping a key never invalidates the image. Reserved keys — anything
	// with the TOOLBOX_ prefix plus PWD — are rejected at validation time to
	// keep the curated env contract authoritative. Motivating use: opt-in
	// env-gated CLI features like CLAUDE_CODE_WORKFLOWS=1.
	Env map[string]string `mapstructure:"env"`
}

// NamedShell is a shell workspace entry configured under shells:<name>.
type NamedShell struct {
	Path string `mapstructure:"path"`
	// Env overlays the top-level Env for this named shell only. Per-shell
	// keys win on collision; see Config.EffectiveEnv. Same reserved-key
	// rules as the top-level map (validated per entry).
	Env map[string]string `mapstructure:"env"`
}

// SDDSkill is the per-skill value of the sdd: map. The YAML shorthand
// `sdd.<key>: true|false` decodes to {Enabled: <bool>} via sddDecodeHook;
// the object form implies Enabled=true and may override the registry's
// default install steps.
//
// Steps mirrors internal/sdd.Skill.InstallSteps: each inner slice is one
// installer invocation's argv tail. The same token rules apply (validated
// by ValidateSDDSteps): the host→container encoding joins args with spaces
// and steps with the sdd.StepSeparator, and the bash bootstrap re-splits
// on exactly those — a token containing either would silently change the
// arg boundaries inside the container.
type SDDSkill struct {
	Enabled bool       `mapstructure:"enabled"`
	Steps   [][]string `mapstructure:"steps"`
}

// Mount represents a host -> container volume bind.
//
// Inside the user's mounts: list, an entry is interpreted as:
//   - a *patch* of a default when Name matches a default and Target is empty
//     (only non-zero fields override the default; useful for retargeting a
//     single Source);
//   - a *replace* of a default when Name matches a default and Target is set
//     (the entire default entry is swapped for the user's);
//   - an *addition* otherwise (appended after the defaults).
//
// See mountplan.Merge for the full contract.
type Mount struct {
	// Name is a stable alias used by patch/replace targeting. Default mounts
	// populate it; user-declared mounts set it to override a default by name.
	Name     string `mapstructure:"name"`
	Source   string `mapstructure:"source"`
	Target   string `mapstructure:"target"`
	ReadOnly bool   `mapstructure:"readonly"`
	// CreateIfMissing creates the source directory (mode 0700) when absent,
	// instead of skipping the mount. Used for toolbox-managed state dirs.
	CreateIfMissing bool `mapstructure:"create_if_missing"`
	// SymlinkFrom is a host path the Source is symlinked to when the Source
	// does not exist yet. Used to keep toolbox state in sync with host files
	// (e.g. ~/.toolbox/ssh -> ~/.ssh). If SymlinkFrom itself is missing, the
	// mount is skipped with a warning.
	SymlinkFrom string `mapstructure:"symlink_from"`
	// Disabled removes the mount from the resolved set. Used in patches to
	// opt out of a default (e.g. drop the Docker socket) without forcing a
	// full mounts: redeclaration.
	Disabled bool `mapstructure:"disabled"`
}

// HomeMountParents is the fixed in-image HOME under which runtime-user-writable
// subdirs live. Mount targets outside this prefix are not the subject of the
// "Docker auto-creates parents as root:root" bug that mountplan.ParentDirs
// guards against.
const HomeMountParents = "/home/toolbox/"

// SupportedShells is the canonical list of values accepted by the `shell`
// key in ~/.toolbox.yaml. Exposed so tests and error messages can consume a
// single source of truth (D-14).
//
// Bash was removed as an interactive shell option: the runtime image now
// ships only the zsh stack (Oh-My-Zsh + fzf + zoxide + starship). The
// `bash` binary itself is still present (Debian Essential) and remains the
// shebang for init.d scripts and smoke-test.sh, but `toolbox shell` no
// longer attaches a bash login session.
var SupportedShells = []string{"zsh"}

// ValidateShell returns nil when s is a supported shell, or an error listing
// the accepted values (D-15). Used by Plan's validation tail and
// (defensively) by the container shell resolver (sessionplan.ResolveShellCmd).
//
// `bash` is rejected with an explicit migration hint instead of the generic
// "unsupported shell" message so existing ~/.toolbox.yaml files surface the
// breaking change clearly.
func ValidateShell(s string) error {
	if slices.Contains(SupportedShells, s) {
		return nil
	}
	if s == "bash" {
		return fmt.Errorf("shell %q is no longer supported: remove the `shell:` key (default zsh) or set `shell: zsh` explicitly", s)
	}
	return fmt.Errorf("unsupported shell %q: must be one of %s",
		s, strings.Join(SupportedShells, ", "))
}

// ValidateMountsRoot rejects mounts_root values that would silently bind
// the wrong path. Empty is allowed (no override). The value must be either
// absolute (/path) or strictly home-relative with a sub-path (~/sub) so
// the resolver can expand it deterministically. Bare "~" is refused on
// purpose: it would rewrite ~/.toolbox/<x> to ~/<x>, dropping the
// isolation namespace and writing toolbox state straight onto the host
// home (~/.claude, ~/.gitconfig, …) — the exact leak the default mount
// set is designed to prevent. Relative paths are refused too: they would
// resolve against the CWD at toolbox-shell invocation, which is almost
// never what the user wants for a global override.
func ValidateMountsRoot(s string) error {
	if s == "" {
		return nil
	}
	if s == "~" {
		return fmt.Errorf("mounts_root %q is too broad: it would write toolbox state directly under the host home, defeating credential isolation; use a sub-path (e.g. ~/toolbox-state) or an absolute path", s)
	}
	if strings.HasPrefix(s, "~/") {
		return nil
	}
	if filepath.IsAbs(s) {
		return nil
	}
	return fmt.Errorf("mounts_root %q must be absolute or start with ~/", s)
}

// Pull policy values accepted by the `pull` key. These are the single source
// of truth for the literals — validation, the empty→default normalization, the
// `config show` renderer, and imageplan.Refresh's dispatch all reference them
// so a new policy can't drift between sites.
const (
	PullAuto   = "auto"   // best-effort, TTL-cached refresh (default)
	PullAlways = "always" // force a pull every shell, bypassing the TTL cache
	PullNever  = "never"  // skip the registry round-trip entirely (air-gapped)
)

// SupportedPullPolicies is the canonical list accepted by the `pull` key.
// Empty normalises to PullAuto at validation time.
var SupportedPullPolicies = []string{PullAuto, PullAlways, PullNever}

// ValidatePull rejects pull-policy values outside SupportedPullPolicies.
// Empty is allowed (normalised to "auto" by the validation tail).
func ValidatePull(s string) error {
	if s == "" || slices.Contains(SupportedPullPolicies, s) {
		return nil
	}
	return fmt.Errorf("unsupported pull policy %q: must be one of %s",
		s, strings.Join(SupportedPullPolicies, ", "))
}

// ValidateImageRef rejects image overrides that are obviously malformed.
// Empty is allowed (no override). The check is intentionally light — the
// Docker daemon is the real arbiter of ref validity — but whitespace or a
// scheme prefix is always a mistake and would otherwise fail far later with
// an opaque pull error.
func ValidateImageRef(s string) error {
	return validateBareRef("image", s, "(host/path:tag)")
}

// ValidateRegistryMirror rejects mirror prefixes that would not splice cleanly
// onto the canonical image path. Empty is allowed (no relocation). The value
// is a registry host with an optional port and path — never a scheme — so a
// leading "http(s)://" or whitespace is refused up front.
func ValidateRegistryMirror(s string) error {
	if err := validateBareRef("registry_mirror", s, "(host[:port][/path])"); err != nil {
		return err
	}
	// A registry host never starts with "/". Catch it here: build.ResolveImage
	// trims trailing slashes and prepends the mirror, so a leading-slash value
	// ("/", "//", "/harbor.io") would splice into a hostless, malformed ref
	// ("/filippolmt/toolbox:latest") that only fails at docker-pull time.
	if strings.HasPrefix(s, "/") {
		return fmt.Errorf("registry_mirror %q must start with a registry host (host[:port][/path]), not %q", s, "/")
	}
	return nil
}

// validateBareRef is the shared guard behind ValidateImageRef and
// ValidateRegistryMirror: empty is allowed, whitespace and a scheme prefix
// are not. shape is the bare-form hint spliced into the scheme-rejection error.
func validateBareRef(key, s, shape string) error {
	if s == "" {
		return nil
	}
	if strings.ContainsFunc(s, unicode.IsSpace) {
		return fmt.Errorf("%s %q must not contain whitespace", key, s)
	}
	if strings.Contains(s, "://") {
		return fmt.Errorf("%s %q must be a bare ref %s, not a URL", key, s, shape)
	}
	return nil
}

// ValidateSDD checks the steps-override entries of the sdd: map. Bool
// shorthand entries (Steps == nil) are deliberately NOT validated: unknown
// keys there are silently dropped by sessionplan so a typo never aborts the
// shell. An explicit steps: override is different — the user is hand-wiring
// installer argv, so failing loud beats a bootstrap that silently runs the
// wrong layout.
//
// Token rules mirror the internal/sdd.Skill.InstallSteps contract: the
// host→container encoding joins args with spaces and steps with
// sdd.StepSeparator, and entrypoint.sh re-splits on exactly those, so a
// token containing whitespace or the separator would shift arg boundaries
// inside the container instead of erroring anywhere.
func ValidateSDD(m map[string]SDDSkill) error {
	for _, k := range slices.Sorted(maps.Keys(m)) { // deterministic first-error across runs
		v := m[k]
		if v.Steps == nil {
			continue
		}
		if _, ok := sdd.Lookup(k); !ok {
			return fmt.Errorf(
				"sdd.%s: unknown integration for steps override; supported: %s",
				k, strings.Join(sdd.Keys(), ", "))
		}
		if len(v.Steps) == 0 {
			return fmt.Errorf(
				"sdd.%s.steps: must list at least one step (or use `sdd.%s: true` for the registry default)",
				k, k)
		}
		for i, step := range v.Steps {
			if len(step) == 0 {
				return fmt.Errorf("sdd.%s.steps[%d]: step must list at least one argument", k, i)
			}
			for _, tok := range step {
				if tok == "" ||
					strings.Contains(tok, sdd.StepSeparator) ||
					strings.ContainsFunc(tok, unicode.IsSpace) {
					return fmt.Errorf(
						"sdd.%s.steps[%d]: invalid token %q: tokens must be non-empty, whitespace-free, and must not contain %q",
						k, i, tok, sdd.StepSeparator)
				}
			}
		}
	}
	return nil
}

// ReservedEnvPrefix is the namespace owned by the curated session env
// contract (TOOLBOX_HOST_WORKSPACE, TOOLBOX_SDD_*, TOOLBOX_LOOPBACK_BRIDGE_*,
// …). User-supplied env: keys may not collide with it.
const ReservedEnvPrefix = "TOOLBOX_"

// ValidateEnv rejects env: keys that are empty, contain "=", or collide with
// the reserved curated-env namespace (the TOOLBOX_ prefix and the explicitly
// set PWD). Keeping these reserved means the curated entries emitted by
// sessionplan stay authoritative regardless of user config. Values are
// unrestricted — empty values are allowed (export VAR=).
func ValidateEnv(env map[string]string) error {
	for k := range env {
		if k == "" {
			return fmt.Errorf("env: empty key is not allowed")
		}
		if strings.Contains(k, "=") {
			return fmt.Errorf("env: key %q must not contain '='", k)
		}
		if k == "PWD" || strings.HasPrefix(k, ReservedEnvPrefix) {
			return fmt.Errorf(
				"env: key %q is reserved (PWD and the %s prefix are owned by toolbox)",
				k, ReservedEnvPrefix)
		}
	}
	return nil
}

// EffectiveEnv returns the env map injected into a session: the top-level Env
// overlaid with the named shell's Env, where per-shell keys win on collision.
// shellName is the raw shells: config key (cfg.Shells is keyed by the raw
// name, not the sanitized container suffix); "" or an unknown key yields the
// top-level Env. The result is always a fresh map — or nil when both layers
// are empty — so callers never alias cfg state.
func (c *Config) EffectiveEnv(shellName string) map[string]string {
	var override map[string]string
	if shellName != "" {
		if s, ok := c.Shells[shellName]; ok {
			override = s.Env
		}
	}
	if len(c.Env) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(c.Env)+len(override))
	maps.Copy(out, c.Env)
	maps.Copy(out, override)
	return out
}
