#!/usr/bin/env bash
set -euo pipefail

# Outer self-gate (D-04): npm is the script's primary owner. node and the
# plugins-cache directory stay as inner gates per Pitfall 5 — node may exist
# without npm in some images, and the cache dir is absent on first boot
# before any Claude Code plugin is installed.
command -v npm >/dev/null 2>&1 || exit 0

# Built-in: build Claude Code MCP plugins shipped with src/ only.
# Some marketplace plugins ship without node_modules/ or dist/, and the
# installer does not run `npm install && npm run build`. The MCP server
# then fails to start ("cannot find module dist/index.js"). This block is
# idempotent via a marker file written after a successful build.
#
# Sentinel: skip entirely when the plugins cache does not exist — a user
# who has never started Claude Code should not pay any cost here.
#
# Per-plugin decision (only plugins with a `mcp/package.json`):
#   - no `scripts.build` in package.json → nothing to build, skip
#   - marker `.toolbox-built` present    → already built, skip
#   - otherwise → npm install + npm run build, then write marker
# Marker lives inside the versioned plugin dir, so a plugin upgrade
# (new version path) naturally invalidates it and triggers a rebuild.
_plugins_cache="$HOME/.claude/plugins/cache"
if [ -d "$_plugins_cache" ] && command -v node >/dev/null 2>&1; then
    shopt -s nullglob
    _mcp_dirs=( "$_plugins_cache"/*/*/*/mcp )
    shopt -u nullglob
    _header_printed=0
    for _mcp_dir in "${_mcp_dirs[@]}"; do
        [ -d "$_mcp_dir" ] || continue
        [ -f "$_mcp_dir/package.json" ] || continue
        [ -f "$_mcp_dir/.toolbox-built" ] && continue

        # Skip plugins that declare no build step.
        _has_build=$(node -e '
            try {
                const p = require(process.argv[1] + "/package.json");
                process.stdout.write(p.scripts && p.scripts.build ? "1" : "");
            } catch (e) {}
        ' "$_mcp_dir" 2>/dev/null) || _has_build=""
        [ -z "$_has_build" ] && continue

        if [ "$_header_printed" -eq 0 ]; then
            echo ""
            echo "Building Claude Code MCP plugins:"
            _header_printed=1
        fi
        echo "  $(basename "$(dirname "$(dirname "$_mcp_dir")")")"
        # Capture stderr to a per-plugin log so a failed build leaves
        # actionable diagnostics behind. The log lives next to the
        # `.toolbox-built` marker (same bind-mounted plugin dir), so it
        # survives container restarts and the user can inspect it from any
        # later shell. Removed on success to keep the dir tidy.
        _build_log="$_mcp_dir/.toolbox-build-error.log"
        rm -f "$_build_log"
        if (
            cd "$_mcp_dir"
            npm install --silent --no-audit --no-fund >/dev/null 2>>"$_build_log" &&
            npm run build --silent >/dev/null 2>>"$_build_log"
        ); then
            touch "$_mcp_dir/.toolbox-built"
            rm -f "$_build_log"
        else
            echo "    build failed (log: $_build_log)"
            if [ -s "$_build_log" ]; then
                tail -n 5 "$_build_log" | sed 's/^/      /'
            fi
        fi
    done
fi
