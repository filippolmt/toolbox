#!/usr/bin/env bash
set -euo pipefail

# Credential probe — reports whether az has an active subscription context.
# Self-gates so an INSTALL_AZURE=false image exits silently.
command -v az >/dev/null 2>&1 || exit 0

if az account show >/dev/null 2>&1; then
    echo "  azure: configured"
else
    echo "  azure: not configured"
fi
