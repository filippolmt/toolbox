#!/usr/bin/env bash
set -euo pipefail

# Seed Codex telemetry-off block in ~/.codex/config.toml. Codex CLI defaults
# otel.metrics_exporter to "statsig" (third-party analytics).
#
# Idempotent: sentinel comment marks the block. File absent → create. File
# present without sentinel → append. Sentinel present → no-op. User edits
# elsewhere in the file are preserved; deleting the sentinel block re-adds it.
command -v codex >/dev/null 2>&1 || exit 0
_codex_home="${CODEX_HOME:-$HOME/.codex}"
[ -d "$_codex_home" ] || exit 0

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
