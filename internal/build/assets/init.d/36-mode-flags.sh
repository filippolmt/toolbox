#!/usr/bin/env bash
set -euo pipefail

# Reconcile behavioral-mode flag files with enabledPlugins in settings.json.
# ponytail/caveman drop a ~/.claude/.<mode>-active flag (read by the managed
# statusline to draw the [PONYTAIL]/[CAVEMAN] badge) from their SessionStart
# hook — but when a plugin is *disabled*, that hook never runs, so a flag left
# over from a previous session goes stale and the badge shows for an inactive
# mode. This runs before `claude` starts: remove the flag for any mode whose
# plugin is not enabled. Enabled plugins are left alone — their hook rewrites
# the flag (with the live level) when `claude` starts, so both badges can show
# at once when both plugins are enabled.
command -v jq >/dev/null 2>&1 || exit 0
# Resolve the config dir like statusline-command.sh and the plugin hooks do.
_cfg="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
_settings="$_cfg/settings.json"
[ -s "$_settings" ] || exit 0

# <plugin-key>:<flag-file> pairs — the behavioral-mode plugins the statusline
# renders. Keep in sync with emit_mode_badge in statusline-command.sh
# (enforced by TestModePluginListsAgree).
for _pair in "ponytail@ponytail:.ponytail-active" "caveman@caveman:.caveman-active"; do
    _plugin="${_pair%%:*}"
    _flag="$_cfg/${_pair#*:}"
    # Default to "enabled" when the key is unreadable (jq failure on a corrupt
    # file): never delete a flag on uncertain state — only on a definite false.
    _enabled=$(jq -r --arg p "$_plugin" '.enabledPlugins[$p] // false' "$_settings" 2>/dev/null) || _enabled=true
    [ "$_enabled" = "false" ] && rm -f "$_flag"
done
exit 0
