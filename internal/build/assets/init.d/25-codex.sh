#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=bin/agent-topology-lib.sh
. /usr/local/lib/toolbox/agent-topology-lib.sh

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
# now run twice, once trusted: drop exactly those entries, and Herdr's script
# they called. Every other hook, the developer's own, stays. The file is
# rewritten only when it holds one, and a file jq cannot parse is left alone.
# shellcheck disable=SC2016 # jq program, not shell
_codex_drop_jq='
def image: (.command? // "") as $c
  | $c == "/usr/local/bin/atuin hook codex"
    or $c == $herdr
    or ($c | test("^/usr/local/bin/wt .*config state marker "));
if [.hooks[]?[]?.hooks[]? | select(image)] == [] then empty
else .hooks |= (map_values(map(.hooks |= map(select(image | not)) | select((.hooks | length) > 0)))
  | with_entries(select((.value | length) > 0)))
end'

_codex_drop_image_hooks() {
    local file="$_codex_home/hooks.json" tmp
    rm -f "$_codex_home/herdr-agent-state.sh"
    [ -s "$file" ] || return 0
    tmp=$(mktemp "$file.XXXXXX") || return 1
    if ! jq --arg herdr "bash '$_codex_home/herdr-agent-state.sh' session" \
        "$_codex_drop_jq" "$file" >"$tmp" 2>/dev/null; then
        rm -f "$tmp"
        return 1
    fi
    if [ ! -s "$tmp" ]; then
        rm -f "$tmp"
        return 0
    fi
    chmod --reference="$file" "$tmp" 2>/dev/null
    mv -f "$tmp" "$file"
}

toolbox_with_codex_hooks_lock _codex_drop_image_hooks || \
    echo "toolbox: earlier image hooks not removed from codex hooks.json (non-fatal — unreadable or not writable)"
