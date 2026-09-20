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
# Skill discovery splits two ways, not three: Claude Code reads ~/.claude/skills,
# while Codex AND pi both read the cross-agent ~/.agents/skills (same split as
# 60-glab.sh). The integration hooks diverge per agent — claude's lands under
# the Claude config dir, codex's under codex's own, pi's under
# ~/.pi/agent/extensions — so the three are handled separately. The claude and
# codex roots come from CLAUDE_CONFIG_DIR / CODEX_HOME with the ~ fallback (the
# Dockerfile sets both; same idiom as 35-statusline.sh and 25-codex.sh) — herdr
# honours those vars too, so gating on a bare $HOME path would skip an install
# that would have landed, or probe a directory the agent never reads. pi has no
# such var baked, so the hook half gates on the bind mount itself, ~/.pi — see
# the block at the foot of this file.
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
# Only the claude install takes that lock. codex's hook and pi's are files of
# their own — pi's is ~/.pi/agent/extensions/herdr-agent-state.ts, disjoint from
# the rtk and atuin extensions 10-rtk.sh and 65-atuin.sh drop in that same
# directory — so there is no shared writer to serialise against.
#
# Non-fatal throughout, and the installs are independent: one failing never
# skips another. A missing skill or hook costs herdr control, not a shell.
command -v herdr >/dev/null 2>&1 || exit 0

_herdr_claude_dir="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
_herdr_codex_dir="${CODEX_HOME:-$HOME/.codex}"
_herdr_claude=""
_herdr_codex=""
_herdr_pi=""
if command -v claude >/dev/null 2>&1 && [ -d "$_herdr_claude_dir" ]; then
    _herdr_claude=1
fi
if command -v codex >/dev/null 2>&1; then
    _herdr_codex=1
fi
if command -v pi >/dev/null 2>&1; then
    _herdr_pi=1
fi
# No agent present: nothing to install, and no reason to run herdr at all.
[ -n "${_herdr_claude}${_herdr_codex}${_herdr_pi}" ] || exit 0

# Skill root, not config root: Codex and pi read the cross-agent ~/.agents/skills
# while their hooks live under CODEX_HOME and ~/.pi/agent respectively.
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
    # One pass for both readers of ~/.agents/skills. Created, not gated on:
    # see the ~/.agents note above.
    if [ -n "$_herdr_codex" ] || [ -n "$_herdr_pi" ]; then
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

# pi: writes ~/.pi/agent/extensions/herdr-agent-state.ts, its own file, no lock.
# The second gate is the ~/.pi bind mount and NOT ~/.pi/agent, which pi creates
# on its own first run whether the state is mounted or not — gating there would
# install a hook that the next `toolbox stop` throws away. Note this condition
# guards the hook only: the skill above lives in container-local ~/.agents and a
# pi shell reads it whether or not pi state is mounted.
#
# ~/.pi/agent is then created, not gated on — the same reasoning as ~/.agents,
# one layer down. herdr refuses a ~/.pi that pi has never populated ("pi
# extension directory not found"), and the other init.d members that write there
# build the tree on their way in; init.d runs them backgrounded in parallel, so
# herdr would otherwise lose that race on a fresh mount and report a failure for
# a mount that is fine.
if [ -n "$_herdr_pi" ] && [ -d "$HOME/.pi" ]; then
    mkdir -p "$HOME/.pi/agent"
    _herdr_install_integration pi
fi
