#!/usr/bin/env bash
set -euo pipefail

# Inject a passwd/group entry for the runtime UID/GID when missing.
# The container runs with --user <host-uid>:<host-gid>, which rarely matches
# the baked "toolbox" user (uid 1000). Tools that call getpwuid() — notably
# ssh — abort with "No user exists for uid NNN" otherwise, breaking git over
# ssh://. /etc/passwd and /etc/group are chmod 0666 in the Dockerfile so this
# append works without root.
_uid=$(id -u)
_gid=$(id -g)
if ! getent passwd "${_uid}" >/dev/null 2>&1; then
    echo "toolbox:x:${_uid}:${_gid}:toolbox:/home/toolbox:/bin/bash" >> /etc/passwd
fi
if ! getent group "${_gid}" >/dev/null 2>&1; then
    echo "toolbox:x:${_gid}:" >> /etc/group
fi
unset _uid _gid

echo "Toolbox credential check:"

# gh (GitHub CLI)
if gh auth status >/dev/null 2>&1; then
    echo "  gh: configured"
else
    echo "  gh: not configured"
fi

# glab (GitLab CLI)
if glab auth status >/dev/null 2>&1; then
    echo "  glab: configured"
else
    echo "  glab: not configured"
fi

# Conditional checks for cloud CLIs (may be volume-mounted from host)
if command -v gcloud >/dev/null 2>&1; then
    if gcloud auth list --filter=status:ACTIVE --format='value(account)' 2>/dev/null | grep -q .; then
        echo "  gcloud: configured"
    else
        echo "  gcloud: not configured"
    fi
fi

if command -v az >/dev/null 2>&1; then
    if az account show >/dev/null 2>&1; then
        echo "  az: configured"
    else
        echo "  az: not configured"
    fi
fi

if command -v oci >/dev/null 2>&1; then
    # </dev/null: oci prompts "Do you want to create a new config file? [Y/n]"
    # when ~/.oci/config is missing. Without a closed stdin it would block the
    # entrypoint on the container's TTY and never reach the startup hooks.
    if oci iam region list --output table </dev/null >/dev/null 2>&1; then
        echo "  oci: configured"
    else
        echo "  oci: not configured"
    fi
fi

