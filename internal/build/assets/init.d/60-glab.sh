#!/usr/bin/env bash
set -euo pipefail

# `glab skills install` is EXPERIMENTAL upstream — non-fatal on failure.
# Two passes: Claude Code reads only ~/.claude/skills; Codex reads only
# ~/.agents/skills (cross-agent USER scope per agentskills.io).
command -v glab >/dev/null 2>&1 || exit 0

_install() {
    local label="$1"; shift
    glab skills install "$@" --force >/dev/null 2>&1 || \
        echo "toolbox: glab skills install ($label) failed (non-fatal — retry: \`glab skills install $* --force\`)"
}

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    _install claude --path "$HOME/.claude/skills"
fi

if command -v codex >/dev/null 2>&1; then
    _install codex --global
fi
