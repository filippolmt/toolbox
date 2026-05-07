#!/usr/bin/env bash
set -euo pipefail

# Build Claude Code MCP plugins that ship src/ only — some marketplace
# plugins skip `npm install && npm run build` at install time, leaving the
# MCP server failing on "cannot find module dist/index.js".
#
# Idempotent via the per-plugin `.toolbox-built` marker. The marker lives
# inside the versioned plugin dir, so a plugin upgrade (new version path)
# naturally invalidates it and triggers a rebuild.
#
# Inner gates (node, plugins-cache dir) stay separate from the outer npm
# gate: node may exist without npm in some images, and the cache dir is
# absent on first boot before any plugin is installed.
command -v npm >/dev/null 2>&1 || exit 0

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
        # Build log lives next to the marker (same bind-mounted plugin dir)
        # so it survives container restarts. Removed on success.
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
