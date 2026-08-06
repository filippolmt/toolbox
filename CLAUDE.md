# CLAUDE.md

Guidance for Claude Code on this repo.

## Project

**Toolbox** — containerised Debian-slim dev environment managed by a host-side Go CLI (`toolbox shell` enters a disposable workspace). Two artefacts, separate release pipelines: Go CLI (host) + runtime image (container).

## Dev commands

**Go is not installed on the host.** Commands run in `golang:1.26` (cache volume `toolbox-gomod`):

| Command | What it does |
|---------|--------------|
| `make go-test` | `go test ./... -count=1` |
| `make go-test-verbose` | `go test -v -race ./...` (opt-in; CGO on) |
| `make go-lint` | `golangci-lint run ./...` (CI-matched) |
| `make go-check` | `go-test` + `go-lint` in one pass (quick Go gate; not a `/verify` substitute — no image smoke-test) |
| `make go-run` | Build CLI + open `toolbox shell` |
| `make go-run-clean` | Like `go-run` + stop existing container (env/mounts are fixed at ContainerCreate) |
| `make build` / `make test` | Build runtime image (tag: `ghcr.io/filippolmt/toolbox:latest`) / build + smoke-test |

Single test: `make go-shell`, then `go test ./internal/mountplan -run TestFoo -count=1`.

`make build` overwrites the local cache of the registry tag, so the next `./toolbox shell` picks up the freshly built image — restore/interaction details in [image selection](docs/configuration.md#image-selection).

Repo-local SDD: `./toolbox sdd list` / `./toolbox sdd init <name>` — see [docs/sdd.md](docs/sdd.md).

**Pre-push validation: use the `/verify` skill.** Mirrors `.github/workflows/ci.yml`. Never invoke `go test` / `golangci-lint` directly — host has no Go.

## Architecture

Host CLI in `cmd/` (cobra) + internal pipelines `config` → `mountplan` → `sessionplan` → `container` (Docker SDK). Runtime image baked from `internal/build/assets/` (Dockerfile + entrypoint + smoke-test + init.d/), embedded so `toolbox build` runs anywhere. Tool catalog (`internal/catalog`) declares every bundled CLI — it drives the init.d bijection, the smoke-test count literals and `inherit_host_auth` eligibility (no build args: per-tool opt-out is gone).

Adding a CLI to the image: use the `add-cli` skill — it covers all required edits (Dockerfile layer + ARG + `internal/catalog` `Entries` row + smoke bijection + Renovate + optional `~/.toolbox/<tool>` mount) and finishes with `/verify`. No per-tool opt-out (see `.claude/rules/image-build.md`).

Pipeline seams (config plan, mount plan, session plan, tool catalog, init sequence) — read package code + the topic files under `docs/` and `docs/internals/` before refactoring.

Shared fs primitives live in `internal/fsx`: `Home()` (strict, empty-`$HOME` guard), `ExpandTilde()`, `AtomicWriteFile()`. Don't re-implement these per-package — `configio` re-exports the last two as thin facades. Soft sites that tolerate an empty home keep calling `os.UserHomeDir` directly. → [shared-fs-primitives](docs/internals/host-cli.md#shared-fs-primitives)

## Code & language

- **Repo content English; chat with user Italian.**
- `AGENTS.md` is a symlink to this file (Codex CLI). Don't unlink unless dropping Codex.
- Standard `gofmt`; lint config `.golangci.yml`.
- Test-first changes: use the `tdd` skill (`/tdd <spec>`) — Specify-Encode-Fulfill, one test at a time, never mix a behavior change with a refactor.

## Gotchas — backstory under [`docs/`](docs/) and [`docs/internals/`](docs/internals/)

Always-on:

- **Host UID mapping**: container runs `--user $(id -u):$(id -g)`; `/home/toolbox` world-writable. Don't revert to fixed UID. → [host-uid](docs/internals/image-build.md#host-uid-mapping)

Path-scoped gotchas live in `.claude/rules/` and lazy-load when matching files are touched; the authoritative scope is each file's `paths:` frontmatter (Codex doesn't auto-load them — read the relevant file before editing those areas):

- [`image-build.md`](.claude/rules/image-build.md) — Dockerfile / catalog / image assets: sudo, docker checksum, layer layout, version pinning/streams, rtk traps + lockdown, homebrew, init.d/completion bijections, skill paths.
- [`container-runtime.md`](.claude/rules/container-runtime.md) — lifecycle / session planning / cmd (plus the bridge + proximo build assets): image selection, port bindings, loopback bridge, codex sandbox, teardown/AutoRemove, bridge (browser/editor/proximo forwarder), proximo.
- [`config-mounts-sdd.md`](.claude/rules/config-mounts-sdd.md) — config / mounts / SDD (plus the catalog auth whitelist): auth isolation, inherit_host_auth, SDD fence/steps, config load order, `env:` passthrough.

Releases: `v*` tag → GoReleaser + Homebrew. Merge to `main` → image push to GHCR. See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
