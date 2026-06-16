#!/usr/bin/env bash
set -euo pipefail

# Per-repo opt-in. The user runs `graphify install --project --platform claude`
# once inside a repo to install the project-scoped `/graphify` skill into the
# repo's `.claude/skills/graphify/`, wire the `## graphify` section into that
# repo's local CLAUDE.md, and register the `.claude/settings.json` hooks — all
# version-controllable with the repo, nothing global.
#
# On every shell, IF the current workspace already has a graphify-out/ dir,
# re-run it so the skill, section, and hooks stay in sync with the bundled
# graphify version after an image upgrade. Repos WITHOUT graphify-out/ are left
# untouched — nothing global is ever written. (Replaces the previous always-on
# global `graphify install`, which registered the skill into ~/.claude/skills/
# on every shell regardless of the repo.)
#
# Inner gate: `claude` binary AND ~/.claude exist (bind-mount auto-creates
# the dir even when tools.claude=false).
command -v graphify >/dev/null 2>&1 || exit 0
[ -d "$PWD/graphify-out" ] || exit 0

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    graphify install --project --platform claude >/dev/null 2>&1 || \
        echo "toolbox: graphify skill refresh failed (non-fatal — run \`graphify install --project --platform claude\` manually to retry)"
fi
