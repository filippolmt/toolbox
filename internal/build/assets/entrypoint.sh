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

# gh (GitHub CLI)
if gh auth status >/dev/null 2>&1; then
    echo "  gh: configured"
else
    echo "  gh: not configured"
fi

# glab (GitLab CLI)
if glab auth status >/dev/null 2>&1; then
    echo "  glab: configured"
else
    echo "  glab: not configured"
fi

# Conditional checks for cloud CLIs (may be volume-mounted from host)
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
    if oci iam region list --output table >/dev/null 2>&1; then
        echo "  oci: configured"
    else
        echo "  oci: not configured"
    fi
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
