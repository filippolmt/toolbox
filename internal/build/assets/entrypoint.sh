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
    echo "toolbox:x:${_uid}:${_gid}:toolbox:/home/toolbox:/bin/zsh" >> /etc/passwd
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

# proximo CA trust (docs/runtime-notes.md#proximo-integration). When the host
# config sets `proximo: true`, sessionplan RO-mounts proximo's root CA at
# /etc/ssl/proximo-ca.pem. Establish full trust so ANY in-container HTTPS client
# reaches https://<name>.test without per-tool flags:
#   - system bundle (curl / git / wget / python ssl+urllib): update-ca-certificates
#   - NSS db (Chromium / Firefox, incl. Playwright's bundled browsers, which read
#     $HOME/.pki/nssdb — not the system bundle, not NODE_EXTRA_CA_CERTS): certutil
# NODE_EXTRA_CA_CERTS (set by sessionplan) already covers Node. python-requests
# uses certifi, not the system store — point REQUESTS_CA_BUNDLE at
# $TOOLBOX_PROXIMO_CA for that one. Self-gated on the mount, idempotent, every
# step best-effort so a trust failure never aborts boot.
_proximo_ca="/etc/ssl/proximo-ca.pem"
if [ -f "$_proximo_ca" ]; then
    # System trust: the ca-certificates source dir wants a .crt; refresh the
    # bundle only when the cert is new (update-ca-certificates is not free).
    # sudo is passwordless + UID-agnostic in this image (see Dockerfile).
    if [ ! -f /usr/local/share/ca-certificates/proximo.crt ] \
       || ! cmp -s "$_proximo_ca" /usr/local/share/ca-certificates/proximo.crt; then
        sudo cp "$_proximo_ca" /usr/local/share/ca-certificates/proximo.crt 2>/dev/null \
          && sudo update-ca-certificates >/dev/null 2>&1 || true
    fi
    # NSS trust for Chromium/Firefox. ~/.pki is ephemeral (HOME subdir, not a
    # bind-mount) so it is rebuilt from the mounted CA on every shell.
    if command -v certutil >/dev/null 2>&1; then
        _nssdb="$HOME/.pki/nssdb"
        mkdir -p "$_nssdb"
        [ -f "$_nssdb/cert9.db" ] || certutil -d "sql:$_nssdb" -N --empty-password >/dev/null 2>&1 || true
        certutil -d "sql:$_nssdb" -L -n proximo >/dev/null 2>&1 \
          || certutil -d "sql:$_nssdb" -A -t C,, -n proximo -i "$_proximo_ca" >/dev/null 2>&1 || true
        unset _nssdb
    fi
    # Auto-sync proximo .test names into /etc/hosts and keep them in sync as
    # stacks come and go. --add-host pins are fixed at container create, so a
    # stack started later would otherwise be unreachable until a re-shell; the
    # backgrounded watcher (docker events) makes it automatic — no manual
    # `proximo-hosts`. Best-effort: needs the mounted docker socket; a missing
    # socket just leaves the watcher idle-looping with no effect.
    if command -v docker >/dev/null 2>&1 && command -v proximo-hosts >/dev/null 2>&1; then
        _px_log="$HOME/.toolbox-state/proximo-hosts.log"
        mkdir -p "$(dirname "$_px_log")" 2>/dev/null || true
        setsid nohup proximo-hosts --watch >>"$_px_log" 2>&1 </dev/null &
        disown 2>/dev/null || true
        unset _px_log
    fi
fi
unset _proximo_ca

# Git credential helper via the bridge. The container can't reach the host
# credential store (macOS Keychain / Linux secret-service), so a plain
# `git clone https://…` for a self-hosted host (Forgejo, Gitea, …) — anything
# glab/gh don't cover — prompts on every clone. When the bridge is installed,
# register our forwarding helper in the SYSTEM gitconfig so git consults the
# host's configured helper through the daemon's /credential endpoint instead.
# System scope only: the host ~/.gitconfig is a RW mount and must not be
# polluted; /etc/gitconfig is container-local and dies with the AutoRemove
# container (same discipline as init.d/60-glab.sh). Absolute helper path —
# `brew tap` and other callers may run git under a scrubbed PATH without
# /usr/local/bin (see 60-glab.sh). Opt out per-repo with env:
# GIT_CREDENTIAL_BRIDGE=0 (un-prefixed: config ValidateEnv reserves TOOLBOX_*,
# so it couldn't travel through env: otherwise). Self-gated on the bridge token
# (new + legacy paths); non-fatal so a failure never aborts boot.
_bridge_token="${HOME}/.toolbox/bridge/token"
[ -r "$_bridge_token" ] || _bridge_token="${HOME}/.toolbox/browser/token"
case "${GIT_CREDENTIAL_BRIDGE:-}" in
0 | false | no | off) _bridge_token="" ;;
esac
if [ -n "$_bridge_token" ] && [ -r "$_bridge_token" ]; then
    sudo flock /tmp/toolbox-gitconfig.lock \
        git config --system credential.helper '!/usr/local/bin/git-credential-toolbox' 2>/dev/null \
      || echo "toolbox: git credential bridge helper registration failed (non-fatal — git will prompt for HTTPS credentials)"
