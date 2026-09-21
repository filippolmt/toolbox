#!/usr/bin/env bash
set -euo pipefail

# Sync the bundled Chromium to the pinned playwright version. The Dockerfile
# bakes the playwright npm package + apt deps only — browser binaries live in
# the ~/.toolbox/playwright-cache bind (host-persisted, kept out of the image).
# Nothing else installs them, so a `playwright` Renovate bump leaves the cache
# on the old Chromium revision and the default headless launch breaks: it wants
# chromium_headless_shell-<rev>, which a stale cache never downloaded.
#
# A version sentinel makes this a no-op on every shell except the first after a
# bump (no per-shell download); on a version change it runs `playwright install
# chromium` (full + headless_shell, matching the pinned rev). Best-effort and
# non-fatal — an offline shell still starts, just without a fresh sync.
if command -v playwright >/dev/null 2>&1; then
    _pw_cache="${PLAYWRIGHT_BROWSERS_PATH:-$HOME/.cache/ms-playwright}"
    _pw_ver=$(node -e "console.log(require('/usr/local/lib/node_modules/playwright/package.json').version)" 2>/dev/null || echo "")
    _pw_sentinel="$_pw_cache/.toolbox-chromium-version"
    _pw_have=""
    if [ -f "$_pw_sentinel" ]; then
        read -r _pw_have < "$_pw_sentinel" 2>/dev/null || true
    fi
    if [ -n "$_pw_ver" ] && [ "$_pw_have" != "$_pw_ver" ]; then
        echo "  playwright: syncing Chromium for playwright ${_pw_ver} (one-time after a version bump)..."
        if playwright install chromium >/dev/null 2>&1; then
            mkdir -p "$_pw_cache"
            printf '%s' "$_pw_ver" > "$_pw_sentinel"
            echo "    Chromium synced"
        else
            echo "    playwright install chromium failed (non-fatal — run \`playwright install chromium\` manually to retry)"
        fi
    fi
    unset _pw_cache _pw_ver _pw_sentinel _pw_have
fi

# Per-repo opt-in (mirrors graphify/codegraph). The user runs
# `playwright-cli install --skills claude` once inside a repo they want browser
# automation in; with no `cd $HOME` wrapper the command initialises the
# workspace in CWD and writes the skill to `$PWD/.claude/skills/playwright-cli/`
# (plus a `.playwright/` workspace dir). Nothing is registered globally.
#
# Workspace Install Refresh (docs/adr/0001-workspace-install-refresh.md): the
# refresh re-runs only when the bundled playwright-cli version moved away from
# the stamp OR the skill's SKILL.md went missing. The skill dir is tracked, so
# an unconditional re-run would rewrite it on every image upgrade and hand the
# user a dirty tree. Repos WITHOUT the skill dir are left untouched — opening an
# un-opted-in repo never dirties it. (Replaces the always-on
# `(cd "$HOME" && playwright-cli install --skills claude)`, which registered the
# skill into ~/.claude/skills/ on every shell regardless of the repo; an
# existing global copy is left as-is, just no longer refreshed.)
#
# Inner gate: `claude` binary AND ~/.claude exist (bind-mount auto-creates the
# dir even when tools.claude=false).
command -v playwright-cli >/dev/null 2>&1 || exit 0
[ -d "$PWD/.claude/skills/playwright-cli" ] || exit 0

# The gate itself lives in one place, sourced by every member that re-runs a
# per-repo installer — stamp shape, the -n guard and the failure message are
# its business, not each member's.
# shellcheck source=bin/install-refresh-lib.sh
. /usr/local/lib/toolbox/install-refresh-lib.sh


# One version probe for both passes below: same bundled playwright-cli, two
# skill roots.
_pwc_ver=$(playwright-cli --version 2>/dev/null | tr -d '\n' || true)

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    toolbox_install_refresh playwright-cli "$PWD/.claude/skills/playwright-cli/SKILL.md" "$_pwc_ver" \
        playwright-cli install --skills claude || true
fi

# codex and pi read the cross-agent .agents/skills, which the claude install
# above never writes — one pass covers both readers, gated on either being
# present. Its own stamp and its own artefact: a shared stamp would leave the
# agents copy unwritten for good, because the claude pass already moved it to
# the bundled version.
#
# The repo-level opt-in stays the claude skill dir checked at the top — this
# adds agents to an already opted-in repo, and `pwcli-init` opts in both at
# once. No ~/.agents gate here: this copy is the repo's, not the home one.
if command -v codex >/dev/null 2>&1 || command -v pi >/dev/null 2>&1; then
    toolbox_install_refresh playwright-cli-agents "$PWD/.agents/skills/playwright-cli/SKILL.md" "$_pwc_ver" \
        playwright-cli install --skills agents || true
fi
unset _pwc_ver
