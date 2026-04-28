# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**Toolbox** — a containerised Debian-slim dev environment bundling every tool we use daily (Claude Code, cloud CLIs, Node, Python, Docker client, kubectl/helm/tofu, gh/glab, …). A Go CLI (`toolbox`) on the host manages the container lifecycle: `toolbox shell` drops you into a disposable, reproducible workspace.

The Go CLI runs on the HOST. The image runs INSIDE the container. They are separate artefacts and release through separate pipelines.

## Dev commands

**Go is not installed on the host.** Every Go command runs inside a `golang:1.26` container, cached in the named volume `toolbox-gomod`:

| Command | What it does |
|---------|--------------|
| `make go-test` | `go test ./... -count=1` (standard suite — use this) |
| `make go-test-verbose` | `go test -v -race ./...` (requires CGO, separate target) |
| `make go-lint` | `golangci-lint run ./...` (matches CI version) |
| `make go-build` | Build the `toolbox` binary |
| `make go-run` | Build CLI + open `toolbox shell` via the fresh binary (host-only smoke loop) |
| `make go-run-clean` | Like `go-run` but stops the existing container first — required when iterating on env vars / mounts (those are fixed at ContainerCreate time) |
| `make go-shell` | Open a shell in the golang container for ad-hoc Go work |
| `make go-clean-cache` | Drop the module/build cache volume |
| `make build` | Build the Docker runtime image (`toolbox:local`) |
| `make test` | Build image + run `internal/build/assets/smoke-test.sh` (validates all bundled tools) |
| `make shell` | Open an interactive bash inside the built image |

Never suggest `go test` or `go build` directly — the host has no Go toolchain.

Single test or package: `make go-shell`, then `go test ./internal/mount -run TestFoo -count=1`. The `toolbox-gomod` volume persists module/build cache, so subsequent runs are fast.

**Pre-push validation: use the `/verify` skill** (`.claude/skills/verify/SKILL.md`). Runs lint → test → (conditional) smoke-test in the same order as `.github/workflows/ci.yml`. Green locally = green on CI. Prefer it over invoking `go test` / `golangci-lint` ad-hoc.

## Architecture

Two artefacts in one repo:

- **Host CLI** (`cmd/` + `main.go`): cobra commands (`shell`, `build`, `stop`, `version`, `completion`) wired in `cmd/root.go` via viper. `cmd/root.go` also owns the config walk-up (`findProjectConfig`) that resolves the nearest `.toolbox.yaml`. `cmd/shell.go` is the hot path: merges config → resolves mounts → asks `internal/container` to ensure-and-attach.
- **Runtime image** (`internal/build/assets/Dockerfile` + `entrypoint.sh` + `smoke-test.sh` + `zshrc.sh`): everything that gets baked into `ghcr.io/filippolmt/toolbox`. The Go CLI embeds these via `internal/build` so `toolbox build` can rebuild from any host without a separate checkout.

Internal packages, in order of "you'll touch this most":

