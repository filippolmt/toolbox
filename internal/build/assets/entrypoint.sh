#!/usr/bin/env bash
set -euo pipefail

# Inject a passwd/group entry for the runtime UID/GID when missing.
# The container runs with --user <host-uid>:<host-gid>, which rarely matches
# the baked "toolbox" user (uid 1000). Tools that call getpwuid() — notably
# ssh — abort with "No user exists for uid NNN" otherwise, breaking git over
# ssh://. /etc/passwd and /etc/group are chmod 0666 in the Dockerfile so this
# append works without root.
_uid=$(id -u)
_gid=$(id -g)
if ! getent passwd "${_uid}" >/dev/null 2>&1; then
    echo "toolbox:x:${_uid}:${_gid}:toolbox:/home/toolbox:/bin/bash" >> /etc/passwd
fi
if ! getent group "${_gid}" >/dev/null 2>&1; then
    echo "toolbox:x:${_gid}:" >> /etc/group
fi
unset _uid _gid

echo "Toolbox credential check:"

# gh (GitHub CLI)
if gh auth status >/dev/null 2>&1; then
    echo "  gh: configured"
else
    echo "  gh: not configured"
fi

# glab (GitLab CLI)
if glab auth status >/dev/null 2>&1; then
    echo "  glab: configured"
else
    echo "  glab: not configured"
fi

# Conditional checks for cloud CLIs (may be volume-mounted from host)
if command -v gcloud >/dev/null 2>&1; then
    if gcloud auth list --filter=status:ACTIVE --format='value(account)' 2>/dev/null | grep -q .; then
        echo "  gcloud: configured"
    else
        echo "  gcloud: not configured"
    fi
fi

if command -v az >/dev/null 2>&1; then
    if az account show >/dev/null 2>&1; then
        echo "  az: configured"
    else
        echo "  az: not configured"
    fi
fi

if command -v oci >/dev/null 2>&1; then
    # </dev/null: oci prompts "Do you want to create a new config file? [Y/n]"
    # when ~/.oci/config is missing. Without a closed stdin it would block the
    # entrypoint on the container's TTY and never reach the startup hooks.
    if oci iam region list --output table </dev/null >/dev/null 2>&1; then
        echo "  oci: configured"
    else
        echo "  oci: not configured"
    fi
fi

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
if [ -d "$_plugins_cache" ] && command -v npm >/dev/null 2>&1 && command -v node >/dev/null 2>&1; then
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
        if (
            cd "$_mcp_dir"
            npm install --silent --no-audit --no-fund >/dev/null 2>&1 &&
            npm run build --silent >/dev/null 2>&1
        ); then
            touch "$_mcp_dir/.toolbox-built"
        else
            echo "    build failed"
        fi
    done
    unset _mcp_dirs _mcp_dir _header_printed _has_build
fi
unset _plugins_cache

# User-defined startup hooks from ~/.toolbox/startup.d/ on the host.
# Each *.sh file runs sequentially before the shell starts. Failures are
# logged but never abort the entrypoint — one bad hook cannot block access.
if [ -d "$HOME/.toolbox-startup.d" ]; then
    shopt -s nullglob
    hooks=( "$HOME/.toolbox-startup.d"/*.sh )
    shopt -u nullglob
    if [ ${#hooks[@]} -gt 0 ]; then
        echo ""
        echo "Toolbox startup hooks:"
        for hook in "${hooks[@]}"; do
            [ -r "$hook" ] || continue
            echo "  $(basename "$hook"):"
            bash "$hook" || echo "  $(basename "$hook"): failed (exit $?)"
        done
    fi
fi

exec "$@"
