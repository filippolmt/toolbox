#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=bin/agent-topology-lib.sh
. /usr/local/lib/toolbox/agent-topology-lib.sh

# Worktrunk's Agent Wiring, on every container start:
#
#   1. its configuration skill, extracted at build time from the release that
#      ships this `wt`, so the skill never describes another version;
#   2. the activity-marker hooks behind `wt list`'s 🤖/💬 column, for every
#      supported agent.
#
# Upstream delivers (2) as agent plugins. The Claude Code one cannot be used
# here: its marketplace source is blocked by managed settings, and copying the
# plugin into place by hand would go around that policy. So the image registers
# the same marker commands itself, the way Herdr and Atuin wire theirs. The
# plugin's other two hooks stay out: the PermissionRequest auto-approval for
# entering worktrees and the routing of Claude's worktree isolation through wt
# both change how the agent behaves (ADR 0018 and its amendment).
#
# Rewritten on every container start rather than stamped, like 61-herdr.sh: the bundled
# binary is the authority, so nothing may outlive the wt it came from. The hook
# merge drops every earlier marker hook before adding the current ones, which
# is what makes a rewrite idempotent and a changed command replace, not stack.
#
# Non-fatal throughout, and each agent independent of the others.
command -v wt >/dev/null 2>&1 || exit 0

_wt_skill_src=/usr/local/share/worktrunk/skills/worktrunk
_wt_bin=/usr/local/bin/wt

_wt_claude=""
_wt_codex=""
_wt_pi=""
if toolbox_agent_available claude && toolbox_agent_persistent claude; then
    _wt_claude=1
fi
if toolbox_agent_available codex; then
    _wt_codex=1
fi
if toolbox_agent_available pi; then
    _wt_pi=1
fi
[ -n "${_wt_claude}${_wt_codex}${_wt_pi}" ] || exit 0

# Skill: replaced whole, so a reference file upstream dropped does not linger.
# A Claude skill root is one host mount shared by every toolbox container, so
# the swap runs under a per-root lock and is skipped when the copy is current.
_wt_install_skill() {
    local root="$2"
    [ -s "$_wt_skill_src/SKILL.md" ] || return 0
    if ! mkdir -p "$root" || ! (
        flock 9
        if [ -d "$root/worktrunk" ] && diff -r "$_wt_skill_src" "$root/worktrunk" >/dev/null 2>&1; then
            exit 0
        fi
        # `set -e` does not apply inside an `if !` condition, so every step
        # checks its own status: a half-removed old copy must stop the swap,
        # or `mv` would nest the new tree inside it.
        tmp=$(mktemp -d "$root/.worktrunk.XXXXXX") || exit 1
        if cp -R "$_wt_skill_src/." "$tmp/" && chmod 0755 "$tmp" \
            && rm -rf "$root/worktrunk" && mv "$tmp" "$root/worktrunk"; then
            exit 0
        fi
        rm -rf "$tmp"
        exit 1
    ) 9>"$root/.worktrunk-skill.lock"; then
        echo "toolbox: worktrunk skill install into $root failed (non-fatal)"
    fi
}