- `internal/config` — `.toolbox.yaml` schema, `DefaultMounts()`, `MergeMounts`, `ApplyMountsRoot`, `KnownTools`. Single source of truth for what gets mounted and which Dockerfile `INSTALL_<TOOL>` ARGs flip.
- `internal/container` — Docker SDK wrapper, split by concern: `lifecycle.go` (state machine: ensure / start / stop / cleanup), `attach.go` (TTY raw mode + signal forwarding), `names.go` (`ContainerNameFor`: `toolbox-<basename>-<hash8>`), `workspace.go` (`/workspace` mount + DooD-friendly mirror at the host's own absolute path), `ports.go` (`--publish` parsing, defaulting host IP to `127.0.0.1`, and the wanted-vs-actual mismatch warning). Calls `client.WithAPIVersionNegotiation()` so the SDK pin in `go.mod` need not match the runtime CLI version.
- `internal/mount` — turns `[]config.Mount` into Docker bind specs, creating missing source dirs and resolving `SymlinkFrom` targets against the host.
- `internal/build` — embeds the Dockerfile + assets and computes the local image tag (`toolbox:local-<hash>`) from the active tool set. `tag.go` `ResolveImage` is what decides "pull `:latest` or build locally".
- `internal/ui` — tiny print helpers (`Success` / `Warning` / etc.) used by `cmd/`.
- `internal/version` — build-time metadata (`-ldflags` populated by Makefile + GoReleaser). Don't hard-code; tests assert defaults.

## Code & language

- **Code, comments, and CLI output: English.** The chat with the user is Italian, but anything checked into the repo is English (variable names, log/user-facing strings, doc comments).
- Standard Go style (`gofmt` defaults). Lint config in `.golangci.yml` — enforced by CI.
- CLI follows cobra + viper conventions (see `cmd/` and `internal/config`).
- `AGENTS.md` is a symlink to this file so Codex CLI reads the same guidance. Don't duplicate content; don't delete the symlink unless dropping Codex support.

## Commits, PRs, releases

See [`CONTRIBUTING.md`](CONTRIBUTING.md). Key: `v*` tag triggers GoReleaser + Homebrew push; merge to `main` triggers image publish to GHCR.

## Non-obvious gotchas

- **Host UID mapping**: the CLI runs the container with `--user $(id -u):$(id -g)`. `/home/toolbox` is world-writable in the image because the runtime UID rarely matches the baked `toolbox` user. Don't revert to a fixed UID without understanding why.
- **Auth isolation under `~/.toolbox/`**: every credential path the container sees lives under `~/.toolbox/` on the host (`.claude`, `state`, `gh`, `glab`, `rtk/{config,data}`, …) or is a symlink to the host's real file (`ssh`, `gitconfig`). See `internal/config/config.go` `DefaultMounts()`. `~/.secrets` is intentionally NOT mounted. rtk is the only tool whose state spans two binds (XDG-strict: `~/.config/rtk` for config, `~/.local/share/rtk` for analytics + tee dumps), so both bind sources are nested under `~/.toolbox/rtk/` to keep the host layout flat.
- **`mounts:` is merged, not replaced**: user-declared mounts in `.toolbox.yaml` patch / replace / append / disable defaults by `name` (see `MergeMounts` in `internal/config/config.go`). A name-only entry patches the matching default; adding `target` replaces it; an unknown name is appended; `disabled: true` drops a default. A patch referencing a name that doesn't exist fails Load() loudly. Sources accept absolute, `~/`, and CWD-relative paths (resolved by `ResolveMounts` against the dir from which `toolbox shell` was invoked).
- **`mounts_root` retargets every default in one line**: setting `mounts_root: /custom/path` rewrites every default mount whose Source starts with `~/.toolbox/` to live under the new root, applied *before* `MergeMounts`. Per-mount patches still win, so a global root + a single per-name override coexist. `docker-sock` and `SymlinkFrom` targets are not touched (they reference real host paths, not toolbox-managed mirrors). Relative values are rejected at startup. See `ApplyMountsRoot` in `internal/config/config.go`.
- **Shared bash history**: `~/.toolbox/state/bash_history` is the `HISTFILE` for every toolbox shell across every project; `PROMPT_COMMAND` syncs concurrent sessions.
- **Docker CLI checksum**: Layer 7 of `internal/build/assets/Dockerfile` has no upstream `.sha256` (Docker doesn't publish one for static binaries). Version pin + HTTPS is the only guard — documented as accepted risk T-01-08.
- **Two Docker version streams, intentionally independent**: `DOCKER_CLI_VERSION` in the Dockerfile pins the CLI binary inside the container (currently 29.x); `github.com/docker/docker` in `go.mod` is the SDK the CLI launcher uses (pinned to the highest v28.x `+incompatible` tag, since upstream publishes no v29 Go module). The client calls `client.WithAPIVersionNegotiation()` so API drift between the two is expected and handled. Don't try to "align" them numerically.
- **Tool versions pinned**: every external binary in `internal/build/assets/Dockerfile` is pinned by version + SHA256 (except the Docker CLI and gcloud). Renovate bumps them. When adding a tool, follow the same pattern — download + verify `sha256sum` before installing. Every optional tool is guarded by an `ARG INSTALL_<TOOL>=true` flag wired to `tools.<key>` in `.toolbox.yaml`.
- **rtk arm64 is built from source** (Dockerfile `rtk-builder` stage + Layer 13c): upstream only ships `aarch64-unknown-linux-gnu` linked against GLIBC 2.39, but the base image (`node:24-bookworm-slim`) ships GLIBC 2.36 — the prebuilt binary aborts with `'GLIBC_2.39' not found`. There is no `aarch64-unknown-linux-musl` release. The fix is a multi-stage build: a `rust:1-slim-bookworm` stage runs `cargo install --git rtk-ai/rtk --tag v${RTK_VERSION} --locked` against the bookworm sysroot (so the resulting binary ABI-matches the runtime), and Layer 13c COPYs it into place. The same stage handles the amd64 tarball download too, so Layer 13c is a single COPY + version check. The base image can't move to Debian trixie yet because the Microsoft Azure CLI apt repo currently has no trixie suite.
- **Rust base image tag scheme is `<ver>-slim-<distro>`, not `<ver>-<distro>-slim`**. Docker Hub publishes `rust:1-slim-bookworm` (correct) but **not** `rust:1-bookworm-slim` (404). Got bitten on PR #89 — the rtk-builder stage failed at image-pull time. When bumping or referencing the rust base, use `<ver>-slim-<distro>` order (or the bare `<ver>-slim` for the default trixie variant when we eventually move).
- **Slim Rust images ship no `curl` / `ca-certificates`**. `rust:1-slim-bookworm` contains cargo + git but nothing to fetch tarballs with. The rtk-builder stage installs them via apt before the amd64 tarball path. If you copy the pattern for another tool (e.g. building a Rust binary from source), replicate the apt install — it doesn't propagate from the base.
- **Image selection**: `toolbox shell` pulls `ghcr.io/filippolmt/toolbox:latest` only when the merged `tools:` config matches the defaults (all true). Any override auto-builds `toolbox:local-<hash>` from the embedded Dockerfile — see `internal/build/tag.go` `ResolveImage`. `toolbox build` is an explicit escape hatch (supports `--no-cache`).
- **Port bindings are fixed at container creation**: `toolbox shell -p <port>` takes effect only when the container is first created. To change or add bindings on an existing workspace, run `toolbox stop` before re-invoking `toolbox shell -p …`. Accepted formats mirror `docker run -p`; host IP defaults to `127.0.0.1` when omitted.
- **Adding (or removing) an entry in `internal/config/tools.go` `KnownTools` invalidates the local image hash for every user with a non-default `tools:` config**. The hash is computed over the sorted Tools map, so a new key shifts the digest even when the user never set it. Practical effect: the next `toolbox shell` shows "Image not found locally — building toolbox:local-…" once and rebuilds. Document this in the release notes when bumping the list. Users on the canonical defaults are unaffected (they pull `:latest` from GHCR).

## Runtime container (shell session)

- **PID 1 is `tini`** (baked into the image) — reaps zombies and forwards signals cleanly so `Ctrl-C` and container stop behave the same as host processes. Don't replace it with a plain `bash` entrypoint.
- **MCP plugin auto-build on shell start**: `internal/build/assets/entrypoint.sh` scans `~/.claude/plugins/cache/**` and runs `npm install && npm run build` for any plugin missing a `dist/`. First shell after a plugin install is therefore slower; subsequent shells are cached via a `.toolbox-built` marker. On failure stderr is captured to `.toolbox-build-error.log` next to the marker (in the same bind-mounted plugin dir, so it survives container restarts) and the last 5 lines are printed inline; the failure stays non-fatal for the rest of the entrypoint.
- **rtk hook auto-wiring on shell start**: `entrypoint.sh` runs `rtk init -g` (Claude) and `rtk init -g --codex` (Codex) on every shell so the Bash-tool rewrite hook stays registered even after a settings reset or a fresh `~/.toolbox/.claude` bind-mount. Gated on `command -v claude` / `command -v codex` so opted-out tools never have rtk hooks injected. Idempotent; failures are non-fatal. Privacy is enforced at the env layer image-wide: `RTK_TELEMETRY_DISABLED=1` blocks every telemetry code path regardless of consent state, and `RTK_TEE=0` blocks the tee feature regardless of `[tee] enabled` in the TOML — so failed-command stdout (which often carries auth tokens from `gh auth status`, `aws sts`, `curl -H Authorization:`) is never written to disk under `~/.local/share/rtk/`. The entrypoint additionally pre-seeds `~/.config/rtk/config.toml` with `[tee] enabled = false` and `[telemetry] enabled = false` on first launch (belt-and-braces, so `rtk telemetry status` reports a consistent state and unsetting either env var still inherits safe defaults). The seed is gated on file absence — env vars are the load-bearing defense for users with a stale config.toml from before the seed existed, and survive `rtk telemetry enable/disable` rewriting the whole TOML.
- **Claude Code plugin auto-update is delegated to Claude Code itself.** The image sets `DISABLE_AUTOUPDATER=1` (block background CLI self-update — `/usr/local/lib/node_modules` is root-only), `FORCE_AUTOUPDATE_PLUGINS=1` (the documented escape hatch from the [discover-plugins guide](https://code.claude.com/docs/en/discover-plugins#configure-auto-updates) that keeps plugin updates running even when the CLI auto-updater is disabled), plus `DISABLE_TELEMETRY=1`, `DISABLE_FEEDBACK_COMMAND=1`, and `DISABLE_ERROR_REPORTING=1` for privacy. Together those four `DISABLE_*` flags cover what the [env vars](https://code.claude.com/docs/en/env-vars) page calls the expansion of `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`. The umbrella flag itself is intentionally NOT set: baking it into the image was correlated with intermittent OAuth re-login prompts on long-lived shells, suggesting the umbrella gates undocumented behaviour beyond its four stated sub-flags. When a plugin is refreshed Claude Code prompts for `/reload-plugins`. Bumping the CLI itself is a Dockerfile concern: `CLAUDE_CODE_VERSION` + image rebuild (Renovate-driven).
- **User config is `.toolbox.yaml`** (project root) merged with `~/.toolbox.yaml` (global). Schema matches `internal/config/config.go` `Config` struct. `tools.<key>: false` opts out of optional layers and drives the local image hash via `ResolveImage` — see `internal/build/tag.go`.
- **Config load order** (highest priority first): `--config` flag → nearest `.toolbox.yaml` walking up from CWD (stops at HOME / filesystem root) → `~/.toolbox.yaml` → `TOOLBOX_*` env vars → built-in defaults. The walk-up means `toolbox shell` from any subdir of a workspace still picks up the workspace's project config; `findProjectConfig` in `cmd/root.go` is the source of truth.
- **Startup hooks**: `~/.toolbox/startup.d/*.sh` run as the `toolbox` user on every `toolbox shell`, before the prompt. Hooks share mounted credentials and can write to `~/.toolbox/npm-global/` without root. Ready-to-copy example in `examples/startup.d/`.
