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

echo "Toolbox credential check:"

# Each provider's auth check is an independent network/file-I/O probe — run
# them in parallel and reassemble output in a deterministic order via the
# numbered temp-file prefix. Cuts cold-entrypoint latency from
# sum(t_i) ≈ 1–3s down to max(t_i) ≈ 300–800ms when az + oci are configured.
# Debian-slim's coreutils `mktemp -d` cannot fail under reasonable conditions
# (no disk-full edge cases at boot in a fresh container); let the script
# crash loudly if it ever does rather than silently skipping every probe.
_cred_check() {
    local name="$1" bin="$2" probe="$3"
    command -v "$bin" >/dev/null 2>&1 || return 0
    if eval "$probe" >/dev/null 2>&1; then
        echo "  ${name}: configured"
    else
        echo "  ${name}: not configured"
    fi
}

_cred_tmp=$(mktemp -d)
_cred_check gh     gh     "gh auth status"                                                                > "$_cred_tmp/10-gh"     &
_cred_check glab   glab   "glab auth status"                                                              > "$_cred_tmp/20-glab"   &
_cred_check gcloud gcloud "gcloud auth list --filter=status:ACTIVE --format='value(account)' | grep -q ." > "$_cred_tmp/30-gcloud" &
_cred_check az     az     "az account show"                                                               > "$_cred_tmp/40-az"     &

# oci needs a fast-path config-file check before the live API call:
# `oci iam region list` is a real network round-trip (~200-500ms when
# configured, multi-second timeout otherwise). </dev/null is load-bearing —
# without it, oci's "Do you want to create a new config file? [Y/n]" prompt
# blocks the entrypoint on the container TTY and never reaches the startup
# hooks. Doesn't fit the _cred_check shape (extra config-file gate), kept
# explicit.
{
    if command -v oci >/dev/null 2>&1; then
        if [ ! -f "$HOME/.oci/config" ]; then
            echo "  oci: not configured"
        elif oci iam region list --output table </dev/null >/dev/null 2>&1; then
            echo "  oci: configured"
        else
            echo "  oci: not configured"
        fi
    fi
} > "$_cred_tmp/50-oci" &

wait
for _f in "$_cred_tmp"/*; do
    [ -s "$_f" ] && cat "$_f"
done
rm -rf "$_cred_tmp"
unset _cred_tmp _f
unset -f _cred_check

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
