# CLAUDE.md

Guidance for Claude Code on this repo.

## Project

**Toolbox** — containerised Debian-slim dev environment managed by a host-side Go CLI (`toolbox shell` enters a disposable workspace). Two artefacts, separate release pipelines: Go CLI (host) + runtime image (container).

## Dev commands

**Go is not installed on the host.** Commands run in `golang:1.26` (cache volume `toolbox-gomod`):

| Command | What it does |
|---------|--------------|
| `make go-test` | `go test ./... -count=1` |
| `make go-lint` | `golangci-lint run ./...` (CI-matched) |
| `make go-run` | Build CLI + open `toolbox shell` |
| `make go-run-clean` | Like `go-run` + stop existing container (env/mounts are fixed at ContainerCreate) |
| `make build` / `make test` | Build runtime image / build + smoke-test |

Single test: `make go-shell`, then `go test ./internal/mountplan -run TestFoo -count=1`.

**Pre-push validation: use the `/verify` skill.** Mirrors `.github/workflows/ci.yml`. Never invoke `go test` / `golangci-lint` directly — host has no Go.

## Architecture

Host CLI in `cmd/` (cobra) + internal pipelines `config` → `mountplan` → `sessionplan` → `container` (Docker SDK). Runtime image baked from `internal/build/assets/` (Dockerfile + entrypoint + smoke-test + init.d/), embedded so `toolbox build` runs anywhere. Tool catalog (`internal/catalog`) drives image hash + Dockerfile build args + init.d bijection.

Per-pipeline design lives in each package's CONTEXT.md (Config Plan / Mount Plan / Session Plan / Tool Catalog / Init Sequence). Read those before refactoring seams.

## Code & language

- **Repo content English; chat with user Italian.**
- `AGENTS.md` is a symlink to this file (Codex CLI). Don't unlink unless dropping Codex.
- Standard `gofmt`; lint config `.golangci.yml`.

## Gotchas — backstory in [`docs/runtime-notes.md`](docs/runtime-notes.md)

- **Host UID mapping**: container runs `--user $(id -u):$(id -g)`; `/home/toolbox` world-writable. Don't revert to fixed UID. → [host-uid](docs/runtime-notes.md#host-uid-mapping)
- **Auth isolation**: every credential under `~/.toolbox/` (canonical list `mountplan.Defaults()`); `~/.secrets` NOT mounted. `mounts:` patches/replaces/appends/disables defaults by `name`; `mounts_root` retargets pre-merge. → [auth-isolation](docs/runtime-notes.md#auth-isolation-under-toolbox), [mounts](docs/runtime-notes.md#mounts--auth-isolation)
- **Docker CLI checksum**: Layer 7 has no upstream `.sha256` — pin + HTTPS only (T-01-08). → [docker-checksum](docs/runtime-notes.md#docker-cli-checksum)
- **Two Docker version streams**: `DOCKER_CLI_VERSION` (29.x) and `go.mod` SDK (28.x `+incompatible`) move independently; `client.WithAPIVersionNegotiation()` handles drift. Don't align numerically. → [docker-streams](docs/runtime-notes.md#two-docker-version-streams)
- **Tool versions pinned** + `ARG INSTALL_<TOOL>` opt-out wired to `tools.<key>` + `Entries` row. Renovate-bumped. → [tool-pinning](docs/runtime-notes.md#tool-version-pinning--arg-install_tool-pattern)
- **rtk arm64 / Rust base traps**: GLIBC mismatch, tag scheme `<ver>-slim-<distro>`, slim ships no curl/ca-certs. → [image-build](docs/runtime-notes.md#image-build)
- **Image selection**: defaults pull `:latest`; overrides auto-build `toolbox:local-<hash>` (`tag.ResolveImage`). Catalog `Entries` edits invalidate hash for non-default users — note in release. → [image-selection](docs/runtime-notes.md#image-selection), [catalog-hash](docs/runtime-notes.md#catalog-entry--image-hash)
- **Port bindings fixed at container creation**: `toolbox stop` before re-`shell -p …`. → [port-bindings](docs/runtime-notes.md#port-bindings-are-fixed-at-container-creation)
- **Adding `init.d/<NN>-<tool>.sh` requires 3 synced edits**: (1) write script, (2) `InitScript` field on the matching `internal/catalog/catalog.go` `Entries` row, (3) `count >= N` literal in `internal/build/assets/smoke-test.sh` bijection block. `TestCatalogInitDBijection` (Go) catches (1)+(2); (3) drifts silently — count by hand.
- **rtk hook auto-wiring + privacy lockdown**: `RTK_TELEMETRY_DISABLED=1`, `RTK_TEE=0` load-bearing. → [rtk-hooks](docs/runtime-notes.md#rtk-hook-auto-wiring--telemetrytee-lockdown)
- **Codex nested sandbox**: `tools.codex: true` (default) sets Docker `seccomp=unconfined`. → [codex-sandbox](docs/runtime-notes.md#codex-nested-sandbox)
- **Skill discovery paths diverge**: Claude reads `~/.claude/skills/`, Codex reads `~/.agents/skills/` — wrappers shipping a SKILL.md need dual-install (see `init.d/60-glab.sh`). → [skill-paths](docs/runtime-notes.md#skill-discovery-paths-diverge-between-claude-and-codex)
- **SDD `.gitignore` fence**: `toolbox sdd init <key>` writes a single fenced block under `# >>> sdd-managed/<key> (toolbox)` using the glob patterns declared in `Skill.GitignoreEntries`. Patterns (not enumerated paths) keep the block compact and drift-free across upstream version bumps. Skills emitting user-authored content (bmad, openspec) leave the field nil and the fence is skipped. → [sdd-gitignore](docs/runtime-notes.md#sdd-gitignore-fence)
- **Config load order** (highest first): `--config` → walked-up `.toolbox.yaml` → `~/.toolbox.yaml` → `TOOLBOX_*` env → defaults. Source of truth: `config.Plan` in `internal/config/plan.go`. `tools.<key>: false` opts out + drives image hash.

Releases: `v*` tag → GoReleaser + Homebrew. Merge to `main` → image push to GHCR. See [`CONTRIBUTING.md`](CONTRIBUTING.md).