# Built-in: build Claude Code MCP plugins shipped with src/ only.
# Some marketplace plugins ship without node_modules/ or dist/, and the
# installer does not run `npm install && npm run build`. The MCP server
# then fails to start ("cannot find module dist/index.js"). This block is
# idempotent via a marker file written after a successful build.
#
# Sentinel: skip entirely when the plugins cache does not exist — a user
# who has never started Claude Code should not pay any cost here.
#
# Per-plugin decision (only plugins with a `mcp/package.json`):
#   - no `scripts.build` in package.json → nothing to build, skip
#   - marker `.toolbox-built` present    → already built, skip
#   - otherwise → npm install + npm run build, then write marker
# Marker lives inside the versioned plugin dir, so a plugin upgrade
# (new version path) naturally invalidates it and triggers a rebuild.
_plugins_cache="$HOME/.claude/plugins/cache"
if [ -d "$_plugins_cache" ] && command -v npm >/dev/null 2>&1 && command -v node >/dev/null 2>&1; then
    shopt -s nullglob
    _mcp_dirs=( "$_plugins_cache"/*/*/*/mcp )
    shopt -u nullglob
    _header_printed=0
    for _mcp_dir in "${_mcp_dirs[@]}"; do
        [ -d "$_mcp_dir" ] || continue
        [ -f "$_mcp_dir/package.json" ] || continue
        [ -f "$_mcp_dir/.toolbox-built" ] && continue

        # Skip plugins that declare no build step.
        _has_build=$(node -e '
            try {
                const p = require(process.argv[1] + "/package.json");
                process.stdout.write(p.scripts && p.scripts.build ? "1" : "");
            } catch (e) {}
        ' "$_mcp_dir" 2>/dev/null) || _has_build=""
        [ -z "$_has_build" ] && continue

        if [ "$_header_printed" -eq 0 ]; then
            echo ""
            echo "Building Claude Code MCP plugins:"
            _header_printed=1
        fi
        echo "  $(basename "$(dirname "$(dirname "$_mcp_dir")")")"
        # Capture stderr to a per-plugin log so a failed build leaves
        # actionable diagnostics behind. The log lives next to the
        # `.toolbox-built` marker (same bind-mounted plugin dir), so it
        # survives container restarts and the user can inspect it from any
        # later shell. Removed on success to keep the dir tidy.
        _build_log="$_mcp_dir/.toolbox-build-error.log"
        rm -f "$_build_log"
        if (
            cd "$_mcp_dir"
            npm install --silent --no-audit --no-fund >/dev/null 2>>"$_build_log" &&
            npm run build --silent >/dev/null 2>>"$_build_log"
        ); then
            touch "$_mcp_dir/.toolbox-built"
            rm -f "$_build_log"
        else
            echo "    build failed (log: $_build_log)"
            if [ -s "$_build_log" ]; then
                tail -n 5 "$_build_log" | sed 's/^/      /'
            fi
        fi
    done
    unset _mcp_dirs _mcp_dir _header_printed _has_build _build_log
fi
unset _plugins_cache

# Re-register the rtk hooks globally on every shell start.
# `rtk init -g` writes the Bash-tool hook into ~/.claude/settings.json (Claude
# Code default); `rtk init -g --codex` does the same for ~/.codex/config.toml.
# Both hook files live in bind-mounted dirs (~/.toolbox/.claude, ~/.toolbox/.codex),
# so a fresh container or a settings reset would otherwise leave the user
# without the wiring. Idempotent — re-running on an already-wired config is a
# no-op. Output suppressed; failures logged but non-fatal.
#
# Flags:
#   --auto-patch  Claude only — rtk init prompts before patching settings.json
#                 on first run; without this flag the entrypoint would block on
#                 a TTY prompt (or silently skip wiring when stdout/stderr are
#                 redirected). Incompatible with --codex (the Codex path has
#                 no settings.json to patch — it just writes AGENTS.md/RTK.md).
#   </dev/null    belt-and-braces: any future prompt rtk adds gets EOF instead
#                 of a hung container.
# Telemetry is killed at the env layer (RTK_TELEMETRY_DISABLED=1 in the
# Dockerfile), independent of whatever consent state rtk persists locally.
#
# Gated on `command -v <ai-cli>` AND directory presence: the bind-mounts
# auto-create both ~/.claude and ~/.codex even when the corresponding tool is
# opted out (tools.claude=false / tools.codex=false), so a dir-only check would
# happily inject hooks into config files no AI CLI ever reads.
if command -v rtk >/dev/null 2>&1; then
    # Pre-seed ~/.config/rtk/config.toml on first start with tee + telemetry
    # disabled. Belt-and-braces: the primary defenses are RTK_TEE=0 and
    # RTK_TELEMETRY_DISABLED=1 (set in the Dockerfile) — those env vars
    # override the TOML and stay effective across rtk's own file rewrites.
    # The seed exists so that:
    #   - `rtk telemetry status` and similar inspection commands report a
    #     consistent state (the env vars block the runtime path but don't
    #     change what the TOML says about consent)
    #   - users who deliberately unset the env vars still inherit safe
    #     defaults instead of upstream's [tee] enabled = true (which would
    #     write `gh auth status`, `aws sts`, `curl -H Authorization:`
    #     output to ~/.local/share/rtk/tee/*.log)
    #
    # The seed is partial on purpose — rtk merges missing keys from its
    # built-in defaults at runtime. The file persists via the `rtk` bind-
    # mount, so manual edits survive container restarts. Note: a few rtk
    # subcommands rewrite the whole file (notably `rtk telemetry
    # enable/disable`), which would reset both keys back to upstream
    # defaults — but RTK_TEE=0 / RTK_TELEMETRY_DISABLED=1 keep working
    # regardless, so the runtime behaviour stays safe.
    if [ ! -f "$HOME/.config/rtk/config.toml" ]; then
        mkdir -p "$HOME/.config/rtk"
        cat > "$HOME/.config/rtk/config.toml" <<'EOF'
# Generated by toolbox entrypoint on first launch. Edit freely — this file is
# regenerated only when absent. Persists via the `rtk` bind-mount.
#
# Note: RTK_TEE=0 and RTK_TELEMETRY_DISABLED=1 are also set image-wide and
# are the primary defense; these settings are belt-and-braces.

[tee]
enabled = false

[telemetry]
enabled = false
EOF
    fi

    if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
        if ! rtk init -g --auto-patch </dev/null >/dev/null 2>&1; then
            echo "  rtk: init -g failed (non-fatal)"
        fi
    fi
    if command -v codex >/dev/null 2>&1 && [ -d "$HOME/.codex" ]; then
        if ! rtk init -g --codex </dev/null >/dev/null 2>&1; then
            echo "  rtk: init -g --codex failed (non-fatal)"
        fi
    fi
fi

# Install the `cf` Cloudflare CLI Claude Code skill on every shell start.
# Idempotent — only writes when the file is absent (so a fresh
# ~/.toolbox/.claude bind-mount always re-materialises the skill, but a user
# edit on disk is preserved). Same belt-and-braces motivation as the rtk
# config.toml pre-seed above.
#
# The skill itself is hand-written (not generated from `cf agent-context`)
# because the universal guide + product-specific contexts are huge (~107
# products, MBs of markdown). The skill instead instructs Claude to invoke
# `cf agent-context <product>` on demand to fetch the focused guide for the
# product the user actually mentioned. Single small skill file, fresh
# product context per call, no version-tracking needed.
#
# Gated on `command -v cf` AND directory presence: tools.cf=false skips the
# install entirely so opted-out users don't see a skill they can't use.
if command -v cf >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    _cf_skill_file="$HOME/.claude/skills/cf/SKILL.md"
    if [ ! -f "$_cf_skill_file" ]; then
        mkdir -p "$(dirname "$_cf_skill_file")"
        cat > "$_cf_skill_file" <<'EOF'
---
name: cf
description: Cloudflare CLI (`cf`, Wrangler vNext preview). Invoke when the user asks about Cloudflare products — Workers, Pages, DNS, R2, KV, D1, AI, AI Gateway, Zero Trust, WAF, CDN, Stream, Images, Queues, Hyperdrive, Vectorize, Durable Objects, Tunnels, Access, Browser Rendering, Magic Transit, Radar, Registrar, etc. Exposes ~3000 API operations with JSON output by default.
---

# cf — Cloudflare CLI

## First step before crafting any command

Run `cf agent-context <product>` to load the universal best-practices guide plus the product-specific command index. Common product names: workers, pages, dns, r2, kv, d1, ai, ai-gateway, zero-trust, waf, stream, images, queues, hyperdrive, vectorize, durable-objects, zones, cache, page-rules, ssl, certificates, browser-rendering, magic-transit, magic-cloud-networking, radar, registrar, billing, alerting, audit-logs, accounts, iam.

Full list: `cf agent-context --list`.

## Authentication

- Preferred: `CLOUDFLARE_API_TOKEN` env var
- Alternative: `cf auth login` (OAuth tokens persist in `~/.cf/config.toml`; context defaults and completion marker live in `~/.config/cf/config.json`. Both bind-mounted from `~/.toolbox/cf/{auth,config}` so they survive `toolbox stop`.)
- Verify: `cf auth whoami`

## Output discipline

- JSON by default — pipe through `jq` for filtering
- `--fields id,name,status` to limit response size
- `--ndjson` for streaming list results
- `--dry-run` ALWAYS before create/update/delete

## Context resolution (highest priority first)

1. CLI flags (`--account-id`, `--zone`)
2. Env vars (`CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_ZONE_ID`)
3. Project config (`.cfrc` walked up from cwd)
4. User config (`~/.config/cf/config.json`)

Set defaults via `cf context set account-id <id>` to avoid repeating IDs.

## Schema introspection

- `cf schema --list` — list all API schemas
- `cf schema <product> <action>` — full request/response schema for a command

## Error handling

- Non-zero exit codes = errors; details on stderr
- 401 → check `cf auth whoami`
EOF
    fi
    unset _cf_skill_file
fi

# Install the `graphify` Claude Code skill on every shell start.
# Always runs `graphify install` when graphify + ~/.claude are present, so the
# SKILL.md tracks the currently-installed `graphifyy` package version on every
# bump. Different from the cf block above (which writes only when absent):
# graphify ships its skill via the upstream package, so the canonical content
# is whatever `graphify install` writes — re-running on every shell keeps the
# skill in sync with the package. Cost is ~50ms per shell.
#
# Side-effect: user edits to ~/.claude/skills/graphify/SKILL.md are
# overwritten on the next shell. Customisations belong in a wrapper skill or
# the upstream graphify repo, not in this auto-managed file.
#
# Gated on `command -v graphify` AND ~/.claude presence: tools.graphify=false
# skips entirely so opted-out users don't see a skill they can't use. Failure
# is non-fatal — logged and swallowed so a broken `graphify install` never
# blocks shell access.
if command -v graphify >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    graphify install >/dev/null 2>&1 || \
        echo "toolbox: graphify install failed (non-fatal — run \`graphify install\` manually to retry)"
fi

# Install the `playwright-cli` Claude Code skills on every shell start.
# Same always-run rationale as the graphify block above: the skills ship with
# the upstream `@playwright/cli` npm package, so re-running keeps them in
# sync on every PLAYWRIGHT_CLI_VERSION bump. `--skills claude` is the
# default; explicit here for clarity.
#
# Side-effect: user edits under ~/.claude/skills/playwright-cli/ (or wherever
# the installer writes) are overwritten on the next shell. Same trade-off as
# graphify — customisations belong in a wrapper skill.
#
# Gated on `command -v playwright-cli` AND ~/.claude presence:
# tools.playwright_cli=false skips entirely. Failure is non-fatal.
if command -v playwright-cli >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    playwright-cli install --skills claude >/dev/null 2>&1 || \
        echo "toolbox: playwright-cli install --skills failed (non-fatal — run \`playwright-cli install --skills\` manually to retry)"
fi

# User-defined startup hooks from ~/.toolbox/startup.d/ on the host.
# Each *.sh file runs sequentially before the shell starts. Failures are
# logged but never abort the entrypoint — one bad hook cannot block access.
if [ -d "$HOME/.toolbox-startup.d" ]; then
    shopt -s nullglob
    hooks=( "$HOME/.toolbox-startup.d"/*.sh )
    shopt -u nullglob
    if [ ${#hooks[@]} -gt 0 ]; then
        echo ""
        echo "Toolbox startup hooks:"
        for hook in "${hooks[@]}"; do
            [ -r "$hook" ] || continue
            echo "  $(basename "$hook"):"
            bash "$hook" || echo "  $(basename "$hook"): failed (exit $?)"
        done
    fi
fi

exec "$@"
