#!/usr/bin/env bash
set -euo pipefail

# Orca Shell — install and start Orca's server (CONTEXT.md, Orca Shell).
#
# Gate: TOOLBOX_ORCA_VERSION, which only the Orca Shell's session plan
# carries; its value is the stablyai/orca release tag. Every other shell exits
# here and runs no Orca code.
#
# The entrypoint runs init.d scripts to completion before the shell, so the
# download and the server run in a detached child (`--foreground`): this
# script returns at once and never fails the container. The child appends
# everything it and the server print to $TOOLBOX_ORCA_LOG, serve.log on the
# state mount, which the host reads the pairing link from. The data directory
# ($ORCA_USER_DATA_PATH) is the Orca Shell's own volume instead: Orca keeps
# Unix sockets there, which a Host Share bind cannot hold.
#
# Layout under $ORCA_USER_DATA_PATH/server: one directory per version,
# extracted beside its final name and renamed into place only after the
# archive matched its line in the release's checksum file, and a `current`
# link switched atomically after that; a fresh install then prunes the
# versions older than it. A failed download falls back to the newest cached version with a
# `warning:` line; a mismatch, or a failed download with nothing cached, ends
# with a `fatal:` line and no server. No restart of a crashed server: tini
# (PID 1) keeps the container up, and the log says why the server stopped.
# However the run ends, its last log line is the stop line the host reads as
# "not serving" (internal/orca, ExitedMarker).

[ -n "${TOOLBOX_ORCA_VERSION:-}" ] || exit 0

data="${ORCA_USER_DATA_PATH:?ORCA_USER_DATA_PATH unset in an Orca Shell}"
log="${TOOLBOX_ORCA_LOG:?TOOLBOX_ORCA_LOG unset in an Orca Shell}"

if [ "${1:-}" != "--foreground" ]; then
    mkdir -p "$data" "$(dirname "$log")"
    setsid nohup bash "$0" --foreground >/dev/null 2>&1 </dev/null &
    disown 2>/dev/null || true
    echo "  orca: starting server ${TOOLBOX_ORCA_VERSION} (log: $log)"
    exit 0
fi

mkdir -p "$data" "$(dirname "$log")"
exec >>"$log" 2>&1 </dev/null
echo "toolbox: orca ${TOOLBOX_ORCA_VERSION}: start $(date -u +%Y-%m-%dT%H:%M:%SZ)"
tmp=""
on_exit() {
    local status=$?
    [ -z "$tmp" ] || rm -rf "$tmp"
    echo "toolbox: orca: server stopped with status $status"
}
trap on_exit EXIT

# fail ends an attempt that starts no server with the line the host reads to
# report the reason and stop the Orca Shell (orca.WaitPairing).
fail() {
    echo "toolbox: orca: fatal: $*"
    exit 1
}

version="$TOOLBOX_ORCA_VERSION"
# The tag lands in a URL and a path: the host validated it, this re-checks.
if ! [[ "$version" =~ ^v[0-9]+(\.[0-9]+)*$ ]]; then
    fail "refusing malformed version '$version'"
fi

case "$(uname -m)" in
x86_64 | amd64) arch=x64 ;;
aarch64 | arm64) arch=arm64 ;;
*)
    fail "no server build for architecture $(uname -m)"
    ;;
esac

servers="$data/server"
dest="$servers/$version"
mkdir -p "$servers"

installed=""
if [ ! -x "$dest/bin/orca-ide" ]; then
    archive="orca-server-linux-$arch.tar.gz"
    base="https://github.com/stablyai/orca/releases/download/$version"
    # Beside the destination, so the final rename stays on one filesystem.
    tmp=$(mktemp -d "$servers/.install-$version.XXXXXX")
    echo "toolbox: orca: downloading $base/$archive"
    if curl -fsSL --retry 3 -o "$tmp/$archive" "$base/$archive" &&
        curl -fsSL --retry 3 -o "$tmp/sums" "$base/orca-server-sha256sums.txt"; then
        want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/sums")
        if [ -z "$want" ]; then
            fail "checksum file has no line for $archive; not installing"
        fi
        got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
        if [ "$got" != "$want" ]; then
            fail "checksum mismatch for $archive (got $got, want $want); not installing"
        fi
        mkdir "$tmp/tree"
        tar -xzf "$tmp/$archive" -C "$tmp/tree" --strip-components=1
        rm -rf "$dest"
        mv "$tmp/tree" "$dest"
        rm -rf "$tmp"
        tmp=""
        installed=1
    else
        # Offline: the newest server already cached runs instead, by version
        # order; the warning line is relayed by the host.
        rm -rf "$tmp"
        tmp=""
        cached=$(cd "$servers" && for v in v*; do
            if [ -x "$v/bin/orca-ide" ]; then echo "$v"; fi
        done | sort -V | tail -n 1)
        if [ -z "$cached" ]; then
            fail "could not download Orca server $version and no server is cached; connect to the network and retry"
        fi
        echo "toolbox: orca: warning: could not download Orca server $version; starting the cached $cached instead, which may not match the desktop app"
        version=$cached
    fi
fi

ln -sfn "$version" "$servers/.current.new"
mv -T "$servers/.current.new" "$servers/current"

# Bounded cache: once current is on a fresh install, the versions older than
# it go (by version order); a downgrade keeps the newer one cached.
if [ -n "$installed" ]; then
    for old in "$servers"/v*; do
        v="${old##*/}"
        if [ "$v" != "$version" ] && [ "$(printf '%s\n' "$v" "$version" | sort -V | head -n 1)" = "$v" ]; then
            rm -rf "$old"
        fi
    done
fi

# Wildcard bind: Docker's port forward reaches the container's external
# interface, not its loopback. The advertised address is the host's loopback,
# where the session plan publishes the port (sessionplan.OrcaPort, which the
# build test holds equal to --port). Not exec'd: on_exit logs its end.
"$servers/current/bin/orca-ide" serve --port 6768 --bind 0.0.0.0 --pairing-address 127.0.0.1 --json
