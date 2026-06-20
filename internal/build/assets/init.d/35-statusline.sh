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
[ -d "$HOME/.claude" ] || exit 0

_settings="$HOME/.claude/settings.json"
_statusline='/etc/toolbox/statusline-command.sh'
_claude_lock="$HOME/.toolbox-state/.claude-settings.lock"
mkdir -p "$(dirname "$_claude_lock")"

(
    flock 200
    _tmp=$(mktemp "${_settings}.XXXXXX") || exit 1
    # Seed from the existing settings, or {} if absent/corrupt, then force the
    # key. Only replace the live file when jq produced non-empty output, so a
    # parse failure never truncates a valid settings.json.
    if jq --arg cmd "bash '$_statusline'" \
            '.statusLine = {type: "command", command: $cmd}' \
            "$_settings" >"$_tmp" 2>/dev/null \
        || printf '{}' | jq --arg cmd "bash '$_statusline'" \
            '.statusLine = {type: "command", command: $cmd}' >"$_tmp" 2>/dev/null; then
        if [ -s "$_tmp" ]; then mv -f "$_tmp" "$_settings"; else rm -f "$_tmp"; fi
    else
        rm -f "$_tmp"
    fi
) 200>"$_claude_lock" || echo "  statusline: settings.json patch failed (non-fatal)"
