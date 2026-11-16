# shellcheck shell=bash
# Worktrunk's activity markers, the hooks behind `wt list`'s 🤖/💬 column: the
# commands, Codex's event list, and the merge that registers them in an
# agent's hooks file. Sourced by init.d/62-worktrunk.sh (Claude Code) and
# init.d/25-codex.sh, and by the Dockerfile's gen-codex-config stage, which
# builds Codex's markers into /etc/codex/hooks.json — one source, so the
# Codex copy cannot drift from Claude's. Needs jq and Agent Topology
# (bin/agent-topology-lib.sh), sourced first.

# Matches every marker command any image has registered, whatever its prefix
# (Claude's carries -C) or marker; 25-codex.sh drops earlier images' Codex
# copies by it.
_wt_marker_re='^/usr/local/bin/wt .*config state marker '

# Codex's marker events, mirroring upstream's Codex plugin. They ship in the
# image's /etc/codex/hooks.json, built by the Dockerfile's gen-codex-config
# stage, where Codex runs them with no per-user trust step.
_wt_codex_events='[
    {"event": "UserPromptSubmit", "marker": "working"},
    {"event": "PermissionRequest", "matcher": "", "marker": "waiting"},
    {"event": "Stop", "marker": "waiting"},
    {"event": "SessionEnd", "marker": "clear", "timeout": 3}]'

# Registers the marker groups $2 (from _wt_marker_groups) in the hooks file
# $1, through Agent Topology's toolbox_merge_hook_groups.
_wt_merge_hooks() {
    toolbox_merge_hook_groups "$1" "$2" "$_wt_marker_re"
}

# $1 is the command prefix (Claude passes the session's project dir with -C;
# Codex runs hooks in the session cwd). Output quieted: a session outside a
# git repository has no marker to set, and that is not an error worth showing.
_wt_marker_cmd() {
    printf '%s config state marker %s >/dev/null 2>&1 || true' "$1" "$2"
}

# $2 lists the agent's events as {event, matcher?, marker, timeout?}, marker
# being working, waiting or clear; prints the group list the merge appends.
_wt_marker_groups() {
    jq -n --argjson spec "$2" \
        --arg working "$(_wt_marker_cmd "$1" 'set 🤖')" \
        --arg waiting "$(_wt_marker_cmd "$1" 'set 💬')" \
        --arg clear "$(_wt_marker_cmd "$1" clear)" '
        {working: $working, waiting: $waiting, clear: $clear} as $cmd
        | $spec | map({event,
            group: ((if has("matcher") then {matcher} else {} end)
                + {hooks: [{type: "command", command: $cmd[.marker]}
                    + (if has("timeout") then {timeout} else {} end)]})})'
}
