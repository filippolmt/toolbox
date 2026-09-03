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

# git safe.directory
# (docs/internals/shell-start.md#git-safedirectory-dubious-ownership).
# A bind-mounted directory transiently reports uid 0 while its own contents keep
# the host uid, and git checks the worktree rather than the files in it, so it
# refuses a repository that is in fact ours with "dubious ownership". Captured on
# the workspace by a 2s probe: 95 failures, 91 of them with euid=501 and mount
# point uid=0 in the same instant (the other 4 had already closed the window
# before the probe's own stat), while .git one level down kept uid 501 throughout.
# Often, but not always, a second container is mounting the same host path at the
# time (45 of the 95), so the trigger is not pinned down — but the wrong uid
# arrives from Docker Desktop's file sharing either way, not from anything this
# image can correct. Registering makes the question moot: git consults
# safe.directory whenever the ownership check does not pass.
#
# Registered as the wildcard rather than as a list of paths, because the affected
# repositories cannot be enumerated here. ~/.claude is a bind mount of the same
# kind as the workspace, and `claude plugin update` clones every git-subdir plugin
# into a randomly named directory under ~/.claude/plugins/cache — so the worktree
# git discovers has a name that does not exist yet at boot. git matches
# safe.directory against that discovered root, and on the git this image ships
# (unpinned, from the base apt block) only an exact path or the wildcard matches:
# no glob form does, the trailing `/*` that a newer git accepts recursively
# included. The wildcard does trust more than the paths it replaces: git will
# honour the config and hooks of a repository owned by some other uid that
# arrives through any mount, where before only the workspace root was trusted
# blanket. Three things make that acceptable rather than free — enumeration
# cannot cover the case this fixes at all, the check never applied to a hostile
# repository the user cloned themselves (that one already carries their own
# uid), and a container running as a single uid with passwordless sudo is not a
# boundary anything in it would have to cross.
#
# Runs before the init sequence, because 30-graphify.sh asks git whether the
# workspace is a work tree with output suppressed, so an ownership fatal there
# skips the hook install in silence. System scope only, same discipline as the git
# credential helper further down and init.d/60-glab.sh: the host ~/.gitconfig is a
# RW mount and must not be polluted, /etc/gitconfig dies with the AutoRemove
# container. --global, which git's own message suggests, could not work here
# anyway — that mount is a bind mount of a single file, so git's rename-in-place
# write fails with EBUSY. Idempotent because ActionStart re-runs the entrypoint on
# a restarted container. The gitconfig lock is the house discipline for
# /etc/gitconfig rather than a live race here — running above the init sequence is
# what keeps this clear of 60-glab.sh, which writes the same file from init
# scripts that do run in parallel; the lock is what would keep it correct if this
# block ever moved below them. Non-fatal: a failed registration warns and boots
# on.
sudo flock /tmp/toolbox-gitconfig.lock sh -c '
    git config --system --get-all safe.directory 2>/dev/null | grep -qxF "$1" \
      || git config --system --add safe.directory "$1"
' _ '*' \
  || echo "toolbox: git safe.directory registration failed (non-fatal — git may report dubious ownership on any bind-mounted repository)"

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

# User zsh config lives on the state mount because ~/.zshrc is truncated by
# every image rebuild. Created empty so the extension point is discoverable
# via `ls ~/.toolbox-state`; zshrc.sh tolerates its absence either way.
# → docs/internals/shell-start.md#user-config-in-zshrcd
mkdir -p "$HOME/.toolbox-state/zshrc.d" 2>/dev/null || true
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

