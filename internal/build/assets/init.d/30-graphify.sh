#!/usr/bin/env bash
set -euo pipefail

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
# Gated on `command -v graphify` AND `command -v claude` AND ~/.claude
# presence. The `command -v claude` gate matches rtk's pattern: the bind-
# mount auto-creates ~/.claude even with tools.claude=false, so a dir-only
# check would write a skill into a directory Claude Code never reads.
# Failure is non-fatal — logged and swallowed so a broken `graphify install`
# never blocks shell access.
command -v graphify >/dev/null 2>&1 || exit 0

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    graphify install >/dev/null 2>&1 || \
        echo "toolbox: graphify install failed (non-fatal — run \`graphify install\` manually to retry)"
fi
