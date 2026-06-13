#!/usr/bin/env bash
set -euo pipefail

# Per-repo opt-in. The user runs `codegraph install --location=local` once
# inside a repo they want indexed; that writes .codegraph/ + per-project MCP
# config + a marker-fenced section into the repo's CLAUDE.md/AGENTS.md.
#
# On every shell, IF the current workspace already has a .codegraph/ dir,
# re-run the local install so the marker guidance + MCP config stay in sync
# with the bundled codegraph version after an image upgrade. Repos WITHOUT
# .codegraph/ are left untouched — no global registration, nothing written
# where the user did not opt in.
#
# Inner gate: `claude` binary AND ~/.claude exist (bind-mount auto-creates
# the dir even when tools.claude=false).
command -v codegraph >/dev/null 2>&1 || exit 0
[ -d "$PWD/.codegraph" ] || exit 0

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    codegraph install --target=claude --location=local --yes >/dev/null 2>&1 || \
        echo "toolbox: codegraph skill refresh failed (non-fatal — run \`codegraph install --target=claude --location=local\` manually to retry)"
fi
