package config

import (
	"fmt"
	"slices"
	"sync"
)

// Kind is a config key's value shape: what `config show` renders, and what a
// lone value for the key can mean.
type Kind int

const (
	// KindUnset is the zero value — a key with no row yet. No surface can
	// present it; TestEveryKeyHasACompleteRow is what names the omission.
	KindUnset Kind = iota
	// KindAlias is a deprecated spelling folded into a live key by the load
	// path. Tracked in provenance, never presented: the live key is what every
	// surface shows.
	KindAlias
	// KindEnum is a scalar with a bounded option set.
	KindEnum
	// KindScalar is a free-text scalar.
	KindScalar
	// KindTri is a *bool whose unset state is a third, meaningful value.
	KindTri
	// KindBool is a plain bool: unset simply is false.
	KindBool
	// KindMap is a key→value collection.
	KindMap
	// KindList is a sequence of scalars.
	KindList
	// KindBlock is a structured collection whose entries carry more than one
	// field, so each surface shapes them itself.
	KindBlock
)

// Editor is the editor `config ui` opens on a key. It lives here rather than in
// internal/configui because "how a human picks a new value for this key" is a
// fact about the key's shape, not about the widget that happens to draw it —
// and every other per-key fact is declared in the same row.
type Editor int

const (
	// EditorNone is the zero value: a key the UI does not edit.
	EditorNone Editor = iota
	// EditorChoice picks one member of a bounded option set.
	EditorChoice
	// EditorText types a free scalar.
	EditorText
	// EditorTri picks unset / true / false.
	EditorTri
	// EditorSet checks members of a bounded option set.
	EditorSet
	// EditorRows edits free-form entries (key→value pairs, or bare values).
	EditorRows
)

// ExampleListing marks the line of an Example block where a live listing
// belongs — the catalog CLIs eligible for host-auth inheritance, the canonical
// default mounts. The prose is the key's own; neither list is config's to
// compute (internal/mountplan imports config, so config cannot import it back),
// so internal/configexample splices them in at this marker.
const ExampleListing = "#   <listing>\n"

// Key is the one declaration of a config key: the doc and default `config ui`
// shows, the prose `config example` prints, the shape `config show` renders,
// the editor the TUI opens, and the validation and fallback the load path
// applies. Every surface reads the row; none restates it.
//
// Which fields a row must carry follows from its Kind, and
// TestEveryKeyHasACompleteRow is the single guard that says so — the seven
// per-surface presence guards it replaced each asserted one column of this
// table from a different package.
type Key struct {
	// Name is the key's `mapstructure` tag — its spelling in the YAML file.
	Name string
	// Kind is the value shape (see the Kind constants).
	Kind Kind
	// Editor is the editor `config ui` opens on the key.
	Editor Editor

	// Summary is the one-line "what this key does" the TUI shows.
	Summary string
	// Default is the built-in default rendered for humans — the value the TUI
	// echoes for an unset key, and the option an enum editor marks "(default)".
	Default string
	// Example is the annotated block `toolbox config example` prints for the
	// key: comment prose plus a commented-out sample, ending in a newline.
	Example string

	// Str reads a scalar key's raw value (KindEnum, KindScalar).
	Str func(*Config) string
	// Tri reads a bool key's value (KindTri, KindBool).
	Tri func(*Config) *bool
	// List reads a sequence key's values (KindList, and the block keys whose
	// entries are bare values).
	List func(*Config) []string
	// Pairs reads a collection key as key→value (KindMap, and the block keys
	// whose entries have one leading field).
	Pairs func(*Config) map[string]string

	// Effective is the post-fallback value of a scalar key — what the runtime
	// uses when the key is unset. Nil for a key with no fallback, where empty
	// already is the effective value.
	Effective func(*Config) string
	// Validate is the key's half of the validation tail, judged over the whole
	// resolved Config. Nil when no value of the key can be invalid.
	Validate func(*Config) error
	// Scalar is the fail-fast verdict on one raw value, for a surface holding a
	// key/value pair before any Config exists (`config set` flags). Nil when
	// only the tail, over a whole Config, can judge the key.
	Scalar func(string) error
}

// Keys returns every config key's row, in Config declaration order — which is
// also the order every surface presents them in.
func Keys() []Key { return slices.Clone(keyRows) }

