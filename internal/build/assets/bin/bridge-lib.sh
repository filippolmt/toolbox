# toolbox bridge shim transport. The state-dir, legacy-dir and socket paths
# below are the container side of the daemon<->shim contract; they must match
# the Go constants in internal/bridge/paths.go (ContainerDir, LegacyContainerDir,
# ContainerSocket, tokenFile, portFile) — enforced by TestBridgeContract_ShimMatchesGo.
#
# Sourced (not executed) by every shim — xdg-open, code/codium, proximo,
# git-credential-toolbox, paplay — so the state-dir location, the readiness
# checks, and the curl POST live in one place. Callers own every
# user-facing message and exit policy (xdg-open exits 0 so OAuth flows never
# block; code/proximo exit non-zero), so only the mechanics live here.

BRIDGE_STATE_DIR="/home/toolbox/.toolbox/bridge"
# Pre-rename host CLI mounts only the legacy browser-bridge location; fall
# back so a new image keeps working until the host toolbox is updated.
if [ ! -r "${BRIDGE_STATE_DIR}/token" ]; then
    BRIDGE_STATE_DIR="/home/toolbox/.toolbox/browser"
fi

# Daemon unix socket (native-Linux hosts; RW bridge-run mount). Full literal,
# independent of the legacy BRIDGE_STATE_DIR fallback — the run/ mount only
# exists at the new path. Absent on macOS, on pre-socket hosts, and on Docker
# Desktop; bridge_post falls back to TCP via host.docker.internal there.
BRIDGE_SOCK="/home/toolbox/.toolbox/bridge/run/bridge.sock"

# bridge_ready: verifies the RO-mounted bridge state and curl, then loads
# BRIDGE_TOKEN / BRIDGE_PORT. On failure returns non-zero with the reason in
# BRIDGE_ERROR (not_installed | no_curl) for the caller to render.
bridge_ready() {
    if [ ! -r "${BRIDGE_STATE_DIR}/token" ] || [ ! -r "${BRIDGE_STATE_DIR}/port" ]; then
        BRIDGE_ERROR="not_installed"
        return 1
    fi
    if ! command -v curl >/dev/null 2>&1; then
        BRIDGE_ERROR="no_curl"
        return 1
    fi
    BRIDGE_TOKEN=$(cat "${BRIDGE_STATE_DIR}/token")
    BRIDGE_PORT=$(cat "${BRIDGE_STATE_DIR}/port")
}

# bridge_post <endpoint> <payload> <body-file> <max-time>: POSTs payload to
# the daemon endpoint, writes the response body to body-file (use /dev/null
# to discard), and prints the HTTP status — 000 on transport failure. Never
# propagates a curl failure, so it is safe under `set -e`.
# _bridge_curl <url> <payload> <body-file> <max-time> [curl-flags...]: the
# shared curl invocation; extra flags (e.g. --unix-socket) ride on "$@".
_bridge_curl() {
    _bridge_url=$1; _bridge_payload=$2; _bridge_body=$3; _bridge_maxtime=$4
    shift 4
    curl --silent --output "${_bridge_body}" --write-out '%{http_code}' \
        --max-time "${_bridge_maxtime}" \
        --header "Authorization: Bearer ${BRIDGE_TOKEN}" \
        --header "Content-Type: application/json" \
        --data "${_bridge_payload}" \
        "$@" \
        "${_bridge_url}" 2>/dev/null
}

bridge_post() {
    if [ -S "${BRIDGE_SOCK}" ]; then
        _bridge_status=$(_bridge_curl "http://localhost$1" "$2" "$3" "$4" \
            --unix-socket "${BRIDGE_SOCK}") || _bridge_status=000
        # 000 = dead transport (stale socket after a daemon crash, or a file
        # that is visible but not traversable — Docker Desktop on Linux):
        # retry over TCP. Any real HTTP status (even 4xx/5xx) must NOT be
        # retried — a second POST would re-execute /proximo on the host.
        if [ "${_bridge_status}" != "000" ]; then
            printf '%s' "${_bridge_status}"
            return 0
        fi
    fi
    _bridge_status=$(_bridge_curl "http://host.docker.internal:${BRIDGE_PORT}$1" \
        "$2" "$3" "$4") || _bridge_status=000
    printf '%s' "${_bridge_status}"
}
