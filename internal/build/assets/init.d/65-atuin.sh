#!/usr/bin/env bash
set -euo pipefail

# Seed atuin config + one-shot history import on every shell start.
# Idempotent: sentinel comment marks the config block (file absent → create,
# file present without sentinel → append, sentinel present → no-op). User
# edits outside the sentinel block survive. The history import runs once,
# gated by a marker file under ~/.toolbox-state so it doesn't re-import on
# every container start (which would duplicate every entry).
#
# Inner gate: `atuin` binary present. The Dockerfile sets ATUIN_CONFIG_DIR
# to a subdir of the ~/.toolbox/atuin bind-mount, so writes here survive
# `toolbox stop`.
command -v atuin >/dev/null 2>&1 || exit 0

# Fail loud if the Dockerfile contract is broken: ATUIN_CONFIG_DIR MUST be
# baked into the image so config + DB land under the ~/.toolbox/atuin
# bind-mount. The default `~/.config/atuin/` is NOT mounted, so a silent
# fallback would invisibly wipe the user's atuin state on every
# `toolbox stop` — better to abort the init script than degrade silently.
: "${ATUIN_CONFIG_DIR:?ATUIN_CONFIG_DIR must be set by the Dockerfile (consumed here + bind-mount in internal/mountplan/defaults.go)}"

# -- Config seed -------------------------------------------------------------
_atuin_config_dir="$ATUIN_CONFIG_DIR"
_atuin_config="$_atuin_config_dir/config.toml"
_atuin_sentinel='# toolbox:atuin:defaults'
_atuin_block='# toolbox:atuin:defaults
# auto_sync defaults to true; flip off because we ship no atuin.sh sync
# account by default and the unauthenticated retry-spams the API on
# every prompt. Users who run `atuin login` can comment this out.
auto_sync = false
# Stops the "new release available" startup ping. Renovate handles version
# bumps via the Dockerfile ARG; this nag is redundant.
update_check = false
# atuin >= 17 default for new users is true, but the existing-user
# heuristic downgrades to false; pin explicitly to avoid the double-enter
# annoyance (press Enter once on a selected row → executes; Tab → edits).
enter_accept = true
# Toolbox shells frequently nest inside a 24-row IDE terminal panel; 20
# leaves enough context above the prompt while still showing useful
# history rows. inline_height defaults to 40 (full-screen overlay).
inline_height = 20
# Enables the "workspace" filter mode (auto-activated inside git repos).
# Combined with the existing global/host/session/directory cycle on
# Ctrl-R, makes scoping history to "this repo" a single key away.
workspaces = true
# atuin ships secrets_filter = true by default, which already matches
# AWS / GitHub PAT / Slack / Stripe / cloud-env-var / Netlify / npm /
# Pulumi tokens BEFORE recording. history_filter below catches the only
# overlap-free pattern worth seeding — anything literally starting with
# "secret". Users add more per-environment via this list.
history_filter = [
    "^secret",
]'

mkdir -p "$_atuin_config_dir"
if [ ! -f "$_atuin_config" ]; then
    printf '%s\n' "$_atuin_block" > "$_atuin_config"
elif ! grep -qF "$_atuin_sentinel" "$_atuin_config"; then
    printf '\n%s\n' "$_atuin_block" >> "$_atuin_config"
fi

# -- One-shot history import -------------------------------------------------
# `atuin import auto` reads $SHELL and imports the matching ~/.bash_history /
# ~/.zsh_history. Running it on every container start would duplicate every
# entry, so a marker file in the state volume tracks whether the import has
# already happened for this user. State volume survives `toolbox stop`, so
# the import really only fires once per machine.
_atuin_marker="$HOME/.toolbox-state/atuin-imported"
if [ ! -f "$_atuin_marker" ]; then
    if atuin import auto >/dev/null 2>&1; then
        mkdir -p "$(dirname "$_atuin_marker")"
        : > "$_atuin_marker"
    else
        echo "  atuin: import auto failed (non-fatal — retry: \`atuin import auto && touch $_atuin_marker\`)"
    fi
fi

# -- AI agent hooks ----------------------------------------------------------
# Capture Bash tool use from Claude Code + Codex into the atuin DB, tagged
# by author (`claude-code` / `codex`). atuin's interactive search hides
# agent-authored entries by default (filter `$all-user`); use
# `atuin search --author claude-code` or `--author '$all-agent'` to inspect.
#
# `atuin hook install` is idempotent upstream, but every invocation still
# opens + parses the agent's config file (and takes the claude-settings
# flock against init.d/10-rtk.sh). Gate per agent via a marker file keyed
# on the atuin binary's mtime+size — when atuin upgrades the hook stub may
# change, so we re-run on binary churn but skip on every other boot.
_atuin_bin=$(command -v atuin)
_atuin_key=$(stat -c '%Y-%s' "$_atuin_bin" 2>/dev/null || echo nokey)
_atuin_hooks_dir="$HOME/.toolbox-state/atuin-hooks"
mkdir -p "$_atuin_hooks_dir"

# Claude Code: writes to ~/.claude/settings.json — flock-guarded against
# rtk's concurrent patch in init.d/10-rtk.sh.
_claude_marker="$_atuin_hooks_dir/claude-${_atuin_key}"
if [ ! -f "$_claude_marker" ] && command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    _claude_lock="$HOME/.toolbox-state/.claude-settings.lock"
    if (
        flock 200
        atuin hook install claude-code >/dev/null 2>&1
    ) 200>"$_claude_lock"; then
        : > "$_claude_marker"
    else
        echo "  atuin: hook install claude-code failed (non-fatal)"
    fi
fi

# Codex: writes to ~/.codex/hooks.json (disjoint from init.d/25-codex.sh's
# config.toml edits, no lock needed).
_codex_marker="$_atuin_hooks_dir/codex-${_atuin_key}"
if [ ! -f "$_codex_marker" ] && command -v codex >/dev/null 2>&1 && [ -d "$HOME/.codex" ]; then
    if atuin hook install codex >/dev/null 2>&1; then
        : > "$_codex_marker"
    else
        echo "  atuin: hook install codex failed (non-fatal)"
    fi
fi
