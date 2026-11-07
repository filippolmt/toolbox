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

# Each current marker group takes the place of the first earlier one for its
# event, so the file keeps its order from one start to the next. Every other
# earlier marker hook is dropped, with any group that held nothing else —
# including events a newer wt no longer hooks. Other tools' hooks are
# untouched.
# shellcheck disable=SC2016 # jq program, not shell
_wt_merge_jq='
def ours: ((.command? // "") | test($marker_re));
def mine: any(.hooks[]?; ours);
def strip: if mine then (.hooks |= map(select(ours | not)) | select((.hooks | length) > 0)) else . end;
def place($g):
  (first(range(length) as $k | select(.[$k] | mine) | $k) // null) as $i
  | if $i == null then map(strip) + [$g]
    else (.[:$i] | map(strip)) + [.[$i] | strip] + [$g] + (.[$i + 1:] | map(strip)) end;
($add | map(.event)) as $events
| .hooks = ((.hooks // {}) | with_entries(
    if (.key as $k | $events | index($k)) then . else .value |= map(strip) end))
| reduce $add[] as $e (.; .hooks[$e.event] = ((.hooks[$e.event] // []) | place($e.group)))'

# Registers the marker groups $2 (from _wt_marker_groups) in the hooks file
# $1, through Agent Topology's toolbox_rewrite_json.
_wt_merge_hooks() {
    toolbox_rewrite_json "$1" "$_wt_merge_jq" --argjson add "$2" --arg marker_re "$_wt_marker_re"
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
