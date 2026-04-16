#!/usr/bin/env bash
set -e

IMAGE="${1:-toolbox:local}"

echo "=== Toolbox Smoke Test ==="
echo "Image: ${IMAGE}"
echo ""

docker run --rm "${IMAGE}" bash -c '
set -e

PASS=0
FAIL=0

check_tool() {
    local name="$1"
    shift
    if output=$("$@" 2>&1); then
        echo "OK: ${name} — ${output}"
        PASS=$((PASS+1))
    else
        echo "FAILED: ${name}"
        FAIL=$((FAIL+1))
    fi
}

check_tool "node"       node --version
check_tool "npm"        npm --version
check_tool "pnpm"       pnpm --version
check_tool "claude"     claude --version
check_tool "python3"    python3 --version
check_tool "uv"         uv --version
check_tool "kubectl"    kubectl version --client
check_tool "helm"       helm version --short
check_tool "tofu"       tofu version
check_tool "gh"         gh --version
check_tool "glab"       glab --version
check_tool "docker"     docker --version
check_tool "jq"         jq --version
check_tool "yq"         yq --version
check_tool "starship"   starship --version
check_tool "git"        git --version

echo ""
echo "=== Results: ${PASS} passed, ${FAIL} failed ==="
[ "$FAIL" -eq 0 ] || exit 1
'
