#!/usr/bin/env bash
set -euo pipefail

# Install the `playwright-cli` Claude Code skills on every shell start.
# Same always-run rationale as the graphify block above: the skills ship with
# the upstream `@playwright/cli` npm package, so re-running keeps them in
# sync on every PLAYWRIGHT_CLI_VERSION bump. `--skills claude` is the
# default; explicit here for clarity.
#
# `playwright-cli install` writes `.claude/skills/playwright-cli/` and
# `.playwright/cli.config.json` into the **current working directory** — the
# CLI exposes no flag for a global target. Running it from `/workspace`
# (the default CWD on `toolbox shell`) would pollute every repo. We `cd $HOME`
# first so the skill lands in `~/.claude/skills/playwright-cli/` (persisted
# via the `~/.toolbox/.claude` bind-mount) and the config in
# `~/.playwright/cli.config.json` — both global, both invisible to repos.
#
# Side-effect: user edits under `~/.claude/skills/playwright-cli/` are
# overwritten on the next shell. Same trade-off as graphify — customisations
# belong in a wrapper skill.
#
# Gated on `command -v playwright-cli` AND `command -v claude` AND
# ~/.claude presence (same double-CLI pattern as the cf/graphify blocks).
# Failure is non-fatal.
command -v playwright-cli >/dev/null 2>&1 || exit 0

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    (cd "$HOME" && playwright-cli install --skills claude) >/dev/null 2>&1 || \
        echo "toolbox: playwright-cli install --skills failed (non-fatal — run \`(cd ~ && playwright-cli install --skills claude)\` manually to retry)"
fi