// KeyByName returns one key's row.
func KeyByName(name string) (Key, bool) {
	k, ok := keyIndex()[name]
	return k, ok
}

// keyIndex is keyRows by name, built once.
var keyIndex = sync.OnceValue(func() map[string]Key {
	out := make(map[string]Key, len(keyRows))
	for _, k := range keyRows {
		out[k.Name] = k
	}
	return out
})

// keyRows is the table: one row per config key, in Config field order.
var keyRows = []Key{
	{
		Name:    "shell",
		Kind:    KindEnum,
		Editor:  EditorChoice,
		Summary: "Login shell inside the container. Only zsh is supported.",
		Default: SupportedShells[0],
		Example: "# Login shell inside the container. Only zsh is supported.\n" +
			"# shell: zsh\n",
		Str:       func(c *Config) string { return c.Shell },
		Effective: func(c *Config) string { return orElse(c.Shell, SupportedShells[0]) },
		Validate:  func(c *Config) error { return ValidateShell(c.Shell) },
		Scalar:    ValidateShell,
	},
	{
		Name:    "agent",
		Kind:    KindEnum,
		Editor:  EditorChoice,
		Summary: "Default AI agent auto-launched by `toolbox worktree` sessions (--agent overrides per run).",
		Default: DefaultAgent,
		Example: "# agent — default AI agent auto-launched by `toolbox worktree` sessions.\n" +
			"# One of: claude (default) | codex. The --agent flag overrides this per run.\n" +
			"# agent: claude\n",
		Str:       func(c *Config) string { return c.Agent },
		Effective: func(c *Config) string { return orElse(c.Agent, DefaultAgent) },
		Validate:  func(c *Config) error { return ValidateAgent(c.Agent) },
		Scalar:    ValidateAgent,
	},
	{
		Name:    "image",
		Kind:    KindScalar,
		Editor:  EditorText,
		Summary: "Full image ref override, used verbatim (host/path:tag or digest). Wins over registry_mirror.",
		Default: "canonical ghcr.io/filippolmt/toolbox:latest",
		Example: "# image — full ref override, used verbatim (host/path:tag or digest).\n" +
			"# Opt-in, like every image selector: unset, the canonical\n" +
			"# ghcr.io/filippolmt/toolbox:latest is pulled. Wins over registry_mirror.\n" +
			"# Note: a local `toolbox build` tags the canonical ref, so it won't satisfy\n" +
			"# a full override.\n" +
			"# image: harbor.corp.io/team/toolbox:pinned\n",
		Str:      func(c *Config) string { return c.Image },
		Validate: func(c *Config) error { return ValidateImageRef(c.Image) },
		Scalar:   ValidateImageRef,
	},
	{
		Name:    "registry_mirror",
		Kind:    KindScalar,
		Editor:  EditorText,
		Summary: "Relocate only the registry host of the canonical image (proxy hub / pull-through cache). Bare host[:port][/path], no scheme.",
		Default: defaultNone,
		Example: "# registry_mirror — relocate ONLY the registry host: point the canonical\n" +
			"# image at a proxy hub / pull-through cache (Harbor, Artifactory, Nexus, ECR\n" +
			"# pull-through). Bare host[:port][/path], no scheme. Ignored when image is set.\n" +
			"# registry_mirror: harbor.corp.io/ghcr-proxy\n",
		Str:      func(c *Config) string { return c.RegistryMirror },
		Validate: func(c *Config) error { return ValidateRegistryMirror(c.RegistryMirror) },
		Scalar:   ValidateRegistryMirror,
	},
	{
		Name:    "pull",
		Kind:    KindEnum,
		Editor:  EditorChoice,
		Summary: "Registry-sync policy for the shell-start refresh and the background prefetch: auto (TTL-cached) | always (force at start) | never (air-gapped, prefetch off).",
		Default: PullAuto,
		Example: "# pull — registry-sync policy on every shell:\n" +
			"#   auto   (default) best-effort, TTL-cached refresh\n" +
			"#   always force a pull every shell (bypass the TTL cache)\n" +
			"#   never  skip the registry entirely (air-gapped; the local image must\n" +
			"#          already be present)\n" +
			"# pull: auto\n",
		Str:       func(c *Config) string { return c.Pull },
		Effective: func(c *Config) string { return orElse(c.Pull, PullAuto) },
		Validate:  func(c *Config) error { return ValidatePull(c.Pull) },
		Scalar:    ValidatePull,
	},
	{
		Name:    "mounts_root",
		Kind:    KindScalar,
		Editor:  EditorText,
		Summary: "Retarget every default mount under ~/.toolbox/ to this prefix. Absolute (/path) or home-relative (~/sub); bare ~ rejected.",
		Default: "~/.toolbox",
		Example: "# Retarget every default mount whose source lives under ~/.toolbox/ to the\n" +
			"# given prefix. Must be absolute (/path) or strictly home-relative (~/sub).\n" +
			"# Bare \"~\" is rejected — it would defeat credential isolation.\n" +
			"# mounts_root: ~/work-toolbox\n",
		Str:      func(c *Config) string { return c.MountsRoot },
		Validate: func(c *Config) error { return ValidateMountsRoot(c.MountsRoot) },
		Scalar:   ValidateMountsRoot,
	},
	{
		Name:    "bridge",
		Kind:    KindTri,
		Editor:  EditorTri,
		Summary: "Host-side forwarder for xdg-open (browser), code/codium (editor) and proximo. Tri-state: unset = auto (on).",
		Default: defaultAutoOn,
		Example: "# bridge — host-side forwarder for xdg-open (browser), code/codium (editor)\n" +
			"# and allowlisted proximo subcommands. Tri-state, default AUTO (on):\n" +
			"# install it with `toolbox bridge install`. Set false to skip the bridge\n" +
			"# mounts entirely.\n" +
			"# (browser_bridge is the deprecated spelling — use bridge.)\n" +
			"# bridge: true\n",
		Tri: func(c *Config) *bool { return c.Bridge },
	},
	{
		Name: DeprecatedBridgeKey,
		Kind: KindAlias,
	},
	{
		Name:    "proximo",
		Kind:    KindTri,
		Editor:  EditorTri,
		Summary: "Reach local-dev apps served by proximo from inside the container. Tri-state: unset = auto (on iff proximo's CA exists on the host).",
		Default: "auto (on if proximo installed)",
		Example: "# proximo — reach local-dev apps served by proximo from inside the container\n" +
			"# (https://github.com/filippolmt/proximo). Tri-state, default AUTO: omit this key\n" +
			"# and the integration turns on by itself iff proximo is installed on the host\n" +
			"# (its root CA exists) — no per-repo opt-in. Set `true` to force on, `false` to\n" +
			"# opt out. When on, `toolbox shell` discovers every running container labelled\n" +
			"# `proximo.hosts=…`, pins each routed hostname to the Docker host-gateway (so\n" +
			"# https://<name>.test reaches the host's Traefik, not the container's loopback)\n" +
			"# for ANY client, and trusts proximo's CA seamlessly: curl/git/wget/python-ssl\n" +
			"# (system bundle), chromium incl. Playwright (NSS), Node (NODE_EXTRA_CA_CERTS).\n" +
			"# Only python-requests needs a nudge: REQUESTS_CA_BUNDLE=$TOOLBOX_PROXIMO_CA.\n" +
			"# Extra-hosts are fixed at container creation — re-run `toolbox shell` for new hosts.\n" +
			"# proximo: false\n",
		Tri: func(c *Config) *bool { return c.Proximo },
	},
	{
		Name:    "managed_statusline",
		Kind:    KindTri,
		Editor:  EditorTri,
		Summary: "Image-owned Claude Code statusline force-applied to settings.json each shell. Tri-state: unset = auto (on); false keeps your own.",
		Default: defaultAutoOn,
		Example: "# managed_statusline — image-owned Claude Code statusline force-applied to\n" +
			"# ~/.claude/settings.json on every shell start. Tri-state, default AUTO (on):\n" +
			"# set false to keep your own statusLine untouched.\n" +
			"# managed_statusline: false\n",
		Tri: func(c *Config) *bool { return c.ManagedStatusline },
	},
	{
		Name:    "image_reclaim",
		Kind:    KindTri,
		Editor:  EditorTri,
		Summary: "Reclaim runtime images this CLI pulled that a later `latest` lost its tag to. Tri-state: unset = auto (on).",
		Default: defaultAutoOn,
		Example: "# image_reclaim — remove the runtime images this CLI pulled that a later\n" +
			"# `latest` took the tag from. Tri-state, default AUTO (on): the sweep runs\n" +
			"# beside every session and the daemon refuses any image a container still\n" +
			"# references, stopped ones included. Set false to keep every generation.\n" +
			"# image_reclaim: false\n",
		Tri: func(c *Config) *bool { return c.ImageReclaim },
	},
	{
		Name:    "peer_messaging",
		Kind:    KindBool,
		Editor:  EditorTri,
		Summary: "Let Claude Code sessions in different toolbox containers see and message each other (shared PID namespace + socket dir).",
		Default: "true (on)",
		Example: "# peer_messaging — let Claude Code sessions in DIFFERENT toolbox containers\n" +
			"# see and message each other (ListAgents / SendMessage). Participating\n" +
			"# containers join one toolbox-owned PID namespace, which also means they\n" +
			"# see each other's process table, and share a toolbox-owned Docker volume\n" +
			"# (toolbox-cc-socks) as their socket dir. Default TRUE: set false to keep\n" +
			"# every workspace isolated. Per-session override:\n" +
			"# `toolbox shell --peer=false`.\n" +
			"# peer_messaging: false\n",
		Tri: func(c *Config) *bool { return &c.PeerMessaging },
	},
	{
		Name:    "sdd",
		Kind:    KindMap,
		Editor:  EditorSet,
		Summary: "Repo-local Spec-Driven-Development skill packs (gsd, bmad, openspec). Each key flips one integration on.",
		Default: "(none enabled)",
		Example: "# sdd — repo-local Spec-Driven-Development skill packs.\n" +
			"# Each key flips one integration on; default is false (no install).\n" +
			"# On the next `toolbox shell` the entrypoint installs the pinned npm\n" +
			"# package and runs the upstream initialiser inside /workspace.\n" +
			"# Use `toolbox sdd init <name>` to flip a key on AND patch .gitignore.\n" +
			"# Supported keys come from internal/sdd.Skills (Renovate-bumped).\n" +
			"# sdd:\n" +
			"#   gsd: true        # gsd-core skill-form into ./.claude + --codex --local\n" +
			"#   bmad: true       # bmad-method install --yes (ONLY when _bmad/ exists)\n" +
			"#   openspec: true   # openspec init --tools=claude,codex --force, then openspec update\n" +
			"# A key also accepts an object form to override the registry's install\n" +
			"# steps (each inner list is one installer invocation's argv):\n" +
			"#   gsd:\n" +
			"#     steps:\n" +
			"#       - [\"--claude\", \"--global\", \"--config-dir\", \"./.claude\"]\n" +
			"#       - [\"--codex\", \"--local\"]\n" +
			"# bmad bootstrap requires a one-time manual `npx bmad-method install`\n" +
			"# (interactive). After committing _bmad/, the entrypoint auto-upgrades on\n" +
			"# every shell. Missing _bmad/ logs a skip message instead of aborting.\n",
		Pairs: func(c *Config) map[string]string {
			out := make(map[string]string, len(c.SDD))
			for name, s := range c.SDD {
				out[name] = fmt.Sprintf("%t", s.Enabled)
			}
			return out
		},
		Validate: func(c *Config) error { return ValidateSDD(c.SDD) },
	},
	{
		Name:    "env",
		Kind:    KindMap,
		Editor:  EditorRows,
		Summary: "Arbitrary KEY=VALUE pairs injected into every shell (after curated TOOLBOX_*/PWD). TOOLBOX_* and PWD are reserved.",
		Default: defaultNone,
		Example: "# env — arbitrary KEY=VALUE pairs injected into every container shell,\n" +
			"# after the curated TOOLBOX_* / PWD entries. Reserved keys (the TOOLBOX_\n" +
			"# prefix and PWD) are rejected. Per-shell shells.<name>.env overlays this.\n" +
			"# env:\n" +
			"#   CLAUDE_CODE_WORKFLOWS: \"1\"\n",
		Pairs:    func(c *Config) map[string]string { return c.Env },
		Validate: func(c *Config) error { return ValidateEnv(c.Env) },
	},
	{
		Name:    "worktree",
		Kind:    KindBlock,
		Editor:  EditorRows,
		Summary: "Tune `toolbox worktree`: seed extra repo-relative paths into a new worktree, on top of the built-in defaults.",
		Default: "(built-in seeds only)",
		Example: "# worktree — tune `toolbox worktree` sessions.\n" +
			"# seed: extra repo-relative paths to copy from the main repo into a new\n" +
			"# worktree, on top of the built-in defaults (.claude/settings.local.json,\n" +
			"# .env[.*], openspec/, .planning/). Only paths git ignores are copied.\n" +
			"# worktree:\n" +
			"#   seed:\n" +
			"#     - .secrets.local\n" +
			"#     - config/local.yaml\n",
		List:     func(c *Config) []string { return c.Worktree.Seed },
		Validate: func(c *Config) error { return ValidateWorktreeSeed(c.Worktree.Seed) },
	},
	{
		Name:    "inherit_host_auth",
		Kind:    KindList,
		Editor:  EditorSet,
		Summary: "Share host credentials read-only: listed CLIs read the host's credential path instead of the isolated ~/.toolbox/<key>/.",
		Default: "[] (fully isolated)",
		Example: "# inherit_host_auth — share host credentials with the container (read-only).\n" +
			"# Listed CLIs read the host's standard credential path instead of the\n" +
			"# isolated ~/.toolbox/<key>/ default. Default: [] (fully isolated).\n" +
			"# Eligible keys (have a stable host credential path):\n" +
			ExampleListing +
			"# inherit_host_auth: [gh, gcloud]\n",
		List:     func(c *Config) []string { return c.InheritHostAuth },
		Validate: func(c *Config) error { return validateInheritHostAuth(c.InheritHostAuth) },
	},
	{
		Name:    "shells",
		Kind:    KindBlock,
		Editor:  EditorRows,
		Summary: "Reusable named workspaces for `toolbox shell <name>`; each path is bind-mounted path -> path.",
		Default: defaultNone,
		Example: "# shells — reusable named workspaces for `toolbox shell <name>`.\n" +
			"# Each path must be absolute. toolbox bind-mounts path -> path and starts\n" +
			"# the shell in that directory.\n" +
			"# shells:\n" +
			"#   infra:\n" +
			"#     path: /tmp/infra\n",
		Pairs: func(c *Config) map[string]string {
			out := make(map[string]string, len(c.Shells))
			for name, s := range c.Shells {
				out[name] = s.Path
			}
			return out
		},
		Validate: func(c *Config) error {
			for name, s := range c.Shells {
				if err := ValidateEnv(s.Env); err != nil {
					return fmt.Errorf("shells.%s.%w", name, err)
				}
			}
			return nil
		},
	},
	{
		Name:    "mounts",
		Kind:    KindBlock,
		Editor:  EditorSet,
		Summary: "Patch / replace / append host -> container binds by name; disable a default with `disabled: true`.",
		Default: "(defaults only)",
		Example: "# mounts — patch / replace / append host -> container binds.\n" +
			"# Behaviour by `name`:\n" +
			"#   - name matches a default + target empty  -> patch (only set fields override)\n" +
			"#   - name matches a default + target set    -> replace (whole entry swapped)\n" +
			"#   - name does NOT match a default          -> appended after defaults\n" +
			"# Use `disabled: true` to opt a default out without redeclaring it.\n" +
			"#\n" +
			"# Canonical default-mount names (patch/replace targets):\n" +
			ExampleListing +
			"# mounts:\n" +
			"#   - name: gh\n" +
			"#     disabled: true              # drop the default gh mount\n" +
			"#   - name: claude\n" +
			"#     source: ~/work/.toolbox/.claude   # patch: retarget source only\n" +
			"#   - name: extra-cache\n" +
			"#     source: ~/work/cache               # append: brand new mount\n" +
			"#     target: /home/toolbox/.cache/extra\n" +
			"#     readonly: false\n" +
			"#     create_if_missing: true\n",
	},
}

// defaultNone is the Default rendering for a key that ships empty — there is
// no built-in value, not a value that happens to be blank.
const defaultNone = "(none)"

// defaultAutoOn is the Default rendering shared by every tri-state toggle that
// is on when the key is absent (bridge, managed_statusline, image_reclaim).
// One spelling, because the three are one contract: *unset* is not a third
// behaviour, it is the on behaviour written the shorter way.
const defaultAutoOn = "auto (on)"

// orElse returns v, or def when v is empty.
func orElse(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
