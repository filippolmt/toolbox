#!/usr/bin/env bash
# Checks that a Codex hooks file carries each of the image's hooks on its own
# event: Worktrunk's markers on the events in _wt_codex_events, Atuin's on the
# three tool-use events, Herdr's on SessionStart, with Herdr's script beside
# the file. The Dockerfile's gen-codex-config stage runs it on the file it
# builds, the smoke test on the one the image ships. Needs jq.
set -euo pipefail

# shellcheck source=bin/worktrunk-markers-lib.sh
. "$(dirname "$0")/worktrunk-markers-lib.sh"

file=${1:-/etc/codex/hooks.json}
dir=$(dirname "$file")
if [ ! -r "$dir/herdr-agent-state.sh" ]; then
    echo "codex hooks: $dir/herdr-agent-state.sh missing or unreadable" >&2
    exit 1
fi
# shellcheck disable=SC2016 # jq program, not shell
if ! jq -e --arg marker_re "$_wt_marker_re" --argjson wt "$_wt_codex_events" \
    --arg herdr "bash '$dir/herdr-agent-state.sh' session" '
    . as $doc
    | def on($e): [$doc.hooks[$e][]?.hooks[]?.command | strings];
      all($wt[].event; any(on(.)[]; test($marker_re)))
      and all("PreToolUse", "PostToolUse", "PostToolUseFailure";
          on(.) | index("/usr/local/bin/atuin hook codex") != null)
      and (on("SessionStart") | index($herdr) != null)' "$file" >/dev/null; then
    echo "codex hooks: $file is missing an image hook on its event" >&2
    exit 1
fi
