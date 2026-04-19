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
| `make go-build` | Build the `toolbox` binary |
| `make go-shell` | Open a shell in the golang container for ad-hoc Go work |
| `make go-clean-cache` | Drop the module/build cache volume |
| `make build` | Build the Docker runtime image (`toolbox:local`) |
| `make test` | Build image + run `internal/build/assets/smoke-test.sh` (validates all bundled tools) |
| `make shell` | Open an interactive bash inside the built image |

Never suggest `go test` or `go build` directly — the host has no Go toolchain.

## Code & language

- **Code, comments, and CLI output: English.** The chat with the user is Italian, but anything checked into the repo is English (variable names, log/user-facing strings, doc comments).
- Standard Go style (`gofmt` defaults). No custom linter config yet — don't invent one.
- CLI follows cobra + viper conventions (see `cmd/` and `internal/config`).

## Commits & PRs

- Conventional-commits style: `feat:`, `fix:`, `chore:`, `docs:`, `refactor:`…
- **No `Co-Authored-By: Claude` / Anthropic trailer** on commits.
- Branches: `feat/<slug>`, `fix/<slug>`. Renovate owns `renovate/*`.
- PRs merged to `main` trigger the Docker image publish workflow.

## Release flow

- Push a `v*` tag → `.github/workflows/release.yml` runs GoReleaser, publishes GitHub release assets, and pushes the Homebrew formula to `filippolmt/homebrew-tap` (needs `HOMEBREW_TAP_TOKEN`).
- Push to `main` → `.github/workflows/docker-publish.yml` builds multi-arch (amd64+arm64), smoke-tests amd64 locally, then pushes `ghcr.io/filippolmt/toolbox:{latest,sha-<short>}`.
- There is **no PR CI for Go tests or lint**. Run `make go-test` locally before pushing.

## Non-obvious gotchas

- **Host UID mapping**: the CLI runs the container with `--user $(id -u):$(id -g)`. `/home/toolbox` is world-writable in the image because the runtime UID rarely matches the baked `toolbox` user. Don't revert to a fixed UID without understanding why.
- **Auth isolation under `~/.toolbox/`**: every credential path the container sees lives under `~/.toolbox/` on the host (`.claude`, `state`, `gh`, `glab`) or is a symlink to the host's real file (`ssh`, `gitconfig`, `gitconfig-dbm`). See `internal/config/config.go` `DefaultMounts()`. `~/.secrets` is intentionally NOT mounted.
- **Shared bash history**: `~/.toolbox/state/bash_history` is the `HISTFILE` for every toolbox shell across every project; `PROMPT_COMMAND` syncs concurrent sessions.
- **Docker CLI checksum**: Layer 7 of `internal/build/assets/Dockerfile` has no upstream `.sha256` (Docker doesn't publish one for static binaries). Version pin + HTTPS is the only guard — documented as accepted risk T-01-08.
- **Two Docker version streams, intentionally independent**: `DOCKER_CLI_VERSION` in the Dockerfile (currently 29.4.0) pins the CLI binary inside the container; `github.com/docker/docker` in `go.mod` (currently v28.5.2+incompatible — the highest Go module upstream publishes) is the SDK the CLI launcher uses. The client calls `client.WithAPIVersionNegotiation()` so API drift between the two is expected and handled. Don't try to "align" them numerically — there is no v29 Go module.
- **Tool versions pinned**: every external binary in `internal/build/assets/Dockerfile` is pinned by version + SHA256 (except the Docker CLI and gcloud). Renovate bumps them. When adding a tool, follow the same pattern — download + verify `sha256sum` before installing. Every optional tool is guarded by an `ARG INSTALL_<TOOL>=true` flag wired to `tools.<key>` in `.toolbox.yaml`.
- **Image selection**: `toolbox shell` pulls `ghcr.io/filippolmt/toolbox:latest` only when the merged `tools:` config matches the defaults (all true). Any override auto-builds `toolbox:local-<hash>` from the embedded Dockerfile — see `internal/build/tag.go` `ResolveImage`. `toolbox build` is an explicit escape hatch (supports `--no-cache`).
- **Claude Code auto-update is disabled** via `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` — the toolbox user can't write to `/usr/local/lib/node_modules` (installed as root). Bump `CLAUDE_CODE_VERSION` in the Dockerfile and rebuild.

## Layout

```
cmd/                           Cobra commands (root, shell, stop, build, version, completion)
internal/config/               Viper config, Mount struct, DefaultMounts, Tools map
internal/container/            Docker SDK lifecycle (create, start, exec, attach, stop)
internal/mount/                Bind-mount source resolution (symlinks, auto-create)
internal/build/                Image build wrapper, embed.FS, ResolveImage tag logic
internal/build/assets/         Dockerfile + bashrc.sh + entrypoint.sh (embedded) + smoke-test.sh
internal/version/              CLI version/commit/date (populated by ldflags)
internal/ui/                   Colored output + spinner
.claude/skills/                Project skills (see /verify)
```
