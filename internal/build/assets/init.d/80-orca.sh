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
# everything it and the server print to $ORCA_USER_DATA_PATH/serve.log, which
# the host reads the pairing link from through the state mount.
#
# Layout under $ORCA_USER_DATA_PATH/server: one directory per version,
# extracted beside its final name and renamed into place only after the
# archive matched its line in the release's checksum file, and a `current`
# link switched atomically after that. A failed download or a mismatch leaves
# `current` where it was. No restart of a crashed server: tini (PID 1) keeps
# the container up, and the log says why the server stopped.

[ -n "${TOOLBOX_ORCA_VERSION:-}" ] || exit 0

data="${ORCA_USER_DATA_PATH:?ORCA_USER_DATA_PATH unset in an Orca Shell}"
log="$data/serve.log"

if [ "${1:-}" != "--foreground" ]; then
    mkdir -p "$data"
    setsid nohup bash "$0" --foreground >/dev/null 2>&1 </dev/null &
    disown 2>/dev/null || true
    echo "  orca: starting server ${TOOLBOX_ORCA_VERSION} (log: $log)"
    exit 0
fi

mkdir -p "$data"
exec >>"$log" 2>&1 </dev/null
echo "toolbox: orca ${TOOLBOX_ORCA_VERSION}: start $(date -u +%Y-%m-%dT%H:%M:%SZ)"

version="$TOOLBOX_ORCA_VERSION"
# The tag lands in a URL and a path: the host validated it, this re-checks.
if ! [[ "$version" =~ ^v[0-9]+(\.[0-9]+)*$ ]]; then
    echo "toolbox: orca: refusing malformed version '$version'"
    exit 1
fi

case "$(uname -m)" in
x86_64 | amd64) arch=x64 ;;
aarch64 | arm64) arch=arm64 ;;
*)
    echo "toolbox: orca: no server build for architecture $(uname -m)"
    exit 1
    ;;
esac

servers="$data/server"
dest="$servers/$version"
mkdir -p "$servers"

if [ ! -x "$dest/bin/orca-ide" ]; then
    archive="orca-server-linux-$arch.tar.gz"
    base="https://github.com/stablyai/orca/releases/download/$version"
    # Beside the destination, so the final rename stays on one filesystem.
    tmp=$(mktemp -d "$servers/.install-$version.XXXXXX")
    trap 'rm -rf "$tmp"' EXIT
    echo "toolbox: orca: downloading $base/$archive"
    curl -fsSL --retry 3 -o "$tmp/$archive" "$base/$archive"
    curl -fsSL --retry 3 -o "$tmp/sums" "$base/orca-server-sha256sums.txt"
    want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/sums")
    if [ -z "$want" ]; then
        echo "toolbox: orca: checksum file has no line for $archive; not installing"
        exit 1
    fi
    got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
    if [ "$got" != "$want" ]; then
        echo "toolbox: orca: checksum mismatch for $archive (got $got, want $want); not installing"
        exit 1
    fi
    mkdir "$tmp/tree"
    tar -xzf "$tmp/$archive" -C "$tmp/tree" --strip-components=1
    rm -rf "$dest"
    mv "$tmp/tree" "$dest"
    rm -rf "$tmp"
    trap - EXIT
fi

ln -sfn "$version" "$servers/.current.new"
mv -T "$servers/.current.new" "$servers/current"

# Wildcard bind: Docker's port forward reaches the container's external
# interface, not its loopback. The advertised address is the host's loopback,
# where the session plan publishes the port.
exec "$servers/current/bin/orca-ide" serve --port 6768 --bind 0.0.0.0 --pairing-address 127.0.0.1 --json
