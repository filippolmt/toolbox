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

# Repo-local SDD (Spec-Driven Development) bootstrap. Opt-in per skill via
# `sdd.<key>: true` in the workspace's .toolbox.yaml. sessionplan emits one
# env var per field (TOOLBOX_SDD_<KEY>_{PKG,VERSION,BIN,STEPS,MARKER}, plus
# the optional pair {MANIFESTS,EXTRAS} for manifest-managed skills) on top
# of TOOLBOX_SDD_ENABLED + TOOLBOX_SDD_WORKSPACE_HASH. Failures log inline
# and never abort the entrypoint.
if [ -n "${TOOLBOX_SDD_ENABLED:-}" ] && [ -n "${TOOLBOX_SDD_WORKSPACE_HASH:-}" ]; then
    _sdd_state_dir="$HOME/.toolbox-state"
    _sdd_printed_banner=0
    _sdd_banner() {
        if [ "$_sdd_printed_banner" = "0" ]; then
            echo ""
            echo "Toolbox sdd bootstrap:"
            _sdd_printed_banner=1
        fi
    }

    # _sdd_regen_gitignore_fence is defined in
    # /usr/local/lib/toolbox/sdd-helpers.sh — kept in a separate file so
    # the smoke-test can source it standalone for unit coverage.
    if [ -f /usr/local/lib/toolbox/sdd-helpers.sh ]; then
        # shellcheck disable=SC1091
        . /usr/local/lib/toolbox/sdd-helpers.sh
    fi

    IFS=',' read -ra _sdd_keys <<< "$TOOLBOX_SDD_ENABLED"
    for _sdd_key in "${_sdd_keys[@]}"; do
        [ -z "$_sdd_key" ] && continue
        _sdd_upper="$(printf '%s' "$_sdd_key" | tr '[:lower:]' '[:upper:]')"
        _sdd_pkg_var="TOOLBOX_SDD_${_sdd_upper}_PKG"
        _sdd_ver_var="TOOLBOX_SDD_${_sdd_upper}_VERSION"
        _sdd_bin_var="TOOLBOX_SDD_${_sdd_upper}_BIN"
        _sdd_steps_var="TOOLBOX_SDD_${_sdd_upper}_STEPS"
        _sdd_marker_var="TOOLBOX_SDD_${_sdd_upper}_MARKER"
        _sdd_manifests_var="TOOLBOX_SDD_${_sdd_upper}_MANIFESTS"
        _sdd_extras_var="TOOLBOX_SDD_${_sdd_upper}_EXTRAS"
        _sdd_pkg="${!_sdd_pkg_var:-}"
        _sdd_ver="${!_sdd_ver_var:-}"
        _sdd_bin="${!_sdd_bin_var:-}"
        _sdd_steps_raw="${!_sdd_steps_var:-}"
        _sdd_marker="${!_sdd_marker_var:-}"
        _sdd_manifests="${!_sdd_manifests_var:-}"
        _sdd_extras="${!_sdd_extras_var:-}"
        if [ -z "$_sdd_pkg" ] || [ -z "$_sdd_ver" ] || [ -z "$_sdd_bin" ] || [ -z "$_sdd_steps_raw" ]; then
            continue
        fi
        if [ -n "$_sdd_marker" ] && [ ! -e "./${_sdd_marker}" ]; then
            _sdd_banner
            echo "  ${_sdd_key}: skipped — no '${_sdd_marker}' in workspace."
            echo "    Run '${_sdd_bin}' init manually once, commit, then re-shell."
            continue
        fi
        _sdd_sentinel="$_sdd_state_dir/sdd.${_sdd_key}.${TOOLBOX_SDD_WORKSPACE_HASH}.version"
        _sdd_cur=""
        read -r _sdd_cur < "$_sdd_sentinel" 2>/dev/null || true
        _sdd_bootstrap_ran=0
        _sdd_failed=0
        if [ "$_sdd_cur" != "$_sdd_ver" ]; then
            _sdd_bootstrap_ran=1
            _sdd_banner
            echo "  ${_sdd_key}: installing ${_sdd_pkg}@${_sdd_ver} (repo-local)..."
            mkdir -p "$_sdd_state_dir"
            _sdd_log="$_sdd_state_dir/sdd.${_sdd_key}.log"
            : > "$_sdd_log"
            if ! npm install -g --silent "${_sdd_pkg}@${_sdd_ver}" >>"$_sdd_log" 2>&1; then
                _sdd_failed=1
            fi
            if [ "$_sdd_failed" = "0" ]; then
                # InstallSteps spec: steps separated by ';', args inside a
                # step split on whitespace — splitting IS the contract.
                IFS=';' read -ra _sdd_steps <<< "$_sdd_steps_raw"
                for _sdd_step in "${_sdd_steps[@]}"; do
                    # shellcheck disable=SC2086
                    if ! "$_sdd_bin" $_sdd_step >>"$_sdd_log" 2>&1; then
                        _sdd_failed=1
                        break
                    fi
                done
                unset _sdd_steps _sdd_step
            fi
            if [ "$_sdd_failed" = "0" ]; then
                printf '%s' "$_sdd_ver" > "$_sdd_sentinel"
                echo "    installed v${_sdd_ver}"
            else
                echo "    install failed (log: $_sdd_log)"
                tail -n 5 "$_sdd_log" 2>/dev/null | sed 's/^/      /'
            fi
            unset _sdd_log
        fi

        # Manifest-managed skills: regen the fenced .gitignore block when
        # the bootstrap ran successfully OR when the fence is absent /
        # malformed (recovery path for users who nuked the block).
        if [ -n "$_sdd_manifests" ] && [ "$_sdd_failed" = "0" ]; then
            _sdd_needs_regen=0
            if [ "$_sdd_bootstrap_ran" = "1" ]; then
                _sdd_needs_regen=1
            elif [ ! -f "/workspace/.gitignore" ] \
                 || ! grep -qF "# >>> sdd-managed/${_sdd_key} (toolbox)" /workspace/.gitignore \
                 || ! grep -qF "# <<< sdd-managed/${_sdd_key} (toolbox)" /workspace/.gitignore; then
                _sdd_needs_regen=1
            fi
            if [ "$_sdd_needs_regen" = "1" ]; then
                _sdd_banner
                if command -v _sdd_regen_gitignore_fence >/dev/null 2>&1 \
                   && _sdd_regen_gitignore_fence "$_sdd_key" "$_sdd_manifests" "$_sdd_extras"; then
                    echo "    .gitignore: synced sdd-managed/${_sdd_key} fence"
                else
                    echo "    .gitignore: regen failed for ${_sdd_key}"
                fi
            fi
            unset _sdd_needs_regen
        fi
        unset _sdd_failed _sdd_bootstrap_ran
    done
    unset -f _sdd_banner
    if command -v _sdd_regen_gitignore_fence >/dev/null 2>&1; then
        unset -f _sdd_regen_gitignore_fence
    fi
    unset _sdd_state_dir _sdd_keys _sdd_key _sdd_upper \
        _sdd_pkg_var _sdd_ver_var _sdd_bin_var _sdd_steps_var _sdd_marker_var \
        _sdd_manifests_var _sdd_extras_var \
        _sdd_pkg _sdd_ver _sdd_bin _sdd_steps_raw _sdd_marker \
        _sdd_manifests _sdd_extras \
        _sdd_sentinel _sdd_cur _sdd_printed_banner
fi

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
