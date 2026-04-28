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

## Code & language

- **Code, comments, and CLI output: English.** The chat with the user is Italian, but anything checked into the repo is English (variable names, log/user-facing strings, doc comments).
- Standard Go style (`gofmt` defaults). Lint config in `.golangci.yml` — enforced by CI.
- CLI follows cobra + viper conventions (see `cmd/` and `internal/config`).
- `AGENTS.md` is a symlink to this file so Codex CLI reads the same guidance. Don't duplicate content; don't delete the symlink unless dropping Codex support.

## Commits, PRs, releases

See [`CONTRIBUTING.md`](CONTRIBUTING.md). Key: `v*` tag triggers GoReleaser + Homebrew push; merge to `main` triggers image publish to GHCR.

## Non-obvious gotchas

- **Host UID mapping**: the CLI runs the container with `--user $(id -u):$(id -g)`. `/home/toolbox` is world-writable in the image because the runtime UID rarely matches the baked `toolbox` user. Don't revert to a fixed UID without understanding why.
- **Auth isolation under `~/.toolbox/`**: every credential path the container sees lives under `~/.toolbox/` on the host (`.claude`, `state`, `gh`, `glab`) or is a symlink to the host's real file (`ssh`, `gitconfig`). See `internal/config/config.go` `DefaultMounts()`. `~/.secrets` is intentionally NOT mounted.
- **`mounts:` is merged, not replaced**: user-declared mounts in `.toolbox.yaml` patch / replace / append / disable defaults by `name` (see `MergeMounts` in `internal/config/config.go`). A name-only entry patches the matching default; adding `target` replaces it; an unknown name is appended; `disabled: true` drops a default. A patch referencing a name that doesn't exist fails Load() loudly. Sources accept absolute, `~/`, and CWD-relative paths (resolved by `ResolveMounts` against the dir from which `toolbox shell` was invoked).
- **`mounts_root` retargets every default in one line**: setting `mounts_root: /custom/path` rewrites every default mount whose Source starts with `~/.toolbox/` to live under the new root, applied *before* `MergeMounts`. Per-mount patches still win, so a global root + a single per-name override coexist. `docker-sock` and `SymlinkFrom` targets are not touched (they reference real host paths, not toolbox-managed mirrors). Relative values are rejected at startup. See `ApplyMountsRoot` in `internal/config/config.go`.
- **Shared bash history**: `~/.toolbox/state/bash_history` is the `HISTFILE` for every toolbox shell across every project; `PROMPT_COMMAND` syncs concurrent sessions.
- **Docker CLI checksum**: Layer 7 of `internal/build/assets/Dockerfile` has no upstream `.sha256` (Docker doesn't publish one for static binaries). Version pin + HTTPS is the only guard — documented as accepted risk T-01-08.
- **Two Docker version streams, intentionally independent**: `DOCKER_CLI_VERSION` in the Dockerfile pins the CLI binary inside the container (currently 29.x); `github.com/docker/docker` in `go.mod` is the SDK the CLI launcher uses (pinned to the highest v28.x `+incompatible` tag, since upstream publishes no v29 Go module). The client calls `client.WithAPIVersionNegotiation()` so API drift between the two is expected and handled. Don't try to "align" them numerically.
- **Tool versions pinned**: every external binary in `internal/build/assets/Dockerfile` is pinned by version + SHA256 (except the Docker CLI and gcloud). Renovate bumps them. When adding a tool, follow the same pattern — download + verify `sha256sum` before installing. Every optional tool is guarded by an `ARG INSTALL_<TOOL>=true` flag wired to `tools.<key>` in `.toolbox.yaml`.
- **rtk arm64 is built from source** (Dockerfile `rtk-builder` stage + Layer 13c): upstream only ships `aarch64-unknown-linux-gnu` linked against GLIBC 2.39, but the base image (`node:24-bookworm-slim`) ships GLIBC 2.36 — the prebuilt binary aborts with `'GLIBC_2.39' not found`. There is no `aarch64-unknown-linux-musl` release. The fix is a multi-stage build: a `rust:1-bookworm-slim` stage runs `cargo install --git rtk-ai/rtk --tag v${RTK_VERSION} --locked` against the bookworm sysroot (so the resulting binary ABI-matches the runtime), and Layer 13c COPYs it into place. The same stage no-ops on amd64 (which still uses the upstream static MUSL tarball). The base image can't move to Debian trixie yet because the Microsoft Azure CLI apt repo currently has no trixie suite.
- **Image selection**: `toolbox shell` pulls `ghcr.io/filippolmt/toolbox:latest` only when the merged `tools:` config matches the defaults (all true). Any override auto-builds `toolbox:local-<hash>` from the embedded Dockerfile — see `internal/build/tag.go` `ResolveImage`. `toolbox build` is an explicit escape hatch (supports `--no-cache`).
- **Claude Code auto-update is disabled** via `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` — the toolbox user can't write to `/usr/local/lib/node_modules` (installed as root). Bump `CLAUDE_CODE_VERSION` in the Dockerfile and rebuild.
- **Port bindings are fixed at container creation**: `toolbox shell -p <port>` takes effect only when the container is first created. To change or add bindings on an existing workspace, run `toolbox stop` before re-invoking `toolbox shell -p …`. Accepted formats mirror `docker run -p`; host IP defaults to `127.0.0.1` when omitted.

## Runtime container (shell session)

- **PID 1 is `tini`** (baked into the image) — reaps zombies and forwards signals cleanly so `Ctrl-C` and container stop behave the same as host processes. Don't replace it with a plain `bash` entrypoint.
- **MCP plugin auto-build on shell start**: `internal/build/assets/entrypoint.sh` scans `~/.claude/plugins/cache/**` and runs `npm install && npm run build` for any plugin missing a `dist/`. First shell after a plugin install is therefore slower; subsequent shells are cached.
- **rtk hook auto-wiring on shell start**: `entrypoint.sh` runs `rtk init -g` (Claude) and `rtk init -g --codex` (Codex) on every shell so the Bash-tool rewrite hook stays registered even after a settings reset or a fresh `~/.toolbox/.claude` bind-mount. Gated on `command -v claude` / `command -v codex` so opted-out tools never have rtk hooks injected. Idempotent; failures are non-fatal.
- **User config is `.toolbox.yaml`** (project root) merged with `~/.toolbox.yaml` (global). Schema matches `internal/config/config.go` `Config` struct. `tools.<key>: false` opts out of optional layers and drives the local image hash via `ResolveImage` — see `internal/build/tag.go`.
- **Config load order** (highest priority first): `--config` flag → nearest `.toolbox.yaml` walking up from CWD (stops at HOME / filesystem root) → `~/.toolbox.yaml` → `TOOLBOX_*` env vars → built-in defaults. The walk-up means `toolbox shell` from any subdir of a workspace still picks up the workspace's project config; `findProjectConfig` in `cmd/root.go` is the source of truth.
- **Startup hooks**: `~/.toolbox/startup.d/*.sh` run as the `toolbox` user on every `toolbox shell`, before the prompt. Hooks share mounted credentials and can write to `~/.toolbox/npm-global/` without root. Ready-to-copy example in `examples/startup.d/`.
