#!/usr/bin/env bash

# Shared facts about the coding agents bundled in the canonical image.
toolbox_agent_names() {
    printf '%s\n' claude codex pi
}

toolbox_agent_home() {
    case "$1" in
        claude) printf '%s\n' "${CLAUDE_CONFIG_DIR:-$HOME/.claude}" ;;
        codex) printf '%s\n' "${CODEX_HOME:-$HOME/.codex}" ;;
        pi) printf '%s\n' "$HOME/.pi" ;;
        *) return 2 ;;
    esac
}

toolbox_agent_skill_root() {
    case "$1" in
        claude) printf '%s/skills\n' "$(toolbox_agent_home claude)" ;;
        codex|pi) printf '%s/.agents/skills\n' "$HOME" ;;
        *) return 2 ;;
    esac
}

toolbox_agent_available() {
    command -v "$1" >/dev/null 2>&1
}

toolbox_agent_persistent() {
    local home
    home=$(toolbox_agent_home "$1") || return
    [ -d "$home" ]
}

toolbox_for_each_active_skill_root() {
    local callback="$1"
    if toolbox_agent_available claude && toolbox_agent_persistent claude; then
        "$callback" claude "$(toolbox_agent_skill_root claude)"
    fi
    if toolbox_agent_available codex || toolbox_agent_available pi; then
        "$callback" cross-agent "$(toolbox_agent_skill_root codex)"
    fi
}

toolbox_with_claude_settings_lock() {
    local lock="$HOME/.toolbox-state/.claude-settings.lock"
    mkdir -p "$(dirname "$lock")"
    (
        flock 200
        "$@"
    ) 200>"$lock"
}
