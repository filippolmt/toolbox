#!/usr/bin/env bash
set -euo pipefail

# Per-repo opt-in. The user runs `graphify claude install` once inside a repo
# to wire the `## graphify` section into that repo's local CLAUDE.md.
#
# On every shell, IF the current workspace already has a graphify-out/ dir,
# re-run it so the section stays in sync with the bundled graphify version
# after an image upgrade. Repos WITHOUT graphify-out/ are left untouched —
# no global skill install. (Replaces the previous always-on global
# `graphify install`, which registered the skill into ~/.claude/skills/ on
# every shell regardless of the repo.)
#
# Inner gate: `claude` binary AND ~/.claude exist (bind-mount auto-creates
# the dir even when tools.claude=false).
command -v graphify >/dev/null 2>&1 || exit 0
[ -d "$PWD/graphify-out" ] || exit 0

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    graphify claude install >/dev/null 2>&1 || \
        echo "toolbox: graphify skill refresh failed (non-fatal — run \`graphify claude install\` manually to retry)"
fi
