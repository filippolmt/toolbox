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
#   - missing `gsd-sdk` binary on PATH (workflows shell out to `gsd-sdk query …`;
#     the installer builds `@gsd-build/sdk` from source into ~/.npm-global)
#   - version drift between the VERSION file shipped by the package and the
#     pinned GSD_VERSION below
#
# Bump the pin here when you want a new version. Nothing else to do — the
# next startup will reconcile.

set -eu

GSD_VERSION="${GSD_VERSION:-1.38.5}"
INSTALL_DIR="$HOME/.claude/get-shit-done"
TOOLS_BIN="$INSTALL_DIR/bin/gsd-tools.cjs"
VERSION_FILE="$INSTALL_DIR/VERSION"

need_install=false
if [ ! -x "$TOOLS_BIN" ]; then
    need_install=true
elif ! command -v gsd-sdk >/dev/null 2>&1; then
    need_install=true
elif [ ! -f "$VERSION_FILE" ] || [ "$(cat "$VERSION_FILE")" != "$GSD_VERSION" ]; then
    need_install=true
fi

if ! $need_install; then
    echo "    gsd: ready (v${GSD_VERSION})"
    exit 0
fi

echo "    gsd: installing v${GSD_VERSION}..."
# Install globally instead of `npx --yes`. The upstream installer creates a
# symlink `@gsd-build/sdk` → its own `sdk/` directory; under `npx` that path
# lives in `~/.npm/_npx/<hash>/...` and is reaped on the next npx run with
# different deps, leaving `gsd-sdk` dangling. `npm install -g` puts the
# package at a stable path under `~/.npm-global/lib/node_modules/`, so the
# symlink survives across shells.
if npm install -g --silent "get-shit-done-cc@${GSD_VERSION}" >/tmp/gsd-install.log 2>&1 \
    && get-shit-done-cc --claude --global >>/tmp/gsd-install.log 2>&1; then
    echo "    gsd: installed"
else
    echo "    gsd: install failed (see /tmp/gsd-install.log)"
    tail -5 /tmp/gsd-install.log | sed 's/^/      /'
    exit 1
fi

# Upstream installer (get-shit-done-cc <=1.38.5) forgets to chmod +x the
# resolved gsd-sdk target, so the symlink exists but execution yields
# "permission denied". Ensure the real file is executable.
if gsd_sdk_target="$(readlink -f "$(command -v gsd-sdk 2>/dev/null)" 2>/dev/null)" \
    && [ -n "$gsd_sdk_target" ] && [ -f "$gsd_sdk_target" ] && [ ! -x "$gsd_sdk_target" ]; then
    chmod +x "$gsd_sdk_target"
fi
