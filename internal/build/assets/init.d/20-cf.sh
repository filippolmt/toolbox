#!/usr/bin/env bash
set -euo pipefail

# Install the `cf` Cloudflare CLI Claude Code skill on every shell start.
# Idempotent — only writes when the file is absent (so a fresh
# ~/.toolbox/.claude bind-mount always re-materialises the skill, but a user
# edit on disk is preserved).
#
# The skill itself is hand-written (not generated from `cf agent-context`)
# because the universal guide + product-specific contexts are huge (~107
# products, MBs of markdown). The skill instead instructs Claude to invoke
# `cf agent-context <product>` on demand to fetch the focused guide for the
# product the user actually mentioned. Single small skill file, fresh
# product context per call, no version-tracking needed.
#
# Gated on `command -v cf` AND `command -v claude` AND directory presence.
# The double-CLI gate matches the rtk pattern above: the bind-mount auto-
# creates ~/.claude even when tools.claude=false, so a dir-only check would
# write a skill into a directory Claude Code never reads.
command -v cf >/dev/null 2>&1 || exit 0

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
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
fi
