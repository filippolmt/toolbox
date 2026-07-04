package config

// KeyDoc is the human documentation for one config key: a one-line summary of
// what it does and the built-in default rendered for humans. The interactive
// TUI (internal/configui) reads it so every key carries a description and an
// explicit default — the two things `config ui` lacked.
//
// ponytail: kept to what the TUI shows. The annotated template
// (internal/configexample) keeps its own richer prose; unifying the two into a
// single source is a larger refactor that changes no visible output, so it is
// deliberately out of scope.
type KeyDoc struct {
	Summary string // one line: what the key does
	Default string // human-readable built-in default
}

// KeyDocs returns the per-key documentation keyed by the top-level config key
// (the `mapstructure` tag). Every SchemaKeys() field except the deprecated
// browser_bridge alias has an entry; keydoc_test asserts that coverage.
func KeyDocs() map[string]KeyDoc {
	return map[string]KeyDoc{
		"shell": {
			Summary: "Login shell inside the container. Only zsh is supported.",
			Default: SupportedShells[0],
		},
		"agent": {
			Summary: "Default AI agent auto-launched by `toolbox worktree` sessions (--agent overrides per run).",
			Default: DefaultAgent,
		},
		"image": {
			Summary: "Full image ref override, used verbatim (host/path:tag or digest). Wins over registry_mirror.",
			Default: "canonical ghcr.io/filippolmt/toolbox:latest",
		},
		"registry_mirror": {
			Summary: "Relocate only the registry host of the canonical image (proxy hub / pull-through cache). Bare host[:port][/path], no scheme.",
			Default: "(none)",
		},
		"pull": {
			Summary: "Registry-sync policy every shell: auto (TTL-cached) | always (force) | never (air-gapped).",
			Default: PullAuto,
		},
		"sdd": {
			Summary: "Repo-local Spec-Driven-Development skill packs (gsd, bmad, openspec). Each key flips one integration on.",
			Default: "(none enabled)",
		},
		"proximo": {
			Summary: "Reach local-dev apps served by proximo from inside the container. Tri-state: unset = auto (on iff proximo's CA exists on the host).",
			Default: "auto (on if proximo installed)",
		},
		"bridge": {
			Summary: "Host-side forwarder for xdg-open (browser), code/codium (editor) and proximo. Tri-state: unset = auto (on).",
			Default: "auto (on)",
		},
		"managed_statusline": {
			Summary: "Image-owned Claude Code statusline force-applied to settings.json each shell. Tri-state: unset = auto (on); false keeps your own.",
			Default: "auto (on)",
		},
		"env": {
			Summary: "Arbitrary KEY=VALUE pairs injected into every shell (after curated TOOLBOX_*/PWD). TOOLBOX_* and PWD are reserved.",
			Default: "(none)",
		},
		"shells": {
			Summary: "Reusable named workspaces for `toolbox shell <name>`; each path is bind-mounted path -> path.",
			Default: "(none)",
		},
		"mounts_root": {
			Summary: "Retarget every default mount under ~/.toolbox/ to this prefix. Absolute (/path) or home-relative (~/sub); bare ~ rejected.",
			Default: "~/.toolbox",
		},
		"inherit_host_auth": {
			Summary: "Share host credentials read-only: listed CLIs read the host's credential path instead of the isolated ~/.toolbox/<key>/.",
			Default: "[] (fully isolated)",
		},
		"mounts": {
			Summary: "Patch / replace / append host -> container binds by name; disable a default with `disabled: true`.",
			Default: "(defaults only)",
		},
		"worktree": {
			Summary: "Tune `toolbox worktree`: seed extra repo-relative paths into a new worktree, on top of the built-in defaults.",
			Default: "(built-in seeds only)",
		},
	}
}
