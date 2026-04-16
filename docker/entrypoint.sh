#!/usr/bin/env bash
set -euo pipefail

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

exec "$@"
