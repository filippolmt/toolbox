#!/usr/bin/env bash
set -euo pipefail

# Loopback bridge — runtime side of `toolbox shell -B`.
#
# sessionplan.Plan emits TOOLBOX_LOOPBACK_BRIDGE_PORTS=<comma-joined> when
# the user passed `-B` together with `-p`. For each port we spawn one socat
# process that listens on the container's external interface IP (eth0) and
# forwards to 127.0.0.1:<port>, making CLIs that bind container loopback
# (shopify store auth, vanilla wrangler login, …) reachable from the host
# browser via the existing `docker -p` forward.
#
# Explicit bind=$ETH_IP (not 0.0.0.0) so a legitimate in-container
# 0.0.0.0:<port> listener does not collide with the bridge. Per-port
# EADDRINUSE is logged and the loop continues — one bad port never blocks
# the rest.
#
# Background ownership: `setsid nohup … & disown` detaches each socat from
# this script so the init.d harness sees the child exit immediately and the
# entrypoint moves on. The processes live for the container lifetime; tini
# reaps them on `toolbox stop`.
#
# Inner gate: bridge env present AND socat binary installed
# (INSTALL_LOOPBACK_BRIDGE=false at build time skips the apt-install).

if [ -z "${TOOLBOX_LOOPBACK_BRIDGE_PORTS:-}" ]; then
    if [ "${TOOLBOX_LOOPBACK_BRIDGE_NO_PUBLISH:-}" = "1" ]; then
        echo "  loopback bridge: enabled but no -p ports published — skipping"
    fi
    exit 0
fi

if ! command -v socat >/dev/null 2>&1; then
    echo "  loopback bridge: socat missing — image build defect"
    exit 0
fi

ETH_IP=$(hostname -i 2>/dev/null | awk '{print $1}')
if [ -z "${ETH_IP:-}" ]; then
    echo "  loopback bridge: could not resolve eth0 IP via 'hostname -i' — skipping"
    exit 0
fi

LOG="$HOME/.toolbox-state/init/70-loopback-bridge.log"
mkdir -p "$(dirname "$LOG")"

IFS=',' read -ra _ports <<< "$TOOLBOX_LOOPBACK_BRIDGE_PORTS"
_started=()
for _p in "${_ports[@]}"; do
    [ -z "$_p" ] && continue
    if setsid nohup socat \
        "TCP-LISTEN:${_p},bind=${ETH_IP},fork,reuseaddr" \
        "TCP:127.0.0.1:${_p}" \
        >>"$LOG" 2>&1 </dev/null &
    then
        disown 2>/dev/null || true
        _started+=("$_p")
    else
        echo "  loopback bridge: failed to spawn socat for port ${_p} (see $LOG)"
    fi
done

if [ "${#_started[@]}" -gt 0 ]; then
    _joined=$(IFS=', '; echo "${_started[*]}")
    echo "  loopback bridge: ${_joined}"
fi
