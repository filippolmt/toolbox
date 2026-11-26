#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=../bin/agent-topology-lib.sh source-path=SCRIPTDIR
. /usr/local/lib/toolbox/agent-topology-lib.sh

# gh startup integration, gated on `command -v gh` so an INSTALL_GH=false
# image exits silently. Reports credential state and installs gh's official
# agent skill: the copy the Dockerfile baked from the cli/cli tag of the bundled
# gh (toolbox monitoring guidance appended), synced into every active skill
# root only where it differs — no network, no rewrite on every start.
#
# Credential outcomes (decision D-08-creds-tristate):
#   configured        — `gh auth status` exits 0.
#   auth check failed — token stored but verification fails (expired /
#                       revoked / network unreachable on first call).
#                       Detected by: a token IS present (`gh auth token`
#                       succeeds) but `gh auth status` does not — the user
#                       believes they're authenticated and would otherwise
#                       hit an opaque error later in the shell.
#   not configured    — no token at all.
command -v gh >/dev/null 2>&1 || exit 0

if gh auth status >/dev/null 2>&1; then
    echo "  gh: configured"
elif gh auth token >/dev/null 2>&1; then
    echo "  gh: auth check failed (try \`gh auth refresh\` or \`gh auth login\`)"
else
    echo "  gh: not configured"
fi

toolbox_sync_skill gh || true
