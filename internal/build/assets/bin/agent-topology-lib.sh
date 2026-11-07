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

# Codex's hooks.json has one init.d writer here (25-codex.sh), but it is one
# host mount shared with containers on earlier images, whose Atuin, Herdr,
# Worktrunk and RTK migration still write it; each read-modify-write takes this
# lock.
toolbox_with_codex_hooks_lock() {
    local lock="$HOME/.toolbox-state/.codex-hooks.lock"
    mkdir -p "$(dirname "$lock")"
    (
        flock 201
        "$@"
    ) 201>"$lock"
}

# Rewrites an agent's JSON file in place: `toolbox_rewrite_json FILE PROGRAM
# [jq args...]` runs the jq PROGRAM over FILE, or over {} when FILE is absent or
# empty. A file jq cannot parse is left alone and the call fails: replacing it
# would throw away whatever the user had in it. Empty output means nothing to
# change, and the file is not touched. Otherwise the result replaces FILE
# atomically, keeping its mode. Callers that share FILE hold its lock.
toolbox_rewrite_json() {
    local file="$1" program="$2" tmp
    shift 2
    mkdir -p "$(dirname "$file")" || return 1
    tmp=$(mktemp "$file.XXXXXX") || return 1
    if [ -s "$file" ]; then
        jq "$@" "$program" "$file" >"$tmp" 2>/dev/null || { rm -f "$tmp"; return 1; }
    else
        printf '{}' | jq "$@" "$program" >"$tmp" || { rm -f "$tmp"; return 1; }
    fi
    if [ ! -s "$tmp" ]; then
        rm -f "$tmp"
        return 0
    fi
    [ -e "$file" ] && chmod --reference="$file" "$tmp" 2>/dev/null
    mv -f "$tmp" "$file" || { rm -f "$tmp"; return 1; }
}
