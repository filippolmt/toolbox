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

Single test or package: `make go-shell`, then `go test ./internal/mountplan -run TestFoo -count=1`. The `toolbox-gomod` volume persists module/build cache, so subsequent runs are fast.

**Pre-push validation: use the `/verify` skill** (`.claude/skills/verify/SKILL.md`). Runs lint → test → (conditional) smoke-test in the same order as `.github/workflows/ci.yml`. Green locally = green on CI. Prefer it over invoking `go test` / `golangci-lint` ad-hoc.

## Architecture

Two artefacts in one repo:

- **Host CLI** (`cmd/` + `main.go`): cobra commands (`shell`, `build`, `stop`, `version`, `completion`) wired in `cmd/root.go`. `cmd/root.go::initConfig` is a thin call site that delegates to `config.Plan(cwd, cfgFile)` (the **Config Plan** Seam — see CONTEXT.md) and stashes the resolved `*Config` on a package-level var; the `--config` flag binding lives here. `cmd/shell.go` is the hot path: consumes the resolved config → resolves mounts → asks `internal/container` to ensure-and-attach.
- **Runtime image** (`internal/build/assets/Dockerfile` + `entrypoint.sh` + `smoke-test.sh` + `zshrc.sh`): everything that gets baked into `ghcr.io/filippolmt/toolbox`. The Go CLI embeds these via `internal/build` so `toolbox build` can rebuild from any host without a separate checkout.

Internal packages, in order of "you'll touch this most":

