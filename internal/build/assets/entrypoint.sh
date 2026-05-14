#!/usr/bin/env bash
set -euo pipefail

# Inject a passwd/group entry for the runtime UID/GID when missing.
# The container runs with --user <host-uid>:<host-gid>, which rarely matches
# the baked "toolbox" user (uid 1000). Tools that call getpwuid() — notably
# ssh — abort with "No user exists for uid NNN" otherwise, breaking git over
# ssh://. /etc/passwd and /etc/group are chmod 0666 in the Dockerfile so this
# append works without root.
_uid=$(id -u)
_gid=$(id -g)
if ! getent passwd "${_uid}" >/dev/null 2>&1; then
    echo "toolbox:x:${_uid}:${_gid}:toolbox:/home/toolbox:/bin/bash" >> /etc/passwd
fi
if ! getent group "${_gid}" >/dev/null 2>&1; then
    echo "toolbox:x:${_gid}:" >> /etc/group
fi
unset _uid _gid

# Init Sequence (CONTEXT.md). Stderr → ~/.toolbox-state/init/<name>.log;
# on failure, tail-5 inline. The `if !` form neutralises the outer `set -e`
# so a failed init never aborts boot.
#
# Scripts touch disjoint config trees (~/.config/rtk, ~/.claude/skills/<name>,
# ~/.codex, ~/.claude/plugins/cache) and gate on their own binaries — safe to
# run in parallel. Stdout per script is buffered to a temp file and replayed
# in lexical filename order after `wait`, preserving the visible "Building
# Claude Code MCP plugins:" output from 50-mcp-plugins.
#
# Log path is `.toolbox-state` (not `.toolbox/state`) because the `state`
# bind-mount resolves Source `~/.toolbox/state` → Target `~/.toolbox-state`
# inside the container.
INIT_D="/usr/local/lib/toolbox/init.d"
TOOLBOX_INIT_LOG_DIR="$HOME/.toolbox-state/init"
# Cred-probe scripts (init.d/0[2-8]-*-creds.sh + 60-glab.sh) emit two-space-
# indented child lines under this banner — restoring it preserves D-08 (see
# .planning/phases/10-init-sequence/10-CONTEXT.md) which kept the
# "Toolbox credential check:" group header from the original entrypoint.
# Header is unconditional because the cred probes always run (they self-gate
# on `command -v` per CLI); printing only when something configured would
# require a pre-scan and add a forking cost for cosmetic gain.
echo "Toolbox credential check:"
if [ -d "$INIT_D" ]; then
    mkdir -p "$TOOLBOX_INIT_LOG_DIR"
    _init_tmp=$(mktemp -d)
    for f in "$INIT_D"/*.sh; do
        [ -r "$f" ] || continue
        _name=$(basename "$f")
        _log="${TOOLBOX_INIT_LOG_DIR}/${_name%.sh}.log"
        _out="${_init_tmp}/${_name}"
        (
            if ! bash "$f" >"$_out" 2>"$_log"; then
                echo "  ${_name}: failed (log: $_log)" >> "$_out"
                [ -s "$_log" ] && tail -n 5 "$_log" | sed 's/^/      /' >> "$_out"
            else
                rm -f "$_log"
            fi
        ) &
    done
    wait
    for f in "$_init_tmp"/*; do
        [ -s "$f" ] && cat "$f"
    done
    rm -rf "$_init_tmp"
    unset _name _log f _init_tmp _out
fi
unset INIT_D TOOLBOX_INIT_LOG_DIR

# User-defined startup hooks from ~/.toolbox/startup.d/ on the host.
# Each *.sh file runs sequentially before the shell starts. Failures are
# logged but never abort the entrypoint — one bad hook cannot block access.
if [ -d "$HOME/.toolbox-startup.d" ]; then
    shopt -s nullglob
    hooks=( "$HOME/.toolbox-startup.d"/*.sh )
    shopt -u nullglob
    if [ ${#hooks[@]} -gt 0 ]; then
        echo ""
        echo "Toolbox startup hooks:"
        for hook in "${hooks[@]}"; do
            [ -r "$hook" ] || continue
            echo "  $(basename "$hook"):"
            bash "$hook" || echo "  $(basename "$hook"): failed (exit $?)"
        done
    fi
fi

exec "$@"