# Global CA trust (docs/mounts.md#ca-certificate-trust). Any cert file dropped
# into ~/.toolbox/certs (RO-mounted at /etc/toolbox/certs) is trusted at each
# shell start across all four surfaces, mirroring the proximo block:
#   - system bundle (curl / git / wget / python-ssl): stage each cert under
#     /usr/local/share/ca-certificates/ + a single update-ca-certificates
#   - NSS db (Chromium / Firefox / Playwright): certutil into $HOME/.pki/nssdb
#   - Node + python-requests: NODE_EXTRA_CA_CERTS / REQUESTS_CA_BUNDLE repointed
#     at the full system bundle (a superset of proximo's single-file value)
# PEM passes through; DER is converted via the python3 stdlib (no openssl CLI in
# the image). Multi-cert PEM files are split so each cert becomes one staged
# .crt. Self-gated on a non-empty folder, idempotent (cmp -s + nickname checks),
# every step best-effort so a malformed file never aborts boot.
_certs_dir="/etc/toolbox/certs"
if [ -d "$_certs_dir" ] && [ -n "$(ls -A "$_certs_dir" 2>/dev/null)" ]; then
    _certs_present=0 # a valid cert was trusted this shell (gates the env export)
    _certs_changed=0 # a cert was newly copied (gates update-ca-certificates)
    _certs_nss="$HOME/.pki/nssdb"
    _certs_have_nss=0
    if command -v certutil >/dev/null 2>&1; then
        mkdir -p "$_certs_nss"
        [ -f "$_certs_nss/cert9.db" ] || certutil -d "sql:$_certs_nss" -N --empty-password >/dev/null 2>&1 || true
        _certs_have_nss=1
    fi
    _certs_tmp=$(mktemp -d)

    # Stage one single-cert PEM into the system store (cmp -s gated) and NSS
    # (existence-gated), mirroring the proximo block. $1 = sanitized name.
    _stage_cert() {
        local _nm="$1" _pf="$2"
        # Skip-guard, not a conversion — a real PEM is still staged as-is. It
        # rejects non-certificates: ssl.DER_cert_to_PEM_cert base64-wraps any
        # bytes without validating, and a bogus BEGIN/END wrapper passes the
        # grep detector, so an unchecked malformed file would land a junk .crt.
        # load_verify_locations parses the block (stdlib, no openssl CLI) and
        # raises on anything that is not a real cert. Only vet when python3 can:
        # if it is unavailable we stage best-effort rather than drop a possibly
        # valid cert, and a rejection is logged so it is never silent.
        if command -v python3 >/dev/null 2>&1 \
           && ! python3 -c 'import ssl,sys; ssl.create_default_context().load_verify_locations(sys.argv[1])' "$_pf" 2>/dev/null; then
            echo "toolbox: skipping ${_nm} from /etc/toolbox/certs (not a valid certificate)" >&2
            return 0
        fi
        local _dst="/usr/local/share/ca-certificates/toolbox-cert-${_nm}.crt"
        if [ ! -f "$_dst" ] || ! cmp -s "$_pf" "$_dst"; then
            if sudo cp "$_pf" "$_dst" 2>/dev/null; then _certs_changed=1; _certs_present=1; fi
        else
            _certs_present=1
        fi
        if [ "$_certs_have_nss" = "1" ]; then
            certutil -d "sql:$_certs_nss" -L -n "toolbox-${_nm}" >/dev/null 2>&1 \
              || certutil -d "sql:$_certs_nss" -A -t C,, -n "toolbox-${_nm}" -i "$_pf" >/dev/null 2>&1 || true
        fi
    }

    for _cert_src in "$_certs_dir"/*.pem "$_certs_dir"/*.crt "$_certs_dir"/*.cer "$_certs_dir"/*.der; do
        [ -f "$_cert_src" ] || continue # skip unmatched literal globs (no nullglob)
        # Normalize to PEM: a file carrying a PEM block passes through; anything
        # else is tried as DER via the python3 stdlib.
        _cert_pem="$_certs_tmp/norm.pem"
        if grep -q 'BEGIN CERTIFICATE' "$_cert_src" 2>/dev/null; then
            cat "$_cert_src" > "$_cert_pem" 2>/dev/null || continue
        elif command -v python3 >/dev/null 2>&1; then
            python3 -c 'import ssl,sys; sys.stdout.write(ssl.DER_cert_to_PEM_cert(open(sys.argv[1],"rb").read()))' \
                "$_cert_src" > "$_cert_pem" 2>/dev/null || continue
        else
            continue
        fi
        [ -s "$_cert_pem" ] || continue
        # Deterministic store name / NSS nickname from the sanitized basename.
        _cert_base=$(basename "$_cert_src")
        _cert_base=$(printf '%s' "${_cert_base%.*}" | tr -c '[:alnum:]' '-')
        # Split multi-cert PEM into one file per cert (Debian's
        # update-ca-certificates is most reliable one-cert-per-file).
        rm -f "$_certs_tmp"/split-*.pem
        awk -v d="$_certs_tmp" '/BEGIN CERTIFICATE/{n++} n>0{print > (d "/split-" n ".pem")}' "$_cert_pem" 2>/dev/null || true
        _cert_total=$(grep -c 'BEGIN CERTIFICATE' "$_cert_pem" 2>/dev/null || echo 0)
        for _cert_block in "$_certs_tmp"/split-*.pem; do
            [ -f "$_cert_block" ] || continue
            _cert_i="$(basename "$_cert_block" .pem)"
            _cert_i="${_cert_i#split-}"
            if [ "$_cert_total" -le 1 ]; then
                _stage_cert "$_cert_base" "$_cert_block"
            else
                _stage_cert "${_cert_base}-${_cert_i}" "$_cert_block"
            fi
        done
    done

    # Refresh the bundle only when a cert was actually copied (update-ca-
    # certificates is not free; on an unchanged folder re-shell it is skipped).
    # The env re-export is gated on presence, not change: a fresh AutoRemove
    # container has an empty process env every shell, so it must run whenever a
    # cert is trusted — repointing Node + python-requests at the full bundle so
    # the shell inherits the superset (overriding proximo's single-file value).
    if [ "$_certs_changed" = "1" ]; then
        sudo update-ca-certificates >/dev/null 2>&1 || true
    fi
    if [ "$_certs_present" = "1" ]; then
        export NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt
        export REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
    fi
    rm -rf "$_certs_tmp"
    unset -f _stage_cert
    unset _certs_present _certs_changed _certs_nss _certs_have_nss _certs_tmp \
        _cert_src _cert_pem _cert_base _cert_total _cert_block _cert_i
fi
unset _certs_dir

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
