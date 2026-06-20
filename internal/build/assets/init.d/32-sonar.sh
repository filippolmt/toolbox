#!/usr/bin/env bash
set -euo pipefail

# Per-repo opt-in, mirroring init.d/30-graphify.sh. When the workspace declares a
# SonarQube project (a sonar-project.properties marker in the root), install
# sonar's own project-scoped Claude integration: `sonar integrate claude` merges
# a `sonarqube` MCP server into .mcp.json and secrets-scanning hooks into
# .claude/ (settings.json + .claude/hooks/sonar-secrets/) — all
# version-controllable with the repo, nothing global. The project key is read
# from sonar-project.properties, so no --project flag is needed. Repos without
# the marker are left untouched.
#
# Re-run every shell, exactly like graphify, so the integration self-heals: a
# fresh clone, or a repo whose .mcp.json/.claude were cleaned, gets it
# re-applied; an already-integrated repo is a no-op. Verified idempotent and
# non-destructive — it MERGES (a pre-existing codegraph MCP server and existing
# hooks/permissions all survive), it never clobbers.
#
# Two deviations from graphify, both forced by how `sonar integrate` behaves:
#   - `--non-interactive`: without it the command opens an arrow-key TUI and
#     blocks forever waiting on input that never comes at shell start.
#   - Detached (setsid) + time-boxed (timeout): it validates the token against
#     the server (network), and the entrypoint `wait`s on every init.d script
#     before showing the prompt, so a synchronous run could block startup on a
#     slow or unreachable server. graphify's `install` is offline, so it can run
#     inline; this cannot.
#
# Outer gate: `sonar` binary present AND the project marker exists in cwd.
command -v sonar >/dev/null 2>&1 || exit 0
[ -f "$PWD/sonar-project.properties" ] || exit 0

# Inner gate: a Claude environment exists (the bind-mount auto-creates ~/.claude
# even when tools.claude=false).
command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ] || exit 0

# Auth pre-check, OFFLINE: a stored token keychain must exist. Integrating with
# no token is pointless and only burns a server round-trip. A cheap file test —
# no `sonar auth status` network call on the startup path. Path is the one the
# image pins via SONARQUBE_CLI_KEYCHAIN_FILE.
keychain="${SONARQUBE_CLI_KEYCHAIN_FILE:-$HOME/.sonar/sonarqube-cli/keychain.json}"
[ -s "$keychain" ] || exit 0

# Detached + time-boxed (see header). Output goes to a log in the persisted state
# mount, never to the shell. Non-fatal: a failed attempt simply re-runs next
# shell. Idempotent, so re-running detached every shell is safe.
log="$HOME/.sonar/.toolbox-integrate.log"
setsid timeout 120 sonar integrate claude --non-interactive </dev/null >"$log" 2>&1 &