fi
unset _bridge_token

# Repo-local SDD (Spec-Driven Development) bootstrap. Opt-in per skill via
# `sdd.<key>: true` in the workspace's .toolbox.yaml. sessionplan emits one
# env var per field (TOOLBOX_SDD_<KEY>_{PKG,VERSION,BIN,STEPS,MARKER}) on
# top of TOOLBOX_SDD_ENABLED + TOOLBOX_SDD_WORKSPACE_HASH. Failures log
# inline and never abort the entrypoint.
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

    IFS=',' read -ra _sdd_keys <<< "$TOOLBOX_SDD_ENABLED"
    for _sdd_key in "${_sdd_keys[@]}"; do
        [ -z "$_sdd_key" ] && continue
        _sdd_upper="${_sdd_key^^}" # builtin: no fork per skill per shell
        _sdd_pkg_var="TOOLBOX_SDD_${_sdd_upper}_PKG"
        _sdd_ver_var="TOOLBOX_SDD_${_sdd_upper}_VERSION"
        _sdd_bin_var="TOOLBOX_SDD_${_sdd_upper}_BIN"
        _sdd_steps_var="TOOLBOX_SDD_${_sdd_upper}_STEPS"
        _sdd_marker_var="TOOLBOX_SDD_${_sdd_upper}_MARKER"
        _sdd_pkg="${!_sdd_pkg_var:-}"
        _sdd_ver="${!_sdd_ver_var:-}"
        _sdd_bin="${!_sdd_bin_var:-}"
        _sdd_steps_raw="${!_sdd_steps_var:-}"
        _sdd_marker="${!_sdd_marker_var:-}"
        if [ -z "$_sdd_pkg" ] || [ -z "$_sdd_ver" ] || [ -z "$_sdd_bin" ] || [ -z "$_sdd_steps_raw" ]; then
            continue
        fi
        if [ -n "$_sdd_marker" ] && [ ! -e "./${_sdd_marker}" ]; then
            _sdd_banner
            echo "  ${_sdd_key}: skipped — no '${_sdd_marker}' in workspace."
            echo "    Run '${_sdd_bin}' init manually once, commit, then re-shell."
            continue
        fi
        # Sentinel fingerprint covers version AND steps: a steps override in
        # .toolbox.yaml (no version bump) must re-run the bootstrap, or the
        # workspace would keep the previous layout until the next Renovate
        # bump. Pre-fingerprint sentinels (bare version) mismatch once and
        # trigger a single idempotent reinstall.
        _sdd_sentinel="$_sdd_state_dir/sdd.${_sdd_key}.${TOOLBOX_SDD_WORKSPACE_HASH}.version"
        _sdd_want="${_sdd_ver}|${_sdd_steps_raw}"
        _sdd_cur=""
        IFS= read -r _sdd_cur < "$_sdd_sentinel" 2>/dev/null || true
        if [ "$_sdd_cur" = "$_sdd_want" ]; then
            continue
        fi
        _sdd_banner
        echo "  ${_sdd_key}: installing ${_sdd_pkg}@${_sdd_ver} (repo-local)..."
        mkdir -p "$_sdd_state_dir"
        _sdd_log="$_sdd_state_dir/sdd.${_sdd_key}.log"
        : > "$_sdd_log"
        _sdd_failed=0
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
            printf '%s' "$_sdd_want" > "$_sdd_sentinel"
            echo "    installed v${_sdd_ver}"
        else
            echo "    install failed (log: $_sdd_log)"
            tail -n 5 "$_sdd_log" 2>/dev/null | sed 's/^/      /'
        fi
        unset _sdd_log _sdd_failed
    done
    unset -f _sdd_banner
    unset _sdd_state_dir _sdd_keys _sdd_key _sdd_upper \
        _sdd_pkg_var _sdd_ver_var _sdd_bin_var _sdd_steps_var _sdd_marker_var \
        _sdd_pkg _sdd_ver _sdd_bin _sdd_steps_raw _sdd_marker \
        _sdd_sentinel _sdd_want _sdd_cur _sdd_printed_banner
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
