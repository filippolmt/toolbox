#!/usr/bin/env bash
set -euo pipefail

# Two things, per agent, so an agent running inside a herdr pane can drive herdr
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
# came from, and no target is tracked by a repo — none is the workspace — so a
# rewrite can't hand the user a dirty tree. Both are idempotent, and the rewrite
# is how each follows a herdr version bump.
#
# Skill discovery paths diverge: Claude Code reads ~/.claude/skills, Codex reads
# ~/.agents/skills (same split as 60-glab.sh). The integration hooks diverge
# differently — claude's lands under the Claude config dir, codex's under
# codex's own — so the two agents are handled separately. Roots come from
# CLAUDE_CONFIG_DIR / CODEX_HOME with the ~ fallback (the Dockerfile sets both;
# same idiom as 35-statusline.sh and 25-codex.sh) — herdr honours those vars
# too, so gating on a bare $HOME path would skip an install that would have
# landed, or probe a directory the agent never reads.
#
# ~/.agents is the exception: unlike ~/.claude it is NOT a bind mount, it is
# container-local, so it is created here rather than gated on. Gating would race
# 60-glab.sh, the only other script that creates it, and init.d runs backgrounded
# in parallel — the Codex skill would land or not depending on which script won.
#
# Skill writes are atomic (mktemp + mv): a target under ~/.claude is ONE host
# mount shared by every toolbox container, so a container starting mid-write must
# never see a partial SKILL.md. The claude integration install takes
# .claude-settings.lock, the same lock 10-rtk.sh / 35-statusline.sh / 65-atuin.sh
# hold: it registers its hook in settings.json, making this the fourth
# concurrent writer of that one file.
#
# Non-fatal throughout, and the two installs are independent: neither failing
# skips the other. A missing skill or hook costs herdr control, not a shell.
command -v herdr >/dev/null 2>&1 || exit 0

_herdr_claude_dir="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
_herdr_codex_dir="${CODEX_HOME:-$HOME/.codex}"
_herdr_claude=""
_herdr_codex=""
if command -v claude >/dev/null 2>&1 && [ -d "$_herdr_claude_dir" ]; then
    _herdr_claude=1
fi
if command -v codex >/dev/null 2>&1; then
    _herdr_codex=1
fi
# Neither agent present: nothing to install, and no reason to run herdr at all.
[ -n "${_herdr_claude}${_herdr_codex}" ] || exit 0

# Skill root, not config root: Codex reads the cross-agent ~/.agents/skills
# while its hook lives under CODEX_HOME.
_herdr_install_skill() {
    local root="$1" dir="$1/skills/herdr" tmp
    if ! mkdir -p "$dir" \
        || ! tmp=$(mktemp "$dir/.SKILL.md.XXXXXX") \
        || ! printf '%s\n' "$_herdr_skill" > "$tmp" \
        || ! mv -f "$tmp" "$dir/SKILL.md"; then
        rm -f "${tmp:-}"
        echo "toolbox: herdr skill install into $root/skills failed (non-fatal — retry: \`herdr --skill > $root/skills/herdr/SKILL.md\`)"
    fi
}

_herdr_install_integration() {
    herdr integration install "$1" >/dev/null 2>&1 || \
        echo "toolbox: herdr integration install $1 failed (non-fatal — retry: \`herdr integration install $1\`)"
}

if _herdr_skill=$(herdr --skill 2>/dev/null); then
    if [ -n "$_herdr_claude" ]; then
        _herdr_install_skill "$_herdr_claude_dir"
    fi
    if [ -n "$_herdr_codex" ]; then
        # Created, not gated on: see the ~/.agents note above.
        _herdr_install_skill "$HOME/.agents"
    fi
else
    echo "toolbox: herdr --skill failed (non-fatal — herdr agent skill not installed)"
fi

if [ -n "$_herdr_claude" ]; then
    _herdr_lock="$HOME/.toolbox-state/.claude-settings.lock"
    mkdir -p "$(dirname "$_herdr_lock")"
    (
        flock 200
        _herdr_install_integration claude
    ) 200>"$_herdr_lock"
fi

if [ -n "$_herdr_codex" ] && [ -d "$_herdr_codex_dir" ]; then
    _herdr_install_integration codex
fi
