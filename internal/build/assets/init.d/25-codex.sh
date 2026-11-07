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
# Earlier images wrote the same hooks into ~/.codex/hooks.json, so they would
# now run twice: drop exactly those entries, then Herdr's script they called.
# Every other hook, the developer's own, stays.
#
# Codex keys a hook's trust to its group's position in the event, so a group
# this empties stays behind as {"hooks": []} while a later group still holds a
# hook: removing it would shift that hook and silently revoke its trust. Only
# trailing emptied groups go. The file is rewritten only when it holds an image
# hook, and one jq cannot parse is left alone, Herdr's script with it.
# shellcheck disable=SC2016 # jq program, not shell
_codex_drop_jq='
def image: (.command? // "") as $c
  | $c == "/usr/local/bin/atuin hook codex"
    or ($c | test("^bash \u0027.*/herdr-agent-state\\.sh\u0027 session$"))
    or ($c | test($marker_re));
def handlers: (.hooks | type) == "array";
def emptied: handlers and (.hooks | length) == 0;
def trim: ([range(length) as $i | select(.[$i] | emptied | not) | $i] | last) as $l
  | if $l == null then [] else .[:$l + 1] end;
if [.hooks[]?[]? | select(handlers) | .hooks[] | select(image)] == [] then empty
else .hooks |= (map_values(if type == "array"
      then map(if handlers then .hooks |= map(select(image | not)) else . end) | trim
      else . end)
    | with_entries(select((.value | type) != "array" or (.value | length) > 0)))
end'

_codex_drop_image_hooks() {
    local file="$_codex_home/hooks.json" tmp
    if [ -s "$file" ]; then
        tmp=$(mktemp "$file.XXXXXX") || return 1
        if ! jq --arg marker_re "$_wt_marker_re" "$_codex_drop_jq" "$file" >"$tmp" 2>/dev/null; then
            rm -f "$tmp"
            return 1
        fi
        if [ -s "$tmp" ]; then
            chmod --reference="$file" "$tmp" 2>/dev/null
            mv -f "$tmp" "$file" || { rm -f "$tmp"; return 1; }
        else
            rm -f "$tmp"
        fi
    fi
    rm -f "$_codex_home/herdr-agent-state.sh"
}

toolbox_with_codex_hooks_lock _codex_drop_image_hooks || \
    echo "toolbox: earlier image hooks not removed from codex hooks.json (non-fatal — unreadable or not writable)"
