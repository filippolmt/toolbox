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
