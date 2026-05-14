#!/usr/bin/env bash
set -euo pipefail

# Credential probe — reports whether gcloud has an ACTIVE account. Self-gates
# so an INSTALL_GCLOUD=false image exits silently. The `| grep -q .` is
# load-bearing: gcloud always exits 0, presence of output is the signal.
#
# Three-way outcome (decision D-08-creds-tristate):
#   configured        — an ACTIVE account exists.
#   auth check failed — accounts are registered but none are ACTIVE
#                       (revoked, token expired, or `gcloud auth revoke`
#                       was run without re-login). User otherwise sees an
#                       opaque "no active account" the next time they run
#                       a gcloud command.
#   not configured    — no accounts at all.
command -v gcloud >/dev/null 2>&1 || exit 0

if gcloud auth list --filter=status:ACTIVE --format='value(account)' 2>/dev/null | grep -q .; then
    echo "  gcloud: configured"
elif gcloud auth list --format='value(account)' 2>/dev/null | grep -q .; then
    echo "  gcloud: auth check failed (try \`gcloud auth login\` or \`gcloud config set account <email>\`)"
else
    echo "  gcloud: not configured"
fi
