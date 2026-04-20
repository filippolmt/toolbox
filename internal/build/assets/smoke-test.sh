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
SKIP=0

# Required tools — fail if absent. These are baked unconditionally (base apt
# layer + node from the base image).
check_required() {
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

# Optional tools — skip if the binary is absent (INSTALL_<TOOL>=false at build
# time). CI always builds the full image so every optional check runs there.
check_optional() {
    local name="$1"
    local bin="$2"
    shift 2
    if ! command -v "${bin}" >/dev/null 2>&1; then
        echo "SKIP: ${name} (not installed)"
        SKIP=$((SKIP+1))
        return
    fi
    if output=$("$@" 2>&1); then
        echo "OK: ${name} — ${output}"
        PASS=$((PASS+1))
    else
        echo "FAILED: ${name}"
        FAIL=$((FAIL+1))
    fi
}

check_required "node"       node --version
check_required "npm"        npm --version
check_required "python3"    python3 --version
check_required "git"        git --version

check_optional  "pnpm"      pnpm     pnpm --version
check_optional  "claude"    claude   claude --version
check_optional  "playwright" playwright playwright --version
check_optional  "playwright-cli" playwright-cli playwright-cli --version
check_optional  "uv"        uv       uv --version
check_optional  "kubectl"   kubectl  kubectl version --client
check_optional  "helm"      helm     helm version --short
check_optional  "tofu"      tofu     tofu version
check_optional  "gh"        gh       gh --version
check_optional  "glab"      glab     glab --version
check_optional  "docker"    docker   docker --version
check_optional  "compose"   docker   docker compose version
check_optional  "gcloud"    gcloud   gcloud --version
check_optional  "gke-gcloud-auth-plugin" gke-gcloud-auth-plugin gke-gcloud-auth-plugin --version
check_optional  "azure"     az       az --version
check_optional  "oci"       oci      oci --version
check_optional  "jq"        jq       jq --version
check_optional  "yq"        yq       yq --version
check_optional  "starship"  starship starship --version

echo ""
echo "=== Results: ${PASS} passed, ${FAIL} failed, ${SKIP} skipped ==="
[ "$FAIL" -eq 0 ] || exit 1
'

echo ""
echo "=== UID mapping check (runtime UID not baked in image) ==="
# Simulates macOS host UID (501) to verify the entrypoint injects /etc/passwd.
# Without the injection, ssh aborts with "No user exists for uid 501" and every
# git-over-ssh operation fails.
docker run --rm --user 501:20 "${IMAGE}" bash -c '
set -e
getent passwd "$(id -u)" >/dev/null || { echo "FAILED: no passwd entry for uid $(id -u)"; exit 1; }
ssh -V 2>&1 | grep -q OpenSSH || { echo "FAILED: ssh missing"; exit 1; }
ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=1 nonexistent.invalid true 2>&1 \
    | grep -q "No user exists for uid" && { echo "FAILED: ssh still reports missing uid"; exit 1; }
echo "OK: passwd entry injected for uid $(id -u)"
'
