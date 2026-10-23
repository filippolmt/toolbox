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

    # The upstream install refreshes container-local skills on every shell.
    local skill="$dir/gh/SKILL.md"
    if [[ -f "$skill" ]] && ! grep -q '^<!-- toolbox-gh-monitoring -->$' "$skill"; then
        if ! cat >> "$skill" <<'MONITORING'

<!-- toolbox-gh-monitoring -->
## Watching checks (Toolbox)

Use compact output and propagate the run's exit status:

```bash
gh run watch "$run_id" --repo "$repo" --compact --exit-status
```

For a long watch, redirect output to a private log (`umask 077`) and report only
state transitions or the final result, rather than streaming every refresh into
the agent context. On failure, use `gh run view "$run_id" --repo "$repo" --log-failed`
and quote only the relevant, redacted failure lines. Verify that the checked
run's `headSha` matches the pushed commit before declaring CI green.
MONITORING
        then
            echo "toolbox: gh monitoring guidance ($label) could not be written (non-fatal)" >&2
        fi
    fi
}

toolbox_for_each_active_skill_root _install_skill
