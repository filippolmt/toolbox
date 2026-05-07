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
if [ ! -f "$HOME/.config/rtk/config.toml" ]; then
    mkdir -p "$HOME/.config/rtk"
    cat > "$HOME/.config/rtk/config.toml" <<'EOF'
# Regenerated only when absent. Persists via the `rtk` bind-mount.
# RTK_TEE=0 and RTK_TELEMETRY_DISABLED=1 in the image env are the primary
# defense; these settings are belt-and-braces.

[tee]
enabled = false

[telemetry]
enabled = false
EOF
fi

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    if ! rtk init -g --auto-patch </dev/null >/dev/null 2>&1; then
        echo "  rtk: init -g failed (non-fatal)"
    fi
fi
if command -v codex >/dev/null 2>&1 && [ -d "$HOME/.codex" ]; then
    if ! rtk init -g --codex </dev/null >/dev/null 2>&1; then
        echo "  rtk: init -g --codex failed (non-fatal)"
    fi
fi
