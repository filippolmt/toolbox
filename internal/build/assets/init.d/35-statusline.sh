#!/usr/bin/env bash
set -euo pipefail

# Force the managed Claude Code statusline on every shell start. The script is
# baked read-only at /etc/toolbox/statusline-command.sh (image-owned policy);
# this re-points settings.json `statusLine` at it every boot so a local edit or
# a fresh bind-mount can't drift it. Change the statusline via a repo PR, not in
# the container.
#
# Only the .statusLine key is rewritten — everything else in settings.json
# (permissions, hooks, enabledPlugins, …) is preserved. Shares the
# .claude-settings.lock with 10-rtk.sh / 65-atuin.sh because entrypoint runs
# init.d scripts in parallel and all three rewrite the same file. flock +
# atomic mktemp+mv avoid clobbering a concurrent writer's result.
# Opt-out: `managed_statusline: false` in .toolbox.yaml → host injects
# TOOLBOX_MANAGED_STATUSLINE=0; leave the user's own statusLine untouched.
[ "${TOOLBOX_MANAGED_STATUSLINE:-1}" = "0" ] && exit 0
command -v jq >/dev/null 2>&1 || exit 0
command -v claude >/dev/null 2>&1 || exit 0

# Resolve the config dir the same way statusline-command.sh and the plugin
# hooks do, so all of them agree on which settings.json to read/write.
_cfg="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
[ -d "$_cfg" ] || exit 0

_settings="$_cfg/settings.json"
_statusline='/etc/toolbox/statusline-command.sh'
_claude_lock="$HOME/.toolbox-state/.claude-settings.lock"
mkdir -p "$(dirname "$_claude_lock")"

(
    flock 200
    _tmp=$(mktemp "${_settings}.XXXXXX") || exit 1
    # Seed from the existing settings, or {} if absent/empty/corrupt, then force
    # the key. The `-s` guard routes a 0-byte file to the {} fallback (jq exits
    # 0 with no output on empty input, which would otherwise leave _tmp empty and
    # silently skip the patch). Only replace the live file when jq produced
    # non-empty output, so a parse failure never truncates a valid settings.json.
    # refreshInterval: event-driven ticks go quiet while the session is idle, so
    # the duration and rate-limit reset clocks would freeze. hideVimModeIndicator:
    # statusline-command.sh renders vim.mode itself — without this the mode shows
    # twice (our segment + the built-in `-- INSERT --` line).
    _patch='.statusLine = {type: "command", command: $cmd, refreshInterval: 30, hideVimModeIndicator: true}'
    if [ -s "$_settings" ] && jq --arg cmd "bash '$_statusline'" \
            "$_patch" \
            "$_settings" >"$_tmp" 2>/dev/null \
        || printf '{}' | jq --arg cmd "bash '$_statusline'" \
            "$_patch" >"$_tmp" 2>/dev/null; then
        if [ -s "$_tmp" ]; then mv -f "$_tmp" "$_settings"; else rm -f "$_tmp"; fi
    else
        rm -f "$_tmp"
    fi
) 200>"$_claude_lock" || echo "  statusline: settings.json patch failed (non-fatal)"
