#!/usr/bin/env bash
set -euo pipefail

# Credential probe — reports whether gh is authenticated. Self-gates so an
# INSTALL_GH=false image exits silently.
#
# Three-way outcome (decision D-08-creds-tristate):
#   configured        — `gh auth status` exits 0.
#   auth check failed — token stored but verification fails (expired /
#                       revoked / network unreachable on first call).
#                       Detected by: a token IS present (`gh auth token`
#                       succeeds) but `gh auth status` does not — the user
#                       believes they're authenticated and would otherwise
#                       hit an opaque error later in the shell.
#   not configured    — no token at all.
command -v gh >/dev/null 2>&1 || exit 0

if gh auth status >/dev/null 2>&1; then
    echo "  gh: configured"
elif gh auth token >/dev/null 2>&1; then
    echo "  gh: auth check failed (try \`gh auth refresh\` or \`gh auth login\`)"
else
    echo "  gh: not configured"
fi
