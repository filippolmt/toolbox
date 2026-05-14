#!/usr/bin/env bash
set -euo pipefail

# Credential probe — reports whether gh is authenticated. Self-gates so an
# INSTALL_GH=false image exits silently. Best-effort: a non-zero auth status
# is "not configured", not a failure.
command -v gh >/dev/null 2>&1 || exit 0

if gh auth status >/dev/null 2>&1; then
    echo "  gh: configured"
else
    echo "  gh: not configured"
fi
