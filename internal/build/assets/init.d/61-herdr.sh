#!/usr/bin/env bash
set -euo pipefail

# Two things, per agent, so an agent running inside a herdr pane can drive it
# and herdr can see what it is doing:
#
#   1. herdr's own agent skill. `herdr --skill` prints the version-matched
#      SKILL.md straight from the binary — no npx, no network, and nothing to
#      pin: upstream's documented `npx skills add herdrdev/herdr` fetches the
#      same file over the wire.
#   2. herdr's integration hook (`herdr integration install <agent>`), which
#      reports the agent's native session reference so herdr can restore the
#      conversation, and registers itself in the agent's own settings.
#
# Both rewritten on every container start rather than version-stamped (contrast
# the Workspace Install Refresh members, 30/31/40): the binary IS the authority
# for its own CLI and its own hook, so neither file may outlive the herdr it
# came from, and no target is tracked by a repo — all are host-shared toolbox
# mounts, not the workspace, so a rewrite can't hand the user a dirty tree. Both
# are idempotent, and the rewrite is how each follows a herdr version bump.
#
# Skill discovery paths diverge: Claude Code reads ~/.claude/skills, Codex reads
# ~/.agents/skills (same split as 60-glab.sh). The integration hooks diverge
# differently — claude's lands under ~/.claude, codex's under ~/.codex — so the
# two installs are gated separately, each on the binary AND the directory it
# writes to: the bind mount auto-creates the dir even when the tool is disabled.
# Roots come from CLAUDE_CONFIG_DIR / CODEX_HOME with the ~ fallback (the
# Dockerfile sets both; same idiom as 35-statusline.sh and 25-codex.sh) — herdr
# honours those vars too, so gating on a bare $HOME path would skip an install
# that would have landed, or probe a directory the agent never reads.
#
# Atomic per target: init.d scripts run backgrounded in parallel and each target
# is ONE host mount shared by every toolbox container, so a container starting
# mid-write must never see a partial SKILL.md.
#
# Non-fatal throughout — a missing skill or hook costs herdr control, not a
# shell. The two are independent: neither failing skips the other.
command -v herdr >/dev/null 2>&1 || exit 0

_herdr_skill=$(herdr --skill 2>/dev/null) || {
    echo "toolbox: herdr --skill failed (non-fatal — herdr agent skill not installed)"
    _herdr_skill=""
}

_herdr_install_skill() {
    local dir="$1/skills/herdr" tmp
    [ -n "$_herdr_skill" ] || return 0
    mkdir -p "$dir" || return 1
    tmp=$(mktemp "$dir/.SKILL.md.XXXXXX") || return 1
    if printf '%s\n' "$_herdr_skill" > "$tmp" && mv -f "$tmp" "$dir/SKILL.md"; then
        return 0
    fi
    rm -f "$tmp"
    return 1
}

_herdr_report_failure() {
    echo "toolbox: herdr skill install into $1/skills failed (non-fatal — retry: \`herdr --skill > $1/skills/herdr/SKILL.md\`)"
}

_herdr_install_integration() {
    herdr integration install "$1" >/dev/null 2>&1 || \
        echo "toolbox: herdr integration install $1 failed (non-fatal — retry: \`herdr integration install $1\`)"
}

_herdr_claude_dir="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
_herdr_codex_dir="${CODEX_HOME:-$HOME/.codex}"

if command -v claude >/dev/null 2>&1 && [ -d "$_herdr_claude_dir" ]; then
    _herdr_install_skill "$_herdr_claude_dir" || _herdr_report_failure "$_herdr_claude_dir"
    _herdr_install_integration claude
fi

if command -v codex >/dev/null 2>&1; then
    # Codex splits the two: the skill goes to the cross-agent ~/.agents/skills,
    # the integration hook to codex's own config dir.
    if [ -d "$HOME/.agents" ]; then
        _herdr_install_skill "$HOME/.agents" || _herdr_report_failure "$HOME/.agents"
    fi
    if [ -d "$_herdr_codex_dir" ]; then
        _herdr_install_integration codex
    fi
fi
