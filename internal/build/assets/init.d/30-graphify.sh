#!/usr/bin/env bash
set -euo pipefail

# Per-repo opt-in. The user runs `graphify install --project --platform claude`
# once inside a repo to install the project-scoped `/graphify` skill into the
# repo's `.claude/skills/graphify/`, wire the `## graphify` section into that
# repo's local CLAUDE.md, and register the `.claude/settings.json` hooks — all
# version-controllable with the repo, nothing global.
#
# Workspace Install Refresh (docs/adr/0001-workspace-install-refresh.md): the
# refresh re-runs only when the bundled graphify version moved away from the
# stamp OR an artefact it should have written went missing. Everything graphify
# installs here is tracked, so an unconditional re-run would rewrite CLAUDE.md,
# .claude/settings.json and .claude/skills/graphify/ on every image upgrade and
# hand the user a dirty tree. Repos WITHOUT graphify-out/ are left untouched.
#
# Two distinct installs run here: `graphify install` (skill + PreToolUse hook +
# CLAUDE.md section) and `graphify hook install` (git post-commit/post-checkout
# hooks that rebuild graph.json on commit). The latter lives in .git/hooks/ —
# never committed, so absent from a fresh clone — and touches nothing tracked,
# which is why it stays OUTSIDE the gate and runs every shell.
#
# Inner gate: `claude` binary AND ~/.claude exist (bind-mount auto-creates
# the dir even when tools.claude=false).
command -v graphify >/dev/null 2>&1 || exit 0
[ -d "$PWD/graphify-out" ] || exit 0

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    # Stamp is toolbox-owned and lives outside the workspace, keyed by
    # (workspace, tool); content is the version last installed from. $PWD is
    # hashed so an arbitrarily deep workspace path still yields a valid name.
    _gfy_ver=$(graphify --version 2>/dev/null | tr -d '\n' || true)
    _gfy_stamp="$HOME/.toolbox-state/install-refresh/$(printf '%s' "$PWD" | sha256sum | cut -c1-16)-graphify"
    _gfy_stamped=""
    if [ -f "$_gfy_stamp" ]; then
        read -r _gfy_stamped < "$_gfy_stamp" 2>/dev/null || true
    fi

    # The -n guard scopes to the version half ONLY. If the version probe ever
    # breaks upstream, an unreadable version must not read as "differs from the
    # stamp" — that would reopen the gate on every shell and hand back exactly
    # the churn this gate removes. Guarding the whole condition instead would be
    # the opposite bug: a deleted install would stop self-healing, silently.
    if { [ -n "$_gfy_ver" ] && [ "$_gfy_stamped" != "$_gfy_ver" ]; } || [ ! -f "$PWD/.claude/skills/graphify/SKILL.md" ]; then
        if graphify install --project --platform claude >/dev/null 2>&1; then
            mkdir -p "$(dirname "$_gfy_stamp")"
            printf '%s' "$_gfy_ver" > "$_gfy_stamp"
        else
            echo "toolbox: graphify skill refresh failed (non-fatal — run \`graphify install --project --platform claude\` manually to retry)"
        fi

        # graphify install writes a backup beside the settings it patched; it is
        # never read back, so drop it instead of leaving an untracked file in
        # every opted-in workspace.
        rm -f "$PWD/.claude/settings.json.graphify-bak"

        # Narrow the PreToolUse matchers graphify install writes: its hook-guard
        # should fire on blind search only, never on Read or Bash. graphify
        # re-installs its hook payload on every upgrade, so a manual edit would
        # not survive — hence normalising here instead. Only the known upstream
        # values are rewritten, so a hand-tuned matcher is left alone. Atomic
        # mktemp+mv guarded on non-empty output, so a jq failure never truncates
        # a valid settings.json (same discipline as 35-statusline.sh; no lock —
        # this is the workspace settings file, which no other init.d script
        # touches).
        _gfy_settings="$PWD/.claude/settings.json"
        if command -v jq >/dev/null 2>&1 && [ -s "$_gfy_settings" ]; then
            _gfy_tmp=$(mktemp "${_gfy_settings}.XXXXXX") || _gfy_tmp=""
            if [ -n "$_gfy_tmp" ]; then
                if jq '(.hooks.PreToolUse[]? | select(.matcher == "Bash|Grep") | .matcher) = "Grep"
                     | (.hooks.PreToolUse[]? | select(.matcher == "Read|Glob") | .matcher) = "Glob"' \
                        "$_gfy_settings" >"$_gfy_tmp" 2>/dev/null && [ -s "$_gfy_tmp" ]; then
                    mv -f "$_gfy_tmp" "$_gfy_settings"
                else
                    rm -f "$_gfy_tmp"
                fi
            fi
        fi
        unset _gfy_settings _gfy_tmp
    fi
    unset _gfy_ver _gfy_stamp _gfy_stamped
fi
# --- end Workspace Install Refresh gate ---

# Git commit hook only makes sense inside a git repo; skip non-git workspaces.
# Ask git rather than testing for a `.git/` dir: in a worktree or submodule
# `.git` is a gitdir-pointer file, not a directory, so a `-d` test would wrongly
# skip the install and let the graph go stale on commit.
if git -C "$PWD" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    graphify hook install >/dev/null 2>&1 || \
        echo "toolbox: graphify git-hook install failed (non-fatal — run \`graphify hook install\` manually to retry)"
fi
