# Runtime privacy internals

Maintainer notes on the telemetry and data-exfiltration controls baked into the image, including the Claude Code env-var matrix (what is set, what is deliberately not, and why).

## Claude Code env-var matrix

The image sets:

- `DISABLE_AUTOUPDATER=1` — block background CLI self-update (`/usr/local/lib/node_modules` is root-only).
- `FORCE_AUTOUPDATE_PLUGINS=1` — documented escape hatch from the [discover-plugins guide](https://code.claude.com/docs/en/discover-plugins#configure-auto-updates) that keeps plugin updates running even when the CLI auto-updater is disabled.
- `DISABLE_FEEDBACK_COMMAND=1`, `DISABLE_ERROR_REPORTING=1` — privacy (`/bug` ships conversation data to Anthropic; error reporting ships stack traces).
- `USE_BUILTIN_RIPGREP=0` — use the apt-pinned system `rg` (base layer) instead of the npm-bundled binary.
- `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1` — return to the project dir after every Bash call; prevents cd-drift across tool invocations.
- `BASH_DEFAULT_TIMEOUT_MS=300000`, `BASH_MAX_TIMEOUT_MS=1200000` — 5m default / 20m ceiling (upstream 2m/10m); in-container image builds and test suites routinely exceed the upstream default.
- `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` — opt into agent teams (experimental upstream; in-process mode needs no tmux).

Host-side (not baked — set per-shell by `sessionplan.shellEnv`): `CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=<workspace basename>`. The upstream default is the machine hostname, which inside the container is the container name (`SessionPlan.Hostname`) — every Remote Control session on claude.ai web/mobile would be named after the container rather than the workspace, carrying the `toolbox-` prefix and, on a workspace session, the path hash.

`DISABLE_TELEMETRY` is intentionally NOT set (dropped 2026-06, was part of the original lockdown): the Statsig channel doubles as feature-flag delivery, and blocking it is reported to break Remote Control entitlements and preview-feature rollouts ([anthropics/claude-code#58383](https://github.com/anthropics/claude-code/issues/58383) — community-reported, not formally documented; re-verify before re-adding the flag). It carries product analytics, not training data: the model-improvement opt-out is the claude.ai account-level privacy setting ("Help improve Claude"), and admin usage analytics ([claude.ai/analytics](https://code.claude.com/docs/en/analytics)) are collected server-side regardless of any client flag.

The `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` umbrella flag is likewise NOT set: baking it into the image was correlated with intermittent OAuth re-login prompts on long-lived shells, suggesting the umbrella gates undocumented behaviour beyond its stated sub-flags.

Remote Control (`claude --remote-control`, pairs the session with claude.ai web/mobile) is outbound-HTTPS-only — no inbound ports, so it works inside the container without `-p` bindings or the loopback bridge. The `/config` toggle "Enable Remote Control for all sessions" persists in the `~/.claude` bind and survives container recreates.

When a plugin is refreshed Claude Code prompts for `/reload-plugins`. Bumping the CLI itself is a Dockerfile concern: `CLAUDE_CODE_VERSION` + image rebuild (Renovate-driven).
