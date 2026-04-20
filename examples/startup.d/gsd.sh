#!/usr/bin/env bash
# Install and keep Get-Shit-Done (GSD) — https://github.com/gsd-build/get-shit-done
# up-to-date inside the toolbox container.
#
# Usage:
#   cp examples/startup.d/gsd.sh ~/.toolbox/startup.d/gsd.sh
#
# On every `toolbox shell` this script checks whether GSD needs (re)installing
# and does it idempotently. Reinstall is triggered by:
#   - missing `gsd-sdk` binary on PATH
#   - missing skill dir under ~/.claude/skills/get-shit-done
#   - version drift vs. the pinned GSD_VERSION below
#
# Bump the pin here when you want a new version. Nothing else to do — the
# next startup will reconcile.

set -eu

GSD_VERSION="${GSD_VERSION:-1.37.1}"
SENTINEL="$HOME/.toolbox-state/gsd.version"
SKILL_DIR="$HOME/.claude/skills/get-shit-done"

need_install=false
if ! command -v gsd-sdk >/dev/null 2>&1; then
    need_install=true
elif [ ! -d "$SKILL_DIR" ]; then
    need_install=true
elif [ ! -f "$SENTINEL" ] || [ "$(cat "$SENTINEL")" != "$GSD_VERSION" ]; then
    need_install=true
fi

if ! $need_install; then
    echo "    gsd: ready (v${GSD_VERSION})"
    exit 0
fi

echo "    gsd: installing v${GSD_VERSION}..."
if npx --yes "get-shit-done-cc@${GSD_VERSION}" --claude --global >/tmp/gsd-install.log 2>&1; then
    mkdir -p "$(dirname "$SENTINEL")"
    echo "$GSD_VERSION" > "$SENTINEL"
    echo "    gsd: installed"
else
    echo "    gsd: install failed (see /tmp/gsd-install.log)"
    tail -5 /tmp/gsd-install.log | sed 's/^/      /'
    exit 1
fi