- `internal/config` — owns the **Config Plan** pipeline (see CONTEXT.md). `.toolbox.yaml` schema (`Config`, `Mount`) + validators (`ValidateMountsRoot`, `ValidateShell`) live in `config.go`. The Plan Seam (`Plan(searchFrom, explicitOverride)` for the full pipeline, `Merge(global, project, explicit []byte)` for pure inspection) lives in `plan.go`. `Load()` survives only as a deprecated wrapper around `Plan(cwd, "")` for tests; `cmd/*` consumes the resolved `*Config` from `cmd/root.go` directly. Tool source-of-truth lives in `internal/catalog` — the `tools:` map is validated against the catalog's keys and feeds into the local-image hash via `internal/build`.
- `internal/mountplan` — owns the **Mount Plan** pipeline (see CONTEXT.md). External seam `Plan(cfg, workspace) → Result` returns the typed `[]Bind` and `WorkingDir` handed to `ContainerCreate`; pure inspection seam `Merge(cfg) → []Mount` exposes the post-merge list with no fs side-effects. Internally walks defaults → `applyMountsRoot` → `mergeMounts` → `resolveAll` → workspace bind + DooD mirror append. Also exports `Defaults()`, `ParentDirs()`, `WorkspaceMirrorPath`, `WorkspaceTarget`.
- `internal/sessionplan` — owns the **Session Plan** pipeline (see CONTEXT.md). External seam `Plan(cfg, workspace, ports, cliVersion) → *SessionPlan` returns the typed plan handed to `container.Shell`: image / binds / ports / env / working-dir / container-name / Cmd / SecurityOpt. Pure inspection seam `Merge(cfg, workspace, ports, cliVersion) → *MergedSessionPlan` exposes the same shape with no fs side effects (Binds typed as `[]config.Mount` instead of `[]mountplan.Bind`). Composes `build.ResolveImage`, `mountplan.Plan` / `mountplan.Merge`, and `shellcmd.ResolveShellCmd` / `shellcmd.NestedSandboxSecurityOpt`. Also exports `MissingPublishPorts` (pure port-mismatch detection consumed by lifecycle), `ContainerNameFor`, and `ContainerNamePrefix`. Phase 06's single-sectioned-file discipline is preserved: `// --- Public Seams ---`, `// --- Port Parsing ---`, `// --- Workspace Env ---` banners.
- `internal/shellcmd` — cycle-breaker package holding `ResolveShellCmd`, `NestedSandboxSecurityOpt`, and `ShellMismatchError`. Lives in its own package because `internal/sessionplan` composes these helpers and `internal/container` imports `internal/sessionplan` — keeping the helpers in `internal/container` would create a cycle. Same validation logic as before Phase 09; just a different home.
- `internal/container` — Docker SDK wrapper, two files by Module/Adapter split: `lifecycle.go` is the orchestration Module owning `Shell`/`Stop`/`StopAll`/`NewClient` plus the unexported helpers, sectioned into `Lifecycle` (state machine + image ensure + create/start consuming a `*sessionplan.SessionPlan`), `Cleanup helpers` (stop/remove + active-exec detection), and the UI-formatting helper `formatPublishMismatch` (consumes the typed plan + the missing-list returned by `sessionplan.MissingPublishPorts`); `Stop` / `StopAll` reach into `sessionplan.ContainerNameFor` / `sessionplan.ContainerNamePrefix` for naming so they need no plan input. Per-Shell session state (image / binds / ports / env / working-dir / container-name / Cmd / SecurityOpt) is computed by `internal/sessionplan` upstream; `lifecycle.Shell` is now a pure Docker SDK orchestrator. `lifecycle.go::NewClient` is the sole caller of `client.WithAPIVersionNegotiation()` so the SDK pin in `go.mod` need not match the runtime CLI version. `attach.go` stays a separate Adapter — TTY raw mode + signal forwarding kept off the Docker-SDK orchestration path; it never constructs a Docker client and operates only on the `client.APIClient` the Module hands it.
- `internal/build` — embeds the Dockerfile + assets and computes the local image tag (`toolbox:local-<hash>`) from the active tool set. `tag.go` `ResolveImage` is what decides "pull `:latest` or build locally".
- `internal/catalog` — owns the **Tool Catalog** (see CONTEXT.md). One typed table (`Entries`) declares every bundled tool's `Key`, `Default`, `BuildArg` plus optional `Description` / `InitScript` / `SmokeTest`. Consumers: `internal/build/tag.go` (canonical-encoded image hash via `WriteCanonical`, build args via `Entries`), `internal/config` (thin `Defaults` / `IsDefault` shims), `internal/build/assets/init.d/` (Init Sequence consumes `Entry.InitScript`). Optional fields are excluded from the canonical encoding so populating `InitScript` is hash-neutral for users.
- `internal/build/assets/init.d/` — owns the **Init Sequence** (see CONTEXT.md). Per-tool boot scripts (`<NN>-<tool>.sh`, currently 5: rtk, cf, graphify, playwright-cli, mcp-plugins) iterated by `entrypoint.sh` at shell start. Set-equality between `catalog.Entry.InitScript` values and the file list is enforced by `TestCatalogInitDBijection` (Go-side, `embed.FS`) and the smoke-test `init.d bijection + executability` block (shell-side, inside the built image — verifies mode 0755 because `embed.FS` strips exec bits). Marker logs at `~/.toolbox-state/init/<name>.log` inside the container.
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
- **Auth isolation under `~/.toolbox/`**: all credentials live under `~/.toolbox/` (canonical list: `mountplan.Defaults()`); `~/.secrets` deliberately NOT mounted. rtk and cf each span two binds (non-XDG split). See [`docs/runtime-notes.md#auth-isolation-under-toolbox`](docs/runtime-notes.md#auth-isolation-under-toolbox).
- **`mounts:` merge + `mounts_root`**: user mounts patch/replace/append/disable defaults by `name` via `mergeMounts`; `mounts_root: /custom/path` retargets every `~/.toolbox/`-rooted default before merge. Full semantics in [`docs/runtime-notes.md#mounts--auth-isolation`](docs/runtime-notes.md#mounts--auth-isolation).
- **Shared bash history**: `~/.toolbox/state/bash_history` is the `HISTFILE` for every toolbox shell across every project; `PROMPT_COMMAND` syncs concurrent sessions.
- **Docker CLI checksum**: Layer 7 of `internal/build/assets/Dockerfile` has no upstream `.sha256` (Docker doesn't publish one for static binaries). Version pin + HTTPS is the only guard — documented as accepted risk T-01-08.
- **Two Docker version streams**: `DOCKER_CLI_VERSION` (Dockerfile, container CLI binary, 29.x) and `github.com/docker/docker` (`go.mod`, SDK, v28.x `+incompatible`) move independently. `client.WithAPIVersionNegotiation()` handles drift. Don't "align" them. Detail in [`docs/runtime-notes.md#two-docker-version-streams`](docs/runtime-notes.md#two-docker-version-streams).
- **Tool versions pinned**: every external binary in `internal/build/assets/Dockerfile` is pinned by version + SHA256 (except the Docker CLI and gcloud). Renovate bumps them. When adding a tool, follow the same pattern — download + verify `sha256sum` before installing. Every optional tool is guarded by an `ARG INSTALL_<TOOL>=true` flag wired to `tools.<key>` in `.toolbox.yaml`.
- **rtk arm64 / Rust base image traps**: rtk arm64 is built from source (GLIBC 2.39 vs runtime 2.36), Rust base tag is `<ver>-slim-<distro>` (not the reverse — PR #89), and slim Rust images ship no `curl` / `ca-certificates`. Full backstory in [`docs/runtime-notes.md`](docs/runtime-notes.md#image-build).
- **Image selection**: `toolbox shell` pulls `ghcr.io/filippolmt/toolbox:latest` only when the merged `tools:` config matches the defaults (all true). Any override auto-builds `toolbox:local-<hash>` from the embedded Dockerfile — see `internal/build/tag.go` `ResolveImage`. `toolbox build` is an explicit escape hatch (supports `--no-cache`).
- **Port bindings are fixed at container creation**: `toolbox shell -p <port>` takes effect only when the container is first created. To change or add bindings on an existing workspace, run `toolbox stop` before re-invoking `toolbox shell -p …`. Accepted formats mirror `docker run -p`; host IP defaults to `127.0.0.1` when omitted.
- **Catalog edits invalidate local image hash**: adding/removing an entry in `internal/catalog/catalog.go` `Entries` shifts the digest for every user with a non-default `tools:` config (one-time rebuild). Canonical-defaults users unaffected. Document in release notes when bumping. Detail in [`docs/runtime-notes.md#catalog-entry--image-hash`](docs/runtime-notes.md#catalog-entry--image-hash).

## Runtime container (shell session)

- **PID 1 is `tini`** (baked into the image) — reaps zombies and forwards signals cleanly so `Ctrl-C` and container stop behave the same as host processes. Don't replace it with a plain `bash` entrypoint.
- **MCP plugin auto-build on shell start**: `init.d/50-mcp-plugins.sh` builds any plugin missing `dist/` (`.toolbox-built` marker; failures non-fatal, captured to `.toolbox-build-error.log`). First shell post-install is slower. Detail in [`docs/runtime-notes.md#mcp-plugin-auto-build`](docs/runtime-notes.md#mcp-plugin-auto-build).
- **rtk hook auto-wiring + privacy**: `internal/build/assets/init.d/10-rtk.sh` re-registers Claude/Codex Bash-rewrite hooks on every shell (idempotent). Telemetry + tee are blocked at the env layer (`RTK_TELEMETRY_DISABLED=1`, `RTK_TEE=0`) — load-bearing for stale TOMLs. Full env matrix + seed gating in [`docs/runtime-notes.md`](docs/runtime-notes.md#rtk-hook-auto-wiring--telemetrytee-lockdown).
- **Codex nested sandbox support**: when `tools.codex` is enabled (the default), `toolbox shell` creates the container with Docker `seccomp=unconfined` so Codex's built-in bubblewrap sandbox can create nested user namespaces. `tools.codex: false` keeps Docker's default seccomp profile.
- **`cf` Cloudflare CLI skill auto-install**: `init.d/20-cf.sh` writes `~/.claude/skills/cf/SKILL.md` when absent. Idempotent; user edits persist. Detail in [`docs/runtime-notes.md#cf-cloudflare-cli-skill-auto-install`](docs/runtime-notes.md#cf-cloudflare-cli-skill-auto-install).
- **Claude Code plugin auto-update**: image sets `DISABLE_AUTOUPDATER=1` + `FORCE_AUTOUPDATE_PLUGINS=1` so plugins keep updating while the CLI does not (CLI bumps are Dockerfile-driven via `CLAUDE_CODE_VERSION`). Privacy flags: `DISABLE_TELEMETRY=1`, `DISABLE_FEEDBACK_COMMAND=1`, `DISABLE_ERROR_REPORTING=1`. The umbrella `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` is intentionally NOT set — caused OAuth re-login prompts. Full rationale in [`docs/runtime-notes.md`](docs/runtime-notes.md#claude-code-env-var-matrix).
- **User config is `.toolbox.yaml`** (project root) merged with `~/.toolbox.yaml` (global). Schema matches `internal/config/config.go` `Config` struct. `tools.<key>: false` opts out of optional layers and drives the local image hash via `ResolveImage` — see `internal/build/tag.go`.
- **Config load order** (highest priority first): `--config` flag → nearest `.toolbox.yaml` walking up from CWD (stops at HOME / filesystem root) → `~/.toolbox.yaml` → `TOOLBOX_*` env vars → built-in defaults. The walk-up means `toolbox shell` from any subdir of a workspace still picks up the workspace's project config; the **Config Plan** Seam (`config.Plan` in `internal/config/plan.go`) is the source of truth.
- **Startup hooks**: `~/.toolbox/startup.d/*.sh` run as the `toolbox` user on every `toolbox shell`, before the prompt. Hooks share mounted credentials and can write to `~/.toolbox/npm-global/` without root. Ready-to-copy example in `examples/startup.d/`.