# Each current marker group takes the place of the first earlier one for its
# event, so the file keeps its order from one start to the next (Codex keys a
# hook's trust to its position). Every other earlier marker hook is dropped,
# with any group that held nothing else — including events a newer wt no
# longer hooks. Other tools' hooks are untouched.
# shellcheck disable=SC2016 # jq program, not shell
_wt_merge_jq='
def ours: ((.command? // "") | test("^/usr/local/bin/wt .*config state marker "));
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

# A file jq cannot parse is left alone: replacing it would throw away
# whatever the user had in it. Absent or empty starts from {}.
_wt_merge_hooks() {
    local file="$1" add="$2" tmp
    mkdir -p "$(dirname "$file")" || return 1
    tmp=$(mktemp "$file.XXXXXX") || return 1
    if [ -s "$file" ]; then
        jq --argjson add "$add" "$_wt_merge_jq" "$file" >"$tmp" 2>/dev/null || { rm -f "$tmp"; return 1; }
    else
        printf '{}' | jq --argjson add "$add" "$_wt_merge_jq" >"$tmp" || { rm -f "$tmp"; return 1; }
    fi
    [ -s "$tmp" ] || { rm -f "$tmp"; return 1; }
    [ -e "$file" ] && chmod --reference="$file" "$tmp" 2>/dev/null
    mv -f "$tmp" "$file"
}

# $1 is the command prefix (Claude passes the session's project dir with -C;
# Codex runs hooks in the session cwd). Output quieted: a session outside a git
# repository has no marker to set, and that is not an error worth showing.
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

# Events and matchers mirror upstream's Claude Code plugin, minus the two
# behaviour-changing hooks named at the top.
_wt_wire_claude() {
    local add
    add=$(_wt_marker_groups "$_wt_bin -C \"\$CLAUDE_PROJECT_DIR\"" '[
        {"event": "UserPromptSubmit", "marker": "working"},
        {"event": "Notification", "matcher": "", "marker": "waiting"},
        {"event": "PreToolUse", "matcher": "AskUserQuestion", "marker": "waiting"},
        {"event": "PermissionRequest", "matcher": "", "marker": "waiting"},
        {"event": "Stop", "marker": "waiting"},
        {"event": "SessionEnd", "matcher": "", "marker": "clear"}]') || return 1
    _wt_merge_hooks "$(toolbox_agent_home claude)/settings.json" "$add"
}

# Mirrors upstream's Codex plugin. Codex asks the user to trust a new or
# changed hook once, in Codex itself.
_wt_wire_codex() {
    local add
    add=$(_wt_marker_groups "$_wt_bin" '[
        {"event": "UserPromptSubmit", "marker": "working"},
        {"event": "PermissionRequest", "matcher": "", "marker": "waiting"},
        {"event": "Stop", "marker": "waiting"},
        {"event": "SessionEnd", "marker": "clear", "timeout": 3}]') || return 1
    _wt_merge_hooks "$(toolbox_agent_home codex)/hooks.json" "$add"
}

# pi: wt writes the extension itself. It skips an existing file, so it renders
# into a scratch agent dir and the result replaces the live one atomically —
# which is how a wt bump reaches pi.
_wt_wire_pi() {
    local ext scratch tmp rc=1
    ext="$(toolbox_agent_home pi)/agent/extensions"
    mkdir -p "$ext" || return 1
    scratch=$(mktemp -d) || return 1
    # The staging name is unique: ~/.pi is one host mount every container shares.
    if PI_CODING_AGENT_DIR="$scratch" wt config plugins pi install --yes >/dev/null 2>&1 \
        && [ -s "$scratch/extensions/worktrunk.ts" ] \
        && tmp=$(mktemp "$ext/.worktrunk.ts.XXXXXX"); then
        if cp "$scratch/extensions/worktrunk.ts" "$tmp" && chmod 0644 "$tmp" \
            && mv -f "$tmp" "$ext/worktrunk.ts"; then
            rc=0
        else
            rm -f "$tmp"
        fi
    fi
    rm -rf "$scratch"
    return "$rc"
}

toolbox_for_each_active_skill_root _wt_install_skill

if [ -n "$_wt_claude" ]; then
    toolbox_with_claude_settings_lock _wt_wire_claude || \
        echo "toolbox: worktrunk marker hooks for claude not wired (non-fatal — settings.json unreadable or not writable)"
fi

if [ -n "$_wt_codex" ] && toolbox_agent_persistent codex; then
    toolbox_with_codex_hooks_lock _wt_wire_codex || \
        echo "toolbox: worktrunk marker hooks for codex not wired (non-fatal — hooks.json unreadable or not writable)"
fi

# Gate on the ~/.pi bind mount, not ~/.pi/agent, which pi creates on its own
# first run whether or not its state is mounted (same reasoning as 61-herdr.sh).
if [ -n "$_wt_pi" ] && toolbox_agent_persistent pi; then
    _wt_wire_pi || echo "toolbox: worktrunk pi extension not installed (non-fatal — retry: \`wt config plugins pi install\`)"
fi
