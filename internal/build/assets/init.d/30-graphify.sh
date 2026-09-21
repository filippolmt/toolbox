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

# The gate itself lives in one place, sourced by every member that re-runs a
# per-repo installer — stamp shape, the -n guard and the failure message are
# its business, not each member's.
# shellcheck source=bin/install-refresh-lib.sh
. /usr/local/lib/toolbox/install-refresh-lib.sh


# Snapshot retention. Every graphify run drops a dated directory (graph.json +
# GRAPH_REPORT.md, ~2.5 MB) beside the live graph and never removes one, so an
# actively indexed repo accumulates indefinitely — upstream exposes no knob for
# it (`graphify --purge` deletes the whole tree, live artefacts included).
# Nothing reads a snapshot back: the live graph is graphify-out/graph.json, and
# the whole tree is gitignored, regenerable, per-developer scratch. Keep the ten
# most recent by mtime, not by name — the `_2`, `_3` … suffixes graphify appends
# for same-day runs do not sort lexically past `_10`. Deliberately outside the
# Workspace Install Refresh gate below: housekeeping is not an install concern,
# and running it every shell is both cheaper and more predictable than tying it
# to a version bump. Never fatal: the script runs under `set -e` + `pipefail`,
# and an unreadable snapshot dir must not stop the refresh below from running.
find "$PWD/graphify-out" -maxdepth 1 -type d -name '20[0-9][0-9]-[0-9][0-9]-[0-9][0-9]*' -printf '%T@ %p\n' 2>/dev/null \
    | sort -n | head -n -10 | cut -d' ' -f2- | while IFS= read -r _gfy_old; do rm -rf "$_gfy_old"; done || true

# One version probe for every pass below: they install the same bundled
# graphify, they just write it where a different agent reads.
_gfy_ver=$(graphify --version 2>/dev/null | tr -d '\n' || true)

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    if toolbox_install_refresh graphify "$PWD/.claude/skills/graphify/SKILL.md" "$_gfy_ver" \
        graphify install --project --platform claude; then

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
        #
        # Dropping the previous run's entry is the other half of that narrowing,
        # not an extra. `graphify install` re-installs by removing what it wrote
        # last time and appending it afresh, and it recognises its own work by
        # (matcher is a known WIDE literal) AND (the entry mentions "graphify")
        # — install.py:1768. The rename breaks only the first half, so upstream
        # stops seeing its own hooks and appends a second pair on every reopened
        # gate. We therefore finish upstream's filter for the two literals we are
        # the ones to introduce: before renaming, drop the graphify-owned entries
        # already sitting at the narrowed matcher — those are last run's, while
        # the pair just appended is still wide, so the two are never confusable.
        #
        # Ownership is checked only where an entry is deleted, never on the
        # rename: a hand-written Grep hook that says nothing about graphify is
        # left alone, which a positional "keep the last one" rule could not
        # promise. The `any(... and gfy)` guard means a failed install (nothing
        # appended) drops nothing, so a workspace is never left hookless.
        _gfy_settings="$PWD/.claude/settings.json"
        if command -v jq >/dev/null 2>&1 && [ -s "$_gfy_settings" ]; then
            _gfy_tmp=$(mktemp "${_gfy_settings}.XXXXXX") || _gfy_tmp=""
            if [ -n "$_gfy_tmp" ]; then
                if jq 'def gfy: tostring | contains("graphify");
                       def narrow($wide; $new):
                         (if any(.hooks.PreToolUse[]?; .matcher == $wide and gfy)
                          then .hooks.PreToolUse |= map(select((.matcher == $new and gfy) | not))
                          else . end)
                         | (.hooks.PreToolUse[]? | select(.matcher == $wide) | .matcher) = $new;
                       narrow("Bash|Grep"; "Grep") | narrow("Read|Glob"; "Glob")' \
                        "$_gfy_settings" >"$_gfy_tmp" 2>/dev/null && [ -s "$_gfy_tmp" ]; then
                    mv -f "$_gfy_tmp" "$_gfy_settings"
                else
                    rm -f "$_gfy_tmp"
                fi
            fi
        fi
        unset _gfy_settings _gfy_tmp
    fi
fi
# pi reads .pi/agent/skills, which the claude install above never writes, so the
# refresh needs a second pass per agent — same gate, its own stamp, its own
# artefact. A shared stamp would be the silent bug: the claude pass would leave
# it at the bundled version and pi's copy would never be written at all.
#
# The agent gate is the ~/.pi bind mount and NOT ~/.pi/agent, which pi creates on
# its own first run whether the state is mounted or not — installing behind the
# wrong gate writes a skill the next `toolbox stop` throws away. The repo-level
# opt-in is still graphify-out/, checked at the top: this block adds an agent to
# an already opted-in repo, never a repo.
#
# No jq/settings pass here: the hooks and the CLAUDE.md section are claude's,
# and graphify writes neither for pi.
if command -v pi >/dev/null 2>&1 && [ -d "$HOME/.pi" ]; then
    toolbox_install_refresh graphify-pi "$PWD/.pi/agent/skills/graphify/SKILL.md" "$_gfy_ver" \
        graphify install --project --platform pi || true
fi
unset _gfy_ver
# --- end Workspace Install Refresh gate ---

# Git commit hook only makes sense inside a git repo; skip non-git workspaces.
# Ask git rather than testing for a `.git/` dir: in a worktree or submodule
# `.git` is a gitdir-pointer file, not a directory, so a `-d` test would wrongly
# skip the install and let the graph go stale on commit.
if git -C "$PWD" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    graphify hook install >/dev/null 2>&1 || \
        echo "toolbox: graphify git-hook install failed (non-fatal — run \`graphify hook install\` manually to retry)"
fi
