#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=bin/agent-topology-lib.sh
. /usr/local/lib/toolbox/agent-topology-lib.sh
# shellcheck source=bin/worktrunk-markers-lib.sh
. /usr/local/lib/toolbox/worktrunk-markers-lib.sh

# Worktrunk's Agent Wiring, on every container start:
#
#   1. its configuration skill, extracted at build time from the release that
#      ships this `wt`, so the skill never describes another version;
#   2. the activity-marker hooks behind `wt list`'s 🤖/💬 column, for Claude
#      Code and pi. Codex gets the same markers from the image's system
#      layer (/etc/codex/hooks.json, built from bin/worktrunk-markers-lib.sh),
#      which needs no per-user trust.
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

# Gate on the ~/.pi bind mount, not ~/.pi/agent, which pi creates on its own
# first run whether or not its state is mounted (same reasoning as 61-herdr.sh).
if [ -n "$_wt_pi" ] && toolbox_agent_persistent pi; then
    _wt_wire_pi || echo "toolbox: worktrunk pi extension not installed (non-fatal — retry: \`wt config plugins pi install\`)"
fi
