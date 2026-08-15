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

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    # Stamp is toolbox-owned and lives outside the workspace, keyed by
    # (workspace, tool); content is the version last installed from. $PWD is
    # hashed so an arbitrarily deep workspace path still yields a valid name.
    _cg_ver=$(codegraph --version 2>/dev/null | tr -d '\n' || true)
    _cg_stamp="$HOME/.toolbox-state/install-refresh/$(printf '%s' "$PWD" | sha256sum | cut -c1-16)-codegraph"
    _cg_stamped=""
    if [ -f "$_cg_stamp" ]; then
        read -r _cg_stamped < "$_cg_stamp" 2>/dev/null || true
    fi

    # No guard on an empty $_cg_ver: if the version probe ever breaks upstream,
    # an empty stamp still compares equal to an empty version, so a healthy
    # workspace stays quiet — while the artefact half keeps self-healing a
    # deleted install, which a `[ -n "$_cg_ver" ]` guard would silently disable.
    if [ "$_cg_stamped" != "$_cg_ver" ] || [ ! -f "$PWD/.mcp.json" ]; then
        if codegraph install --refresh >/dev/null 2>&1; then
            mkdir -p "$(dirname "$_cg_stamp")"
            printf '%s' "$_cg_ver" > "$_cg_stamp"
        else
            echo "toolbox: codegraph skill refresh failed (non-fatal — run \`codegraph install --refresh\` manually to retry)"
        fi
    fi
    unset _cg_ver _cg_stamp _cg_stamped
fi
