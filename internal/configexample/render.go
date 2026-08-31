// Package configexample renders the annotated .toolbox.yaml template
// printed by `toolbox config example` and written by `toolbox init`.
package configexample

import (
	"fmt"
	"strings"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// Render returns an annotated `.toolbox.yaml` template covering every
// supported field. The output is generated from the live catalog and
// the canonical mount defaults so a new tool added to internal/catalog
// or a new mount added to internal/mountplan/defaults.go shows up here
// without a second edit.
func Render() string {
	var b strings.Builder

	b.WriteString("# .toolbox.yaml — toolbox configuration.\n")
	b.WriteString("#\n")
	b.WriteString("# Precedence (highest first):\n")
	b.WriteString("#   1. --config <file>\n")
	b.WriteString("#   2. nearest .toolbox.yaml walking up from CWD (project)\n")
	b.WriteString("#   3. ~/.toolbox.yaml (global)\n")
	b.WriteString("#   4. TOOLBOX_* env vars\n")
	b.WriteString("#   5. built-in defaults\n")
	b.WriteString("#\n")
	b.WriteString("# Every field below is optional. Uncomment only what you want to override.\n")
	b.WriteString("\n")

	b.WriteString("# Login shell inside the container. Only zsh is supported.\n")
	b.WriteString("# shell: zsh\n")
	b.WriteString("\n")

	b.WriteString("# agent — default AI agent auto-launched by `toolbox worktree` sessions.\n")
	b.WriteString("# One of: claude (default) | codex | pi. The --agent flag overrides this per run.\n")
	b.WriteString("# agent: claude\n")
	b.WriteString("\n")

	b.WriteString("# Image selection (all opt-in; default = ghcr.io/filippolmt/toolbox:latest).\n")
	b.WriteString("# image            full ref override, used verbatim (host/path:tag or digest).\n")
	b.WriteString("#                  Wins over registry_mirror. Note: a local `toolbox build`\n")
	b.WriteString("#                  tags the canonical ref, so it won't satisfy a full override.\n")
	b.WriteString("# registry_mirror  relocate ONLY the registry host — point the canonical image\n")
	b.WriteString("#                  at a proxy hub / pull-through cache (Harbor, Artifactory,\n")
	b.WriteString("#                  Nexus, ECR pull-through). Bare host[:port][/path], no scheme.\n")
	b.WriteString("# pull             registry-sync policy on every shell:\n")
	b.WriteString("#                    auto   (default) best-effort, TTL-cached refresh\n")
	b.WriteString("#                    always force a pull every shell (bypass the TTL cache)\n")
	b.WriteString("#                    never  skip the registry entirely (air-gapped; the local\n")
	b.WriteString("#                           image must already be present)\n")
	b.WriteString("# image: harbor.corp.io/team/toolbox:pinned\n")
	b.WriteString("# registry_mirror: harbor.corp.io/ghcr-proxy\n")
	b.WriteString("# pull: auto\n")
	b.WriteString("\n")

	b.WriteString("# sdd — repo-local Spec-Driven-Development skill packs.\n")
	b.WriteString("# Each key flips one integration on; default is false (no install).\n")
	b.WriteString("# On the next `toolbox shell` the entrypoint installs the pinned npm\n")
	b.WriteString("# package and runs the upstream initialiser inside /workspace.\n")
	b.WriteString("# Use `toolbox sdd init <name>` to flip a key on AND patch .gitignore.\n")
	b.WriteString("# Supported keys come from internal/sdd.Skills (Renovate-bumped).\n")
	b.WriteString("# sdd:\n")
	b.WriteString("#   gsd: true        # gsd-core skill-form into ./.claude + --codex --local\n")
	b.WriteString("#   bmad: true       # bmad-method install --yes (ONLY when _bmad/ exists)\n")
	b.WriteString("#   openspec: true   # openspec init --tools=claude,codex --force, then openspec update\n")
	b.WriteString("# A key also accepts an object form to override the registry's install\n")
	b.WriteString("# steps (each inner list is one installer invocation's argv):\n")
	b.WriteString("#   gsd:\n")
	b.WriteString("#     steps:\n")
	b.WriteString("#       - [\"--claude\", \"--global\", \"--config-dir\", \"./.claude\"]\n")
	b.WriteString("#       - [\"--codex\", \"--local\"]\n")
	b.WriteString("# bmad bootstrap requires a one-time manual `npx bmad-method install`\n")
	b.WriteString("# (interactive). After committing _bmad/, the entrypoint auto-upgrades on\n")
	b.WriteString("# every shell. Missing _bmad/ logs a skip message instead of aborting.\n")
	b.WriteString("\n")

	b.WriteString("# proximo — reach local-dev apps served by proximo from inside the container\n")
	b.WriteString("# (https://github.com/filippolmt/proximo). Tri-state, default AUTO: omit this key\n")
	b.WriteString("# and the integration turns on by itself iff proximo is installed on the host\n")
	b.WriteString("# (its root CA exists) — no per-repo opt-in. Set `true` to force on, `false` to\n")
	b.WriteString("# opt out. When on, `toolbox shell` discovers every running container labelled\n")
	b.WriteString("# `proximo.hosts=…`, pins each routed hostname to the Docker host-gateway (so\n")
	b.WriteString("# https://<name>.test reaches the host's Traefik, not the container's loopback)\n")
	b.WriteString("# for ANY client, and trusts proximo's CA seamlessly: curl/git/wget/python-ssl\n")
	b.WriteString("# (system bundle), chromium incl. Playwright (NSS), Node (NODE_EXTRA_CA_CERTS).\n")
	b.WriteString("# Only python-requests needs a nudge: REQUESTS_CA_BUNDLE=$TOOLBOX_PROXIMO_CA.\n")
	b.WriteString("# Extra-hosts are fixed at container creation — re-run `toolbox shell` for new hosts.\n")
	b.WriteString("# proximo: false\n")
	b.WriteString("\n")

	b.WriteString("# bridge — host-side forwarder for xdg-open (browser), code/codium (editor)\n")
	b.WriteString("# and proximo up|down|status. Tri-state, default AUTO (on): install it with\n")
	b.WriteString("# `toolbox bridge install`. Set false to skip the bridge mounts entirely.\n")
	b.WriteString("# (browser_bridge is the deprecated spelling — use bridge.)\n")
	b.WriteString("# bridge: true\n")
	b.WriteString("\n")

	b.WriteString("# managed_statusline — image-owned Claude Code statusline force-applied to\n")
	b.WriteString("# ~/.claude/settings.json on every shell start. Tri-state, default AUTO (on):\n")
	b.WriteString("# set false to keep your own statusLine untouched.\n")
	b.WriteString("# managed_statusline: false\n")
	b.WriteString("\n")

	b.WriteString("# peer_messaging — let Claude Code sessions in DIFFERENT toolbox containers\n")
	b.WriteString("# see and message each other (ListAgents / SendMessage). Participating\n")
	b.WriteString("# containers join one toolbox-owned PID namespace, which also means they\n")
	b.WriteString("# see each other's process table, and share a toolbox-owned Docker volume\n")
	b.WriteString("# (toolbox-cc-socks) as their socket dir. Default TRUE: set false to keep\n")
	b.WriteString("# every workspace isolated. Per-session override:\n")
	b.WriteString("# `toolbox shell --peer=false`.\n")
	b.WriteString("# peer_messaging: false\n")
	b.WriteString("\n")

	b.WriteString("# env — arbitrary KEY=VALUE pairs injected into every container shell,\n")
	b.WriteString("# after the curated TOOLBOX_* / PWD entries. Reserved keys (the TOOLBOX_\n")
	b.WriteString("# prefix and PWD) are rejected. Per-shell shells.<name>.env overlays this.\n")
	b.WriteString("# env:\n")
	b.WriteString("#   CLAUDE_CODE_WORKFLOWS: \"1\"\n")
	b.WriteString("\n")

	b.WriteString("# shells — reusable named workspaces for `toolbox shell <name>`.\n")
	b.WriteString("# Each path must be absolute. toolbox bind-mounts path -> path and starts\n")
	b.WriteString("# the shell in that directory.\n")
	b.WriteString("# shells:\n")
	b.WriteString("#   infra:\n")
	b.WriteString("#     path: /tmp/infra\n")
	b.WriteString("\n")

	b.WriteString("# Retarget every default mount whose source lives under ~/.toolbox/ to the\n")
	b.WriteString("# given prefix. Must be absolute (/path) or strictly home-relative (~/sub).\n")
	b.WriteString("# Bare \"~\" is rejected — it would defeat credential isolation.\n")
	b.WriteString("# mounts_root: ~/work-toolbox\n")
	b.WriteString("\n")

	b.WriteString("# inherit_host_auth — share host credentials with the container (read-only).\n")
	b.WriteString("# Listed CLIs read the host's standard credential path instead of the\n")
	b.WriteString("# isolated ~/.toolbox/<key>/ default. Default: [] (fully isolated).\n")
	b.WriteString("# Eligible keys (have a stable host credential path):\n")
	for _, k := range catalog.HostAuthEligibleKeys() {
		entry, ok := catalog.Find(k)
		if !ok || entry.HostAuthMount == nil {
			continue
		}
		fmt.Fprintf(&b, "#   - %-7s  %s -> %s\n", k, entry.HostAuthMount.HostPath, entry.HostAuthMount.ContainerPath)
	}
	b.WriteString("# inherit_host_auth: [gh, gcloud]\n")
	b.WriteString("\n")

	b.WriteString("# mounts — patch / replace / append host -> container binds.\n")
	b.WriteString("# Behaviour by `name`:\n")
	b.WriteString("#   - name matches a default + target empty  -> patch (only set fields override)\n")
	b.WriteString("#   - name matches a default + target set    -> replace (whole entry swapped)\n")
	b.WriteString("#   - name does NOT match a default          -> appended after defaults\n")
	b.WriteString("# Use `disabled: true` to opt a default out without redeclaring it.\n")
	b.WriteString("#\n")
	b.WriteString("# Canonical default-mount names (patch/replace targets):\n")
	for _, m := range mountplan.Defaults() {
		fmt.Fprintf(&b, "#   %-18s %s -> %s\n", m.Name, m.Source, m.Target)
	}
	b.WriteString("# mounts:\n")
	b.WriteString("#   - name: gh\n")
	b.WriteString("#     disabled: true              # drop the default gh mount\n")
	b.WriteString("#   - name: claude\n")
	b.WriteString("#     source: ~/work/.toolbox/.claude   # patch: retarget source only\n")
	b.WriteString("#   - name: extra-cache\n")
	b.WriteString("#     source: ~/work/cache               # append: brand new mount\n")
	b.WriteString("#     target: /home/toolbox/.cache/extra\n")
	b.WriteString("#     readonly: false\n")
	b.WriteString("#     create_if_missing: true\n")
	b.WriteString("\n")

	b.WriteString("# worktree — tune `toolbox worktree` sessions.\n")
	b.WriteString("# seed: extra repo-relative paths to copy from the main repo into a new\n")
	b.WriteString("# worktree, on top of the built-in defaults (.claude/settings.local.json,\n")
	b.WriteString("# .env[.*], openspec/, .planning/). Only paths git ignores are copied.\n")
	b.WriteString("# worktree:\n")
	b.WriteString("#   seed:\n")
	b.WriteString("#     - .secrets.local\n")
	b.WriteString("#     - config/local.yaml\n")

	return b.String()
}
