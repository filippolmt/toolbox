#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=bin/agent-topology-lib.sh
. /usr/local/lib/toolbox/agent-topology-lib.sh

# Claude Code guardrails, on every container start: three PreToolUse hooks on
# Bash, all run by the image's /usr/local/lib/toolbox/claude-guardrail, which
# block a command that would reach past the agent's own work:
#
#   docker-mount    a docker bind source that exists only in this container,
#                   which the host daemon (Docker-outside-of-Docker) resolves
#                   on the host instead;
#   git-commit-all  git commit -a / --all;
#   git-stash       a git stash with no pathspec.
#
# They go into the developer's settings.json, not a managed-settings file:
# under Claude Code's default managedSourcesBehavior ("first-wins") an
# organisation's server-managed settings would make it ignore an image-baked
# one. Every guard command starts with the script's path, which is what makes
# the image's entries identifiable: toolbox_merge_hooks replaces them in place,
# drops a guard a later image retired and never touches another hook.
#
# Non-fatal: a missing guard costs a guardrail, not a shell.
toolbox_agent_available claude && toolbox_agent_persistent claude || exit 0

_guardrail=/usr/local/lib/toolbox/claude-guardrail

_guardrails_wire() {
    local add
    add=$(jq -n --arg bin "$_guardrail" '[{event: "PreToolUse", group: {matcher: "Bash",
        hooks: (["docker-mount", "git-commit-all", "git-stash"]
            | map({type: "command", command: "\($bin) \(.)"}))}}]') || return 1
    toolbox_merge_hooks "$(toolbox_agent_home claude)/settings.json" "^$_guardrail " "$add"
}

toolbox_with_claude_settings_lock _guardrails_wire || \
    echo "toolbox: claude guardrail hooks not wired (non-fatal — settings.json unreadable or not writable)"
