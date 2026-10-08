#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=bin/agent-topology-lib.sh
. /usr/local/lib/toolbox/agent-topology-lib.sh

# gh startup integration, gated on `command -v gh` so an INSTALL_GH=false
# image exits silently. Reports credential state and installs gh's official
# agent skill non-fatally. Claude gets its own config root; Codex and pi share
# the portable ~/.agents/skills root.
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

_install_skill() {
    local label="$1" dir="$2"
    gh skill install cli/cli gh --dir "$dir" --force >/dev/null 2>&1 || \
        echo "toolbox: gh skill install ($label) failed (non-fatal — retry: \`gh skill install cli/cli gh --dir $dir --force\`)"
}

toolbox_for_each_active_skill_root _install_skill
