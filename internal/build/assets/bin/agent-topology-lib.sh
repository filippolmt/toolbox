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

# Global skills the image bakes at build, each extracted from the release that
# ships its tool (Dockerfile fetch stages), so a skill never describes another
# version and installing one needs no network.
toolbox_baked_skills=/usr/local/share/toolbox/skills

# toolbox_sync_skill NAME copies the baked skill NAME into every active skill
# root, only where the copy there differs: a current copy is never rewritten, a
# missing one self-heals (~/.agents is container-local and starts empty on every
# recreate) and a hand edit is replaced. Nothing baked, nothing to do. A root
# that fails is reported and the others still run; the return status is 1 when
# any root failed, so a caller under `set -e` that must go on adds `|| true`.
toolbox_sync_skill() {
    [ -s "$toolbox_baked_skills/$1/SKILL.md" ] || return 0
    # Read and set by the callback through bash's dynamic scoping.
    local _toolbox_skill_name="$1" _toolbox_skill_failed=0
    toolbox_for_each_active_skill_root _toolbox_sync_skill_into
    return "$_toolbox_skill_failed"
}

# Replaced whole, so a file upstream dropped does not linger. A Claude skill
# root is one host mount shared by every toolbox container, so the swap runs
# under a lock — kept in the state mount, every container shares it, and not in
# the skill root, which an agent scans. The staging copy must sit in the root
# (a rename does not cross mounts), so one a killed sync left there is swept
# under the same lock, where no other sync can be mid-swap.
_toolbox_sync_skill_into() {
    local root="$2" name="$_toolbox_skill_name"
    local src="$toolbox_baked_skills/$name" lock="$HOME/.toolbox-state/.skill-$name.lock"
    if ! mkdir -p "$root" "${lock%/*}" || ! (
        flock 9
        rm -rf "$root/.$name".?????? "$root/.$name".??????.old
        if [ -d "$root/$name" ] && diff -r "$src" "$root/$name" >/dev/null 2>&1; then
            exit 0
        fi
        # `set -e` does not apply inside an `if !` condition, so every step
        # checks its own status: a half-moved old copy must stop the swap, or
        # `mv` would nest the new tree inside it. Two renames, so a reader on
        # another container never finds the skill half-written and misses it
        # only for that instant.
        tmp=$(mktemp -d "$root/.$name.XXXXXX") || exit 1
        if cp -R "$src/." "$tmp/" && chmod 0755 "$tmp" \
            && { [ ! -e "$root/$name" ] || mv "$root/$name" "$tmp.old"; } \
            && mv "$tmp" "$root/$name"; then
            rm -rf "$tmp.old"
            exit 0
        fi
        rm -rf "$tmp"
        exit 1
    ) 9>"$lock"; then
        echo "toolbox: $name skill install into $root failed (non-fatal)"
        _toolbox_skill_failed=1
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
