#!/usr/bin/env bash
set -euo pipefail

# Credential probe — reports whether gcloud has an ACTIVE account. Self-gates
# so an INSTALL_GCLOUD=false image exits silently. The `| grep -q .` is
# load-bearing: gcloud always exits 0, presence of output is the signal.
command -v gcloud >/dev/null 2>&1 || exit 0

if gcloud auth list --filter=status:ACTIVE --format='value(account)' 2>/dev/null | grep -q .; then
    echo "  gcloud: configured"
else
    echo "  gcloud: not configured"
fi
