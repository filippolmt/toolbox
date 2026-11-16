#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=bin/agent-topology-lib.sh
. /usr/local/lib/toolbox/agent-topology-lib.sh

# Registers the image's agent guardrails as a Claude Code PreToolUse hook on
# Bash, on every start (Agent Wiring): Claude Code has no system layer a
# developer can override, and an image managed-settings file is ignored
# wherever an organisation sends server-managed settings (first-wins).
#
# The hook ($toolbox_guardrails_bin, built from guardrails/)
# blocks `git commit -a`, an implicit `git stash` and, while a Docker daemon is
# reachable, a bind mount whose source is container-local. Codex and pi get the
# same binary as Image Defaults (/etc/codex/hooks.json, the bin/pi wrapper).
#
# toolbox_merge_hook_groups keeps the entry where it is from one start to the
# next and replaces an earlier image's, so a rewrite is idempotent.
command -v jq >/dev/null 2>&1 || exit 0
toolbox_agent_available claude && toolbox_agent_persistent claude || exit 0

[ -x "$toolbox_guardrails_bin" ] || exit 0

_guardrails_wire() {
    toolbox_merge_guardrails "$(toolbox_agent_home claude)/settings.json"
}

toolbox_with_claude_settings_lock _guardrails_wire || \
    echo "toolbox: agent guardrails not wired for claude (non-fatal — settings.json unreadable or not writable)"
