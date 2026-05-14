#!/usr/bin/env bash
set -euo pipefail

# Credential probe — reports whether az has an active subscription context.
# Self-gates so an INSTALL_AZURE=false image exits silently.
#
# Three-way outcome (decision D-08-creds-tristate):
#   configured        — `az account show` succeeds.
#   auth check failed — subscriptions are cached locally (~/.azure/) but
#                       the token verification fails. Probed via
#                       `az account list --refresh=false`, which reads
#                       cached state without contacting Microsoft Entra ID,
#                       so it costs nothing when no creds are present.
#   not configured    — no cached state at all.
command -v az >/dev/null 2>&1 || exit 0

if az account show >/dev/null 2>&1; then
    echo "  azure: configured"
elif az account list --refresh=false --query "[].id" --output tsv 2>/dev/null | grep -q .; then
    echo "  azure: auth check failed (try \`az login\`)"
else
    echo "  azure: not configured"
fi
