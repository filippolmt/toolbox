# CLAUDE.md

## Project

**Toolbox** — containerised Debian-slim dev environment managed by a host-side Go CLI (`toolbox shell` enters a disposable workspace). Two artefacts, separate release pipelines: Go CLI (host) + runtime image (container).

## Dev commands

**Go is not installed on the host** — reach every Go command through the `make` targets, which run `golang:1.26` (cache volume `toolbox-gomod`). `make help` lists them; what the target comments can't carry:

- `make go-check` — test + lint in one pass.
- `make go-run-clean` — `go-run` plus stopping the existing container, because env and mounts are fixed at ContainerCreate.
- `make build` — overwrites the local cache of the registry tag, so the next `./toolbox shell` picks up the freshly built image. → [image selection](docs/configuration.md#image-selection)
- Single test: `make go-shell`, then `go test ./internal/mountplan -run TestFoo -count=1`.

**Pre-push gate** — run what CI would run for the paths you touched:

| Touched | Run | CI job |
|---|---|---|
| Any Go file | `make go-check` | `ci.yml` (test + lint) |
| `internal/build/assets/**` or `go.mod` | `make test` as well | `docker-ci.yml` (build + smoke) |
| `renovate.json` | `npx --yes --package renovate@<pin> renovate-config-validator renovate.json` — take `<pin>` from `RENOVATE_VERSION` in `ci.yml`; unpinned `latest` has shipped an unfetchable tarball before | `ci.yml` (renovate-validate) |
| `.github/workflows/**` | `actionlint` | the workflow itself, on the next push |
| `.github/scripts/**` | `shellcheck` | the workflow that calls it, on the next push |

Markdown-only and `docs/**`-only changes add `make check-links` (`docs.yml`) as their own gate. `ci.yml` still runs on them — its three jobs are required checks on `main`, and a filtered-out workflow leaves them pending forever — but they touch nothing a docs change can break.

**Two gates block a merge on coverage**, and `make go-check` only mirrors one of them:

- `ci.yml` (`test`) enforces a **74% floor on total statement coverage**, pinned in the workflow — deliberately a couple of points under the current total (~75.6%), so the first sizeable untested addition doesn't turn an unrelated PR red. Always runs. `go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out` reproduces it locally.
- `sonar.yml` (`analyze`) is a required check on `main` and goes red on a failing Quality Gate — **80% on new code**, a server-side threshold. Skipped, and therefore silently satisfied, whenever the SonarQube server is powered down (it runs 09:00–19:00 Europe/Rome, Mon–Fri).

The two numbers use different denominators on purpose. → [sonarqube](docs/internals/sonarqube.md#the-two-coverage-numbers)

## Architecture

Host CLI in `cmd/` (cobra) + internal pipelines `config` → `mountplan` → `sessionplan` → `container` (Docker SDK). Runtime image baked from `internal/build/assets/` (Dockerfile + entrypoint + smoke-test + init.d/), embedded so `toolbox build` runs anywhere. Tool catalog (`internal/catalog`) declares every bundled CLI — it drives the init.d bijection, the smoke-test count literals and `inherit_host_auth` eligibility.

Before refactoring a pipeline seam (config plan, mount plan, session plan, tool catalog, init sequence): read the package plus its topic file under `docs/` and `docs/internals/`.

Shared fs primitives live in `internal/fsx`: `Home()` (strict, empty-`$HOME` guard), `ExpandTilde()`, `AtomicWriteFile()`. Call these from every package — `configio` re-exports the last two as thin facades. Soft sites that tolerate an empty home keep calling `os.UserHomeDir` directly. → [shared-fs-primitives](docs/internals/host-cli.md#shared-fs-primitives)

## Code & language

- **Repo content English; chat with user Italian.**
- `AGENTS.md` is a symlink to this file (Codex CLI). Keep the symlink while Codex is in use.
- Standard `gofmt`; lint config `.golangci.yml`.
- Test-first changes: use the `tdd` skill — Specify-Encode-Fulfill, one test at a time; a behavior change and a refactor stay in separate steps.

## Gotchas

Always-on:

- **Host UID mapping**: container runs `--user $(id -u):$(id -g)`; `/home/toolbox` world-writable. Keep the mapping dynamic. → [host-uid](docs/internals/image-build.md#host-uid-mapping)

Path-scoped gotchas live in `.claude/rules/`, each scoped by its own `paths:` frontmatter and lazy-loaded when a matching file is touched. Codex loads none of them — read the relevant file before editing that area:

- [`image-build.md`](.claude/rules/image-build.md) — Dockerfile, catalog, image assets.
- [`container-runtime.md`](.claude/rules/container-runtime.md) — lifecycle, session planning, `cmd/`, bridge + proximo assets.
- [`config-mounts-sdd.md`](.claude/rules/config-mounts-sdd.md) — config, mounts, SDD, catalog auth whitelist.

Releases: `v*` tag → GoReleaser + Homebrew. Merge to `main` → image push to GHCR. See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Code intelligence

Two indexes, split by what you are asking about:

- **Code — symbols, call paths, blast radius**: `codegraph_explore` (see [`.claude/CLAUDE.md`](.claude/CLAUDE.md)). Reach for it before grep or Read.
- **Everything else — architecture, docs, cross-file structure**: `graphify query "<question>"`, plus `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for one concept. `graphify-out/wiki/index.md` navigates broadly; `GRAPH_REPORT.md` only when those fall short. After modifying code: `graphify update .` (AST-only, no API cost).

The `## graphify` section below is generated by `graphify install` and states a
graphify-first rule for all codebase questions. This section overrides it: the
split above (symbols → codegraph, architecture/docs → graphify) is the rule; the
generated block describes graphify's commands, not their precedence.

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
