#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=../bin/agent-topology-lib.sh source-path=SCRIPTDIR
. /usr/local/lib/toolbox/agent-topology-lib.sh

# Claude Code's share of the agent guardrails, wired on every start as Agent
# Wiring; what they block, why this route and how the other agents get them:
# docs/internals/image-build.md#agent-guardrails.
command -v jq >/dev/null 2>&1 || exit 0
toolbox_agent_available claude && toolbox_agent_persistent claude || exit 0

[ -x "$toolbox_guardrails_bin" ] || exit 0

_guardrails_wire() {
    toolbox_merge_guardrails "$(toolbox_agent_home claude)/settings.json"
}

toolbox_with_claude_settings_lock _guardrails_wire || \
    echo "toolbox: agent guardrails not wired for claude (non-fatal — settings.json unreadable or not writable)"
