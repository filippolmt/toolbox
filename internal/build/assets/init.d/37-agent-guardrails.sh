#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=bin/agent-topology-lib.sh
. /usr/local/lib/toolbox/agent-topology-lib.sh

# Registers the image's agent guardrails as a Claude Code PreToolUse hook on
# Bash, on every start (Agent Wiring): Claude Code has no system layer a
# developer can override, and an image managed-settings file is ignored
# wherever an organisation sends server-managed settings (first-wins).
#
# The hook (/usr/local/lib/toolbox/agent-guardrails, built from guardrails/)
# blocks `git commit -a` and an implicit `git stash`, and with --docker a bind
# mount whose source is container-local. --docker is passed only while the
# Docker socket is mounted: Docker access crosses the Containment Boundary.
#
# The merge drops every earlier guardrails entry before appending the current
# one, so a rewrite is idempotent and a changed command replaces, not stacks.
# shortcut: the entry is re-appended at the end of PreToolUse, so it moves
# behind hooks added after it; keep its place like 62-worktrunk.sh does if the
# order ever matters.
command -v jq >/dev/null 2>&1 || exit 0
toolbox_agent_available claude && toolbox_agent_persistent claude || exit 0

_guardrails_bin=/usr/local/lib/toolbox/agent-guardrails
_guardrails_cmd=$_guardrails_bin
[ -S /var/run/docker.sock ] && _guardrails_cmd="$_guardrails_bin --docker"

# shellcheck disable=SC2016 # jq program, not shell
_guardrails_jq='
def ours: ((.command? // "") | startswith($bin));
.hooks.PreToolUse = (
  ((.hooks.PreToolUse // [])
    | map(if any(.hooks[]?; ours)
          then (.hooks |= map(select(ours | not))) | select((.hooks | length) > 0)
          else . end))
  + [{matcher: "Bash", hooks: [{type: "command", command: $cmd}]}])'

_guardrails_wire() {
    toolbox_rewrite_json "$(toolbox_agent_home claude)/settings.json" "$_guardrails_jq" \
        --arg bin "$_guardrails_bin" --arg cmd "$_guardrails_cmd"
}

toolbox_with_claude_settings_lock _guardrails_wire || \
    echo "toolbox: agent guardrails not wired for claude (non-fatal — settings.json unreadable or not writable)"
