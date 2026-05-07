#!/usr/bin/env bash
set -euo pipefail

# Run `graphify install` on every shell to keep ~/.claude/skills/graphify/
# SKILL.md in sync with the upstream package — graphify owns its skill,
# re-installing is the documented refresh path. User edits are overwritten;
# customisations belong in a wrapper skill or upstream.
#
# Inner gate: `claude` binary AND ~/.claude exist (bind-mount auto-creates
# the dir even when tools.claude=false).
command -v graphify >/dev/null 2>&1 || exit 0

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    graphify install >/dev/null 2>&1 || \
        echo "toolbox: graphify install failed (non-fatal — run \`graphify install\` manually to retry)"
fi
