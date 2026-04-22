#!/usr/bin/env bash
# Install and keep Get-Shit-Done (GSD) — https://github.com/gsd-build/get-shit-done
# up-to-date inside the toolbox container.
#
# Usage:
#   cp examples/startup.d/gsd.sh ~/.toolbox/startup.d/gsd.sh
#
# On every `toolbox shell` this script checks whether GSD needs (re)installing
# and does it idempotently. Reinstall is triggered by:
#   - missing GSD install dir under ~/.claude/get-shit-done
#   - missing `gsd-tools.cjs` runtime
#   - version drift between the VERSION file shipped by the package and the
#     pinned GSD_VERSION below
#
# Bump the pin here when you want a new version. Nothing else to do — the
# next startup will reconcile.

set -eu

GSD_VERSION="${GSD_VERSION:-1.38.3}"
INSTALL_DIR="$HOME/.claude/get-shit-done"
TOOLS_BIN="$INSTALL_DIR/bin/gsd-tools.cjs"
VERSION_FILE="$INSTALL_DIR/VERSION"

need_install=false
if [ ! -x "$TOOLS_BIN" ]; then
    need_install=true
elif [ ! -f "$VERSION_FILE" ] || [ "$(cat "$VERSION_FILE")" != "$GSD_VERSION" ]; then
    need_install=true
fi

if ! $need_install; then
    echo "    gsd: ready (v${GSD_VERSION})"
    exit 0
fi

echo "    gsd: installing v${GSD_VERSION}..."
if npx --yes "get-shit-done-cc@${GSD_VERSION}" --claude --global >/tmp/gsd-install.log 2>&1; then
    echo "    gsd: installed"
else
    echo "    gsd: install failed (see /tmp/gsd-install.log)"
    tail -5 /tmp/gsd-install.log | sed 's/^/      /'
    exit 1
fi
