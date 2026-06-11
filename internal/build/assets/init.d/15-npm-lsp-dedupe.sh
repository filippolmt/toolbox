#!/usr/bin/env bash
set -u

# Heal LSP version drift caused by stale npm-global volume duplicates.
#
# pyright / typescript / typescript-language-server are baked into the image
# under /usr/local (Dockerfile-pinned, Renovate-bumped). PATH puts
# ~/.npm-global/bin ahead of /usr/local/bin so the baked claude-code/codex can
# self-update and keep winning — but a volume seeded in the pre-baking era can
# carry stale copies of these LSP packages, which then shadow the image pins
# (observed: runtime pyright 1.1.409 vs image 1.1.410). The bump never reaches
# the user.
#
# Fix: on every start, remove ONLY the pinned LSP set from the volume when
# present, letting the baked /usr/local versions surface. Scoped to the three
# LSP plugins the image owns — never touches self-updating tools. Idempotent,
# offline, non-fatal (init.d runs each script under `if ! bash`, neutralising
# set -e); after the first heal the existence test short-circuits before npm.

_prefix="${NPM_CONFIG_PREFIX:-$HOME/.npm-global}"
for pkg in pyright typescript typescript-language-server; do
    if [ -e "$_prefix/lib/node_modules/$pkg" ]; then
        npm rm -g "$pkg" >/dev/null 2>&1 || true
    fi
done
