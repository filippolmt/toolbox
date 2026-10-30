# Runtime privacy internals

Maintainer notes on the telemetry and data-exfiltration controls baked into the image, including the Claude Code env-var matrix (what is set, what is deliberately not, and why).

## Claude Code env-var matrix

The image sets:

- `DISABLE_UPDATES=1` — block every CLI update, background and manual (`claude update` prints a refusal instead of failing on the root-only `/usr/local/lib/node_modules`). Upstream's switch for a CLI distributed through its own channel; Renovate drives the version.
- `FORCE_AUTOUPDATE_PLUGINS=1` — documented escape hatch from the [discover-plugins guide](https://code.claude.com/docs/en/discover-plugins#configure-auto-updates) that keeps plugin updates running even when CLI updates are disabled; the CLI checks it before `DISABLE_UPDATES`.
- `DISABLE_FEEDBACK_COMMAND=1`, `DISABLE_ERROR_REPORTING=1`, `CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1` — privacy (`/feedback`, `/bug` and `/share` ship conversation data to Anthropic; error reporting ships stack traces; the session survey's follow-up can upload the transcript).
- `CLAUDE_CONFIG_DIR=/home/toolbox/.claude` — keeps `.credentials.json` and `.claude.json` in the `~/.claude` bind so OAuth survives container recreates.
- `USE_BUILTIN_RIPGREP=0` — use the apt-pinned system `rg` (base layer) instead of the npm-bundled binary.
- `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1` — return to the project dir after every Bash call in the main session; prevents cd-drift across tool invocations.
- `BASH_DEFAULT_TIMEOUT_MS`, `BASH_MAX_TIMEOUT_MS` — the default and the ceiling for a foreground Bash call, both raised above upstream's defaults: in-container image builds and test suites routinely outlast them.
- `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` — opt into agent teams (experimental upstream; the default in-process mode needs no tmux). A subagent Claude names launches as a teammate, so a team can form unasked.
- `CLAUDE_CODE_NEW_INIT=1` — `/init` asks which files to generate (CLAUDE.md, skills, hooks) before exploring and writing them.
- `CLAUDE_CODE_FORK_SUBAGENT=0` — fork mode off, which stops the `agent_summary` side query. → [Claude fork mode off](image-build.md#claude-fork-mode-off-agent_summary)

Host-side (not baked — set per-shell by `sessionplan.shellEnv`): `CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=<workspace basename>`. The upstream default is the machine hostname, which inside the container is the container name (`SessionPlan.Hostname`) — every Remote Control session on claude.ai web/mobile would be named after the container rather than the workspace, carrying the `toolbox-` prefix and, on a workspace session, the path hash.

`DISABLE_TELEMETRY` is intentionally NOT set (it was part of the original lockdown): upstream [documents](https://code.claude.com/docs/en/env-vars#features-that-need-feature-flag-fetching) that it also turns off feature-flag fetching, which drops the flag-gated features (artifact comments, MCP tasks, account skill and plugin sync, idle compaction, …) and Remote Control auto-connect, and [Remote Control itself](https://code.claude.com/docs/en/remote-control#requirements) once the organization requires Trusted Devices — first reported in [anthropics/claude-code#58383](https://github.com/anthropics/claude-code/issues/58383). It carries product analytics, not training data: the model-improvement opt-out is the claude.ai account-level privacy setting ("Help improve Claude"), and admin usage analytics ([claude.ai/analytics](https://code.claude.com/docs/en/analytics)) are collected server-side regardless of any client flag.

The `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` umbrella flag is likewise NOT set, for a stronger documented reason: it turns off feature-flag fetching too, and makes Remote Control unavailable outright. Baking it into the image was also correlated with intermittent OAuth re-login prompts on long-lived shells. The granular switches above cover the privacy part of what it bundles.

Remote Control (`claude --remote-control`, pairs the session with claude.ai web/mobile) is outbound-HTTPS-only — no inbound ports, so it works inside the container without `-p` bindings or the loopback bridge. The `/config` toggle "Enable Remote Control for all sessions" persists in the `~/.claude` bind and survives container recreates.

When a plugin is refreshed Claude Code prompts for `/reload-plugins`. Bumping the CLI itself is a Dockerfile concern: `CLAUDE_CODE_VERSION` + image rebuild (Renovate-driven).
