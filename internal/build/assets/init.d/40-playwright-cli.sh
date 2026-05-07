#!/usr/bin/env bash
set -euo pipefail

# Re-install playwright-cli skills on every shell so ~/.claude/skills/
# playwright-cli/ tracks PLAYWRIGHT_CLI_VERSION. User edits are overwritten;
# customisations belong in a wrapper skill.
#
# `playwright-cli install` writes to CWD (no global-target flag). The
# `cd "$HOME"` wrapper is load-bearing: without it the skill would land in
# /workspace/.claude/skills/playwright-cli/ and pollute every repo.
command -v playwright-cli >/dev/null 2>&1 || exit 0

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    (cd "$HOME" && playwright-cli install --skills claude) >/dev/null 2>&1 || \
        echo "toolbox: playwright-cli install --skills failed (non-fatal — run \`(cd ~ && playwright-cli install --skills claude)\` manually to retry)"
fi
