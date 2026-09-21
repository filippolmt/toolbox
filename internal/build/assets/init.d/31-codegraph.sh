#!/usr/bin/env bash
set -euo pipefail

# Per-repo opt-in. The user runs `codegraph install --location=local` once
# inside a repo they want indexed; that writes .codegraph/ + per-project MCP
# config + a marker-fenced section into the repo's CLAUDE.md/AGENTS.md.
#
# Workspace Install Refresh (docs/adr/0001-workspace-install-refresh.md): the
# refresh re-runs only when the bundled codegraph version moved away from the
# stamp OR .mcp.json went missing. Both the fenced section and .mcp.json are
# tracked, so an unconditional re-run would rewrite them on every image upgrade
# and hand the user a dirty tree. `install --refresh` is upstream's own "rewrite
# what previous installs configured" mode — it never adds a new agent, which is
# exactly the scope a refresh wants. Repos WITHOUT .codegraph/ are left
# untouched — no global registration, nothing written where the user did not
# opt in.
#
# Inner gate: `claude` binary AND ~/.claude exist (bind-mount auto-creates
# the dir even when tools.claude=false).
command -v codegraph >/dev/null 2>&1 || exit 0
[ -d "$PWD/.codegraph" ] || exit 0

# The gate itself lives in one place, sourced by every member that re-runs a
# per-repo installer — stamp shape, the -n guard and the failure message are
# its business, not each member's.
# shellcheck source=bin/install-refresh-lib.sh
. /usr/local/lib/toolbox/install-refresh-lib.sh


if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    _cg_ver=$(codegraph --version 2>/dev/null | tr -d '\n' || true)
    # The artefact half is .mcp.json, not a skill dir: codegraph writes an MCP
    # config, and it is what must come back if the repo loses it.
    toolbox_install_refresh codegraph "$PWD/.mcp.json" "$_cg_ver" \
        codegraph install --refresh || true
    unset _cg_ver
fi
