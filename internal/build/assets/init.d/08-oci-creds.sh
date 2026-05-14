#!/usr/bin/env bash
set -euo pipefail

# Credential probe — reports whether oci has a usable config + live API
# access. Self-gates so an INSTALL_OCI=false image exits silently.
#
# Two-stage probe by design:
#   1. config-file presence check avoids a multi-second timeout on every
#      boot for users who never configured oci.
#   2. `oci iam region list` is the live API call (~200-500ms when
#      configured).
#
# </dev/null is load-bearing — without it, oci's "Do you want to create a
# new config file? [Y/n]" prompt blocks the entrypoint on the container TTY
# and never reaches the startup hooks.
# Three-way outcome (decision D-08-creds-tristate): the original two-way
# probe lumped "config file missing" and "config present + live API
# rejection" into the same "not configured" string — hiding expired keys
# and tenancy/region misconfiguration behind a fresh-install message.
command -v oci >/dev/null 2>&1 || exit 0

if [ ! -f "$HOME/.oci/config" ]; then
    echo "  oci: not configured"
elif oci iam region list --output table </dev/null >/dev/null 2>&1; then
    echo "  oci: configured"
else
    echo "  oci: auth check failed (verify ~/.oci/config + key fingerprint)"
fi
