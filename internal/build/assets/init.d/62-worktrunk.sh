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
# Rewritten on every start rather than stamped, like 61-herdr.sh: the bundled
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
        tmp=$(mktemp -d "$root/.worktrunk.XXXXXX") || exit 1
        if ! cp -R "$_wt_skill_src/." "$tmp/" || ! chmod 0755 "$tmp"; then
            rm -rf "$tmp"
            exit 1
        fi
        rm -rf "$root/worktrunk"
        mv "$tmp" "$root/worktrunk"
    ) 9>"$root/.worktrunk-skill.lock"; then
        echo "toolbox: worktrunk skill install into $root failed (non-fatal)"
    fi
}

# Drop every marker hook an earlier start registered, and any group that held
# only those, then append the current set. Other tools' hooks are untouched.
# shellcheck disable=SC2016 # jq program, not shell
_wt_merge_jq='
def ours: ((.command? // "") | test("^/usr/local/bin/wt .*config state marker "));
.hooks = ((.hooks // {}) | map_values(map(
    if any(.hooks[]?; ours)
    then (.hooks |= map(select(ours | not)) | select((.hooks | length) > 0))
    else . end)))
| reduce $add[] as $e (.; .hooks[$e.event] += [$e.group])'

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
    mv -f "$tmp" "$file"
}

# $1 is the command prefix (Claude passes the session's project dir with -C;
# Codex runs hooks in the session cwd). Output quieted: a session outside a git
# repository has no marker to set, and that is not an error worth showing.
_wt_marker_cmd() {
    printf '%s config state marker %s >/dev/null 2>&1 || true' "$1" "$2"
}

_wt_wire_claude() {
    local pre="$_wt_bin -C \"\$CLAUDE_PROJECT_DIR\"" add
    add=$(jq -n \
        --arg working "$(_wt_marker_cmd "$pre" 'set 🤖')" \
        --arg waiting "$(_wt_marker_cmd "$pre" 'set 💬')" \
        --arg clear "$(_wt_marker_cmd "$pre" clear)" '
        def hook($c): {type: "command", command: $c};
        [ {event: "UserPromptSubmit", group: {hooks: [hook($working)]}},
          {event: "Notification", group: {matcher: "", hooks: [hook($waiting)]}},
          {event: "PreToolUse", group: {matcher: "AskUserQuestion", hooks: [hook($waiting)]}},
          {event: "PermissionRequest", group: {matcher: "", hooks: [hook($waiting)]}},
          {event: "Stop", group: {hooks: [hook($waiting)]}},
          {event: "SessionEnd", group: {matcher: "", hooks: [hook($clear) + {timeout: 3}]}} ]') || return 1
    _wt_merge_hooks "$(toolbox_agent_home claude)/settings.json" "$add"
}

# Codex asks the user to trust a new or changed hook once, in Codex itself.
_wt_wire_codex() {
    local add
    add=$(jq -n \
        --arg working "$(_wt_marker_cmd "$_wt_bin" 'set 🤖')" \
        --arg waiting "$(_wt_marker_cmd "$_wt_bin" 'set 💬')" \
        --arg clear "$(_wt_marker_cmd "$_wt_bin" clear)" '
        def hook($c): {type: "command", command: $c};
        [ {event: "UserPromptSubmit", group: {hooks: [hook($working)]}},
          {event: "PermissionRequest", group: {matcher: "", hooks: [hook($waiting)]}},
          {event: "Stop", group: {hooks: [hook($waiting)]}},
          {event: "SessionEnd", group: {hooks: [hook($clear) + {timeout: 3}]}} ]') || return 1
    _wt_merge_hooks "$(toolbox_agent_home codex)/hooks.json" "$add"
}

# pi: wt writes the extension itself. It skips an existing file, so it renders
# into a scratch agent dir and the result replaces the live one atomically —
# which is how a wt bump reaches pi.
_wt_wire_pi() {
    local ext scratch rc=0
    ext="$(toolbox_agent_home pi)/agent/extensions"
    mkdir -p "$ext" || return 1
    scratch=$(mktemp -d) || return 1
    if PI_CODING_AGENT_DIR="$scratch" wt config plugins pi install --yes >/dev/null 2>&1 \
        && [ -s "$scratch/extensions/worktrunk.ts" ] \
        && cp "$scratch/extensions/worktrunk.ts" "$ext/.worktrunk.ts.tmp"; then
        mv -f "$ext/.worktrunk.ts.tmp" "$ext/worktrunk.ts" || rc=1
    else
        rc=1
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
