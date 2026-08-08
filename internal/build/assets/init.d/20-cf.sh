#!/usr/bin/env bash
set -euo pipefail

# Seed the cf Claude skill on every shell start. Idempotent: writes only when
# the file is absent or carries an older version marker, so a fresh
# ~/.toolbox/.claude bind-mount re-materialises the skill and a corrected text
# reaches installs that already hold an older copy. Skill points Claude at
# `cf agent-context <product>` on demand instead of pre-baking ~107 products of
# MD into the image.
#
# Bump _cf_skill_version whenever the skill text below changes — the file is
# generated, so local edits to it are replaced on a bump; edit this script.
#
# Inner gate checks both `claude` AND ~/.claude exist because bind-mounts
# auto-create the dir even when tools.claude=false.
command -v cf >/dev/null 2>&1 || exit 0

_cf_skill_version=2

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    _cf_skill_file="$HOME/.claude/skills/cf/SKILL.md"
    _cf_skill_marker="<!-- toolbox-cf-skill: v${_cf_skill_version} -->"
    if ! grep -qF "$_cf_skill_marker" "$_cf_skill_file" 2>/dev/null; then
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
- Alternative: `cf auth login` (OAuth tokens persist in `~/.config/cloudflare/config/default.json`; context defaults and completion marker live in `~/.config/.cf/config.json`. Both bind-mounted from `~/.toolbox/cf/{auth,config}` so they survive `toolbox stop` and are shared by every toolbox.)
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
4. User config (`~/.config/.cf/config.json`)

Set defaults via `cf context set account-id <id>` to avoid repeating IDs.

## Schema introspection

- `cf schema --list` — list all API schemas
- `cf schema <product> <action>` — full request/response schema for a command

## Error handling

- Non-zero exit codes = errors; details on stderr
- 401 → check `cf auth whoami`
EOF
        printf '\n%s\n' "$_cf_skill_marker" >>"$_cf_skill_file"
    fi
fi
