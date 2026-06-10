# toolbox bridge shim transport. Sourced (not executed) by the three
# shims — xdg-open, code/codium, proximo — so the state-dir location, the
# readiness checks, and the curl POST live in one place. Callers own every
# user-facing message and exit policy (xdg-open exits 0 so OAuth flows never
# block; code/proximo exit non-zero), so only the mechanics live here.

BRIDGE_STATE_DIR="/home/toolbox/.toolbox/bridge"
# Pre-rename host CLI mounts only the legacy browser-bridge location; fall
# back so a new image keeps working until the host toolbox is updated.
if [ ! -r "${BRIDGE_STATE_DIR}/token" ]; then
    BRIDGE_STATE_DIR="/home/toolbox/.toolbox/browser"
fi

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
bridge_post() {
    _bridge_status=$(curl --silent --output "$3" --write-out '%{http_code}' \
        --max-time "$4" \
        --header "Authorization: Bearer ${BRIDGE_TOKEN}" \
        --header "Content-Type: application/json" \
        --data "$2" \
        "http://host.docker.internal:${BRIDGE_PORT}$1" 2>/dev/null) || _bridge_status=000
    printf '%s' "${_bridge_status}"
}
