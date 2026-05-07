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

# gh (GitHub CLI) — gated on binary presence so tools.gh=false skips the
# block instead of mislabelling "not installed" as "not configured".
if command -v gh >/dev/null 2>&1; then
    if gh auth status >/dev/null 2>&1; then
        echo "  gh: configured"
    else
        echo "  gh: not configured"
    fi
fi

# glab (GitLab CLI) — same binary-presence gating as gh above.
if command -v glab >/dev/null 2>&1; then
    if glab auth status >/dev/null 2>&1; then
        echo "  glab: configured"
    else
        echo "  glab: not configured"
    fi
fi

if command -v gcloud >/dev/null 2>&1; then
    if gcloud auth list --filter=status:ACTIVE --format='value(account)' 2>/dev/null | grep -q .; then
        echo "  gcloud: configured"
    else
        echo "  gcloud: not configured"
    fi
fi

if command -v az >/dev/null 2>&1; then
    if az account show >/dev/null 2>&1; then
        echo "  az: configured"
    else
        echo "  az: not configured"
    fi
fi

if command -v oci >/dev/null 2>&1; then
    # Fast path: skip the live API call when no config file exists. `oci iam
    # region list` is a real network round-trip (~200-500ms when configured,
    # multi-second timeout otherwise); checking the config file first keeps
    # entrypoint snappy for users who haven't run `oci setup config`.
    #
    # </dev/null: oci prompts "Do you want to create a new config file? [Y/n]"
    # when ~/.oci/config is missing. Without a closed stdin it would block the
    # entrypoint on the container's TTY and never reach the startup hooks.
    if [ ! -f "$HOME/.oci/config" ]; then
        echo "  oci: not configured"
    elif oci iam region list --output table </dev/null >/dev/null 2>&1; then
        echo "  oci: configured"
    else
        echo "  oci: not configured"
    fi
fi

# Per-tool init scripts (Init Sequence — see CONTEXT.md glossary).
# Each init.d/<NN>-<tool>.sh runs in a fresh bash subshell (D-05). Stderr is
# captured to a per-script log; on failure the iterator surfaces the tail-5
# inline so the user sees actionable diagnostics without scrolling. The
# iterator's `if !` neutralises the outer `set -e` (Pitfall 9), so a failed
# init script never aborts boot — startup hooks and `exec "$@"` always run.
#
# Marker-log path: $HOME/.toolbox-state/init/<name>.log. The container path
# is .toolbox-state (NOT .toolbox/state) because the `state` bind-mount
# resolves Source ~/.toolbox/state -> Target ~/.toolbox-state inside the
# container (Pitfall 1 / mountplan defaults).
INIT_D="/usr/local/lib/toolbox/init.d"
TOOLBOX_INIT_LOG_DIR="$HOME/.toolbox-state/init"
if [ -d "$INIT_D" ]; then
    mkdir -p "$TOOLBOX_INIT_LOG_DIR"
    for f in "$INIT_D"/*.sh; do
        [ -r "$f" ] || continue
        _name=$(basename "$f")
        _log="${TOOLBOX_INIT_LOG_DIR}/${_name%.sh}.log"
        if ! bash "$f" 2>"$_log"; then
            echo "  ${_name}: failed (log: $_log)"
            [ -s "$_log" ] && tail -n 5 "$_log" | sed 's/^/      /'
        else
            rm -f "$_log"
        fi
    done
    unset _name _log f
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
