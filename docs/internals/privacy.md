# Runtime privacy internals

Maintainer notes on the telemetry / data-exfiltration lockdown baked into the image: rtk's env-layer defenses and the Claude Code env-var matrix (what is set, what is deliberately not, and why).

## rtk hook auto-wiring + telemetry/tee lockdown

`internal/build/assets/init.d/10-rtk.sh` runs `rtk init -g` (Claude) and `rtk init -g --codex` (Codex) on every shell so the Bash-tool rewrite hook stays registered even after a settings reset or a fresh `~/.toolbox/.claude` bind-mount. Gated on `command -v claude` / `command -v codex` so opted-out tools never have rtk hooks injected. Idempotent; failures are non-fatal.

Privacy is enforced at the env layer image-wide:

- `RTK_TELEMETRY_DISABLED=1` blocks every telemetry code path regardless of consent state.
- `RTK_TEE=0` blocks the tee feature regardless of `[tee] enabled` in the TOML — so failed-command stdout (which often carries auth tokens from `gh auth status`, `aws sts`, `curl -H Authorization:`) is never written to disk under `~/.local/share/rtk/`.

The entrypoint additionally pre-seeds `~/.config/rtk/config.toml` with `[tee] enabled = false` and `[telemetry] enabled = false` on first launch (belt-and-braces, so `rtk telemetry status` reports a consistent state and unsetting either env var still inherits safe defaults). Seed gated on file absence — env vars are the load-bearing defense for users with a stale config.toml from before the seed existed, and survive `rtk telemetry enable/disable` rewriting the whole TOML.

## Claude Code env-var matrix

The image sets:

- `DISABLE_AUTOUPDATER=1` — block background CLI self-update (`/usr/local/lib/node_modules` is root-only).
- `FORCE_AUTOUPDATE_PLUGINS=1` — documented escape hatch from the [discover-plugins guide](https://code.claude.com/docs/en/discover-plugins#configure-auto-updates) that keeps plugin updates running even when the CLI auto-updater is disabled.
- `DISABLE_FEEDBACK_COMMAND=1`, `DISABLE_ERROR_REPORTING=1` — privacy (`/bug` ships conversation data to Anthropic; error reporting ships stack traces).
- `USE_BUILTIN_RIPGREP=0` — use the apt-pinned system `rg` (base layer) instead of the npm-bundled binary.
- `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1` — return to the project dir after every Bash call; prevents cd-drift across tool invocations.
- `BASH_DEFAULT_TIMEOUT_MS=300000`, `BASH_MAX_TIMEOUT_MS=1200000` — 5m default / 20m ceiling (upstream 2m/10m); in-container image builds and test suites routinely exceed the upstream default.
- `CLAUDE_CODE_ENABLE_TASKS=1` — structured cross-session task tools; state persists in the `~/.claude` bind.
- `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` — opt into agent teams (experimental upstream; in-process mode needs no tmux).

Host-side (not baked — set per-shell by `sessionplan.shellEnv`): `CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=<workspace basename>`. The upstream default is the machine hostname, which inside the container is the Docker container ID — every Remote Control session on claude.ai web/mobile would be named hex gibberish.

`DISABLE_TELEMETRY` is intentionally NOT set (dropped 2026-06, was part of the original lockdown): the Statsig channel doubles as feature-flag delivery, and blocking it is reported to break Remote Control entitlements and preview-feature rollouts ([anthropics/claude-code#58383](https://github.com/anthropics/claude-code/issues/58383) — community-reported, not formally documented; re-verify before re-adding the flag). It carries product analytics, not training data: the model-improvement opt-out is the claude.ai account-level privacy setting ("Help improve Claude"), and admin usage analytics ([claude.ai/analytics](https://code.claude.com/docs/en/analytics)) are collected server-side regardless of any client flag.

The `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` umbrella flag is likewise NOT set: baking it into the image was correlated with intermittent OAuth re-login prompts on long-lived shells, suggesting the umbrella gates undocumented behaviour beyond its stated sub-flags.

Remote Control (`claude --remote-control`, pairs the session with claude.ai web/mobile) is outbound-HTTPS-only — no inbound ports, so it works inside the container without `-p` bindings or the loopback bridge. The `/config` toggle "Enable Remote Control for all sessions" persists in the `~/.claude` bind and survives container recreates.

When a plugin is refreshed Claude Code prompts for `/reload-plugins`. Bumping the CLI itself is a Dockerfile concern: `CLAUDE_CODE_VERSION` + image rebuild (Renovate-driven).
