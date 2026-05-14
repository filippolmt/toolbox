#!/usr/bin/env bash
set -euo pipefail

# Re-register rtk hooks on every shell start so a settings reset or fresh
# bind-mount can't leave the user unwired. `rtk init -g` patches Claude's
# ~/.claude/settings.json; `rtk init -g --codex` writes ~/.codex/AGENTS.md
# + RTK.md (no settings.json to patch on the Codex side).
#
# --auto-patch + </dev/null avoid a TTY-prompt deadlock on first run
# (entrypoint has no terminal). Inner gates check both the binary AND the
# config dir because bind-mounts auto-create the dirs even when a tool is
# opted out, so a dir-only check would inject hooks into configs no CLI
# reads.
command -v rtk >/dev/null 2>&1 || exit 0

# RTK_TEE=0 + RTK_TELEMETRY_DISABLED=1 in the Dockerfile are the primary
# defense — they survive `rtk telemetry enable/disable` rewriting the TOML.
# The TOML seed is belt-and-braces so `rtk telemetry status` reports a
# consistent state and users who unset the env vars still get safe defaults.
#
# Idempotent: sentinel comment marks the block. File absent → create. File
# present without sentinel → append. Sentinel present → no-op.
_rtk_config="$HOME/.config/rtk/config.toml"
_rtk_sentinel='# toolbox:rtk:telemetry-off'
_rtk_block='# toolbox:rtk:telemetry-off
[tee]
enabled = false

[telemetry]
enabled = false'

mkdir -p "$HOME/.config/rtk"
if [ ! -f "$_rtk_config" ]; then
    printf '%s\n' "$_rtk_block" > "$_rtk_config"
elif ! grep -qF "$_rtk_sentinel" "$_rtk_config"; then
    printf '\n%s\n' "$_rtk_block" >> "$_rtk_config"
fi

# Both `rtk init -g --auto-patch` (here) and `atuin hook install claude-code`
# (init.d/65-atuin.sh) rewrite ~/.claude/settings.json. Since entrypoint runs
# init.d scripts in parallel, concurrent writes would clobber each other.
# Serialise via flock on a shared lock file under the state mount. The lock
# is held for the full rtk init invocation; atuin acquires the same lock
# from its script. flock comes from util-linux (base Debian, always present).
_claude_lock="$HOME/.toolbox-state/.claude-settings.lock"
mkdir -p "$(dirname "$_claude_lock")"
if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    (
        flock 200
        rtk init -g --auto-patch </dev/null >/dev/null 2>&1
    ) 200>"$_claude_lock" || echo "  rtk: init -g failed (non-fatal)"
fi
if command -v codex >/dev/null 2>&1 && [ -d "$HOME/.codex" ]; then
    if ! rtk init -g --codex </dev/null >/dev/null 2>&1; then
        echo "  rtk: init -g --codex failed (non-fatal)"
    fi
fi
