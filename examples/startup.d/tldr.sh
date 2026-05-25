#!/usr/bin/env bash
# Install `tldr` (community CLI cheatsheets — https://tldr.sh) inside the
# toolbox container.
#
# Usage:
#   cp examples/startup.d/tldr.sh ~/.toolbox/startup.d/tldr.sh
#
# Idempotent: skipped when `tldr` is already on PATH. Persisted via the
# `~/.toolbox/npm-global` mount, so the install survives container recreation.

set -eu

if command -v tldr >/dev/null 2>&1; then
    echo "    tldr: ready"
    exit 0
fi

echo "    tldr: installing..."
if npm install -g --silent tldr >/tmp/tldr-install.log 2>&1; then
    echo "    tldr: installed"
else
    echo "    tldr: install failed (see /tmp/tldr-install.log)"
    tail -5 /tmp/tldr-install.log | sed 's/^/      /'
    exit 1
fi
