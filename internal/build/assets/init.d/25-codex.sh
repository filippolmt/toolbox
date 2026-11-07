#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=bin/agent-topology-lib.sh
. /usr/local/lib/toolbox/agent-topology-lib.sh
# shellcheck source=bin/worktrunk-markers-lib.sh
. /usr/local/lib/toolbox/worktrunk-markers-lib.sh

# Seed Codex telemetry-off block in ~/.codex/config.toml. Codex CLI defaults
# otel.metrics_exporter to "statsig" (third-party analytics).
#
# Idempotent: sentinel comment marks the block. File absent → create. File
# present without sentinel → append. Sentinel present → no-op. User edits
# elsewhere in the file are preserved; deleting the sentinel block re-adds it.
toolbox_agent_available codex || exit 0
_codex_home=$(toolbox_agent_home codex)
toolbox_agent_persistent codex || exit 0

_codex_config="$_codex_home/config.toml"
_codex_sentinel='# toolbox:codex:telemetry-off'
_codex_block='# toolbox:codex:telemetry-off
[otel]
exporter = "none"
metrics_exporter = "none"
trace_exporter = "none"'

if [ ! -f "$_codex_config" ]; then
    printf '%s\n' "$_codex_block" > "$_codex_config"
elif ! grep -qF "$_codex_sentinel" "$_codex_config"; then
    printf '\n%s\n' "$_codex_block" >> "$_codex_config"
fi

# The image's Codex hooks (Worktrunk markers, Herdr, Atuin) ship in the system
# layer, /etc/codex/hooks.json, where Codex runs them with no per-user trust.
# Earlier images wrote the same hooks, and RTK's, into ~/.codex/hooks.json:
# drop exactly those, then Herdr's script they called. Every other hook, the
# developer's own, stays at its position, which Codex keys its trust to: a
# group this empties in front of another stays as {"hooks": []} (Agent Wiring
# in CONTEXT.md). An event the cleanup empties goes; a file with no image hook
# is not rewritten; one jq cannot parse is left alone, Herdr's script with it.
# One case still moves a hook: a developer's handler after an image handler in
# the same group. No placeholder handler helps — an untrusted one prompts for
# trust too, and one Codex cannot parse makes it drop the whole file.
# shellcheck disable=SC2016 # jq program, not shell
_codex_drop_jq='
def image: (.command? // "") as $c | ($c | type) == "string" and (
  $c == "/usr/local/bin/atuin hook codex"
  or ($c | test("^bash \u0027.*/herdr-agent-state\\.sh\u0027 session$"))
  or ($c | test("(^|[ /])rtk hook codex$|rtk-rewrite\\.(sh|json)$"))
  or ($c | test($marker_re)));
def group: type == "object" and (.hooks | type) == "array";
def holds_image: group and any(.hooks[]; image);
def drop: if holds_image
  then .hooks |= map(select(image | not)) | if .hooks == [] then .toolbox_emptied = true else . end
  else . end;
def emptied: type == "object" and .toolbox_emptied == true;
def trim: ([range(length) as $i | select(.[$i] | emptied | not) | $i] | last) as $l
  | (if $l == null then [] else .[:$l + 1] end)
  | map(if type == "object" then del(.toolbox_emptied) else . end);
if [.hooks? | objects | .[] | arrays | .[] | select(holds_image)] == [] then empty
else .hooks |= with_entries(
  if (.value | type) == "array" and any(.value[]; holds_image)
  then (.value |= (map(drop) | trim)) | select(.value != [])
  else . end)
end'

_codex_drop_image_hooks() {
    toolbox_rewrite_json "$_codex_home/hooks.json" "$_codex_drop_jq" \
        --arg marker_re "$_wt_marker_re" || return 1
    rm -f "$_codex_home/herdr-agent-state.sh"
}

toolbox_with_codex_hooks_lock _codex_drop_image_hooks || \
    echo "toolbox: earlier image hooks not removed from codex hooks.json (non-fatal — unreadable or not writable)"
