# CLAUDE.md

## Project

**Toolbox** — containerised Debian-slim dev environment managed by a host-side Go CLI (`toolbox shell` enters a disposable workspace). Two artefacts, separate release pipelines: Go CLI (host) + runtime image (container).

## Dev commands

**Go is not installed on the host** — reach every Go command through the `make` targets, which run the `golang` image pinned by `GO_IMAGE_VERSION` in the `Makefile` (cache volume `toolbox-gomod`). `make help` lists them; what the target comments can't carry:

- `make go-build` — targets the host through `TOOLBOX_HOST_OS` / `TOOLBOX_HOST_ARCH` (set by `sessionplan`), since `uname` in a toolbox shell reports the container. Explicit override: `make go-build-macos` (`MACOS_ARCH=amd64` for an Intel Mac).
- `make build` — overwrites the local cache of the registry tag, so the next `./toolbox shell` picks up the freshly built image. Inspect a build through `toolbox-local:build`, which no pull re-points. → [image selection](docs/configuration.md#image-selection)
- One package: `make go-test-one PKG=./internal/mountplan` (optional `RUN=TestFoo`, `TAGS=...`).
- Ad-hoc `docker run -v`/`--mount` from a toolbox shell, daemon gates, new-code preflight: [development feedback loops](docs/agents/development-feedback.md).

**Pre-push gate** — run what CI would run for the paths you touched:

| Touched | Run | CI job |
|---|---|---|
| Any Go file | `make go-check` | `ci.yml` (coverage-enforced test + lint) |
| `.pi/**` | `make check-agent` | `ci.yml` (lint) |
| `internal/build/assets/**` or `go.mod` | `make test` as well | `docker-ci.yml` (build + smoke) |
| `internal/{container,mountplan,sessionplan,reload,imagereclaim}/**` | `make go-check` + `make go-dockergate` against the intended runtime image | `docker-ci.yml` (build + smoke + real-daemon gates) |
| `renovate.json` | `npx --yes --package renovate@<pin> renovate-config-validator renovate.json` — take `<pin>` from `RENOVATE_VERSION` in `ci.yml`; unpinned `latest` has shipped an unfetchable tarball before | `ci.yml` (renovate-validate) |
| `.goreleaser.yaml` | `goreleaser release --snapshot --clean --skip=publish,validate`, then read `dist/homebrew/Casks/toolbox.rb` — `goreleaser check` only validates the schema and renders no template | `ci.yml` (cask) |
| `.github/workflows/**` | `actionlint` | the workflow itself, on the next push |
| `.github/scripts/**` | `shellcheck` for shell scripts + `make check-devtools` | the workflow that calls it, on the next push — `.github/scripts/host-share-git-rewrite.sh` has none, run `make test-host-share` |
| `internal/build/assets/entrypoint.sh` | `make test-host-share` as well, from a Mac running Docker Desktop | none — no runner serves a Host Share |
| `internal/build/assets/**/*.sh` | `find internal/build/assets -name '*.sh' ! -name 'zshrc.sh' -print0 \| xargs -0 shellcheck -S error` — `zshrc.sh` is zsh, which shellcheck cannot parse | `ci.yml` (shellcheck) |
| Any `*.md`, `.claude/rules/**`, the Dockerfile | `make go-test-one PKG=./internal/devrules` + `make check-links` — devrules holds the docs map, the rule files and the Claude env-knob matrix to the files they describe | `ci.yml` (test), `docs.yml` |

**Coverage: 80%, two denominators.** `ci.yml` gates **total** coverage (`COVERAGE_MIN`; `make go-coverage`, part of `make go-check`); `sonar.yml` gates **new code** on the server — and passes unchecked while the SonarQube server is off. → [sonarqube](docs/internals/sonarqube.md#the-two-coverage-denominators)

## Architecture

Host CLI in `cmd/` (cobra) + internal pipelines `config` → `mountplan` → `sessionplan` → `container` (Docker SDK). Runtime image baked from `internal/build/assets/` (Dockerfile + entrypoint + smoke-test + init.d/), embedded so `toolbox build` runs anywhere. Tool catalog (`internal/catalog`) declares every bundled CLI — it drives the init.d bijection, the smoke-test count literals and `inherit_host_auth` eligibility.

[`CONTEXT.md`](CONTEXT.md) is the glossary: every named concept (Mount Plan, Config Schema, Invalidation Floor, Peer Anchor, …) with its meaning, its owning package and why it was named. Before crossing a pipeline seam, use the [heading-first lookup](docs/agents/domain.md#before-exploring); add an entry when a design conversation names a new concept. Topic guides are mapped in [`docs/README.md`](docs/README.md). A rule under `.claude/rules/` carries the guardrail and its test names, the glossary the meaning and the why: link across, one source each.

Shared fs primitives live in `internal/fsx` (listed in its package doc; `Home()` is strict, with an empty-`$HOME` guard). Call them directly, under their own names; soft sites that tolerate an empty home call `os.UserHomeDir`. → [shared-fs-primitives](docs/internals/host-cli.md#shared-fs-primitives)

## Code & language

- **Repo content English.**
- **Name the pin, not the value.** Renovate moves every pinned tool, so prose names what pins it (`GO_IMAGE_VERSION` in the `Makefile`, `toolchain` in `go.mod`, the Dockerfile `*_VERSION` ARGs) and describes behaviour, not the release it changed in. Same for any moving figure: coverage totals, tool counts, timings. Figures stay only as an enforced threshold, a dated ADR measurement, or in a commit message / PR body.
- `AGENTS.md` is a symlink to this file (Codex CLI). Keep the symlink while Codex is in use.
- Test-first changes: use the `tdd` skill — Specify-Encode-Fulfill, one test at a time; a behavior change and a refactor stay in separate steps.

## Gotchas

Always-on:

- **Host UID mapping**: container runs `--user $(id -u):$(id -g)`; `/home/toolbox` world-writable. Keep the mapping dynamic. → [host-uid](docs/internals/image-build.md#host-uid-mapping)

Path-scoped gotchas live in `.claude/rules/`, each scoped by its own `paths:` frontmatter and lazy-loaded when a matching file is touched. Codex loads none of them — read the relevant file before editing that area:

- [`image-build.md`](.claude/rules/image-build.md) — Dockerfile, catalog, image assets.
- [`container-runtime.md`](.claude/rules/container-runtime.md) — lifecycle, session planning, `cmd/`, bridge + proximo assets.
- [`config.md`](.claude/rules/config.md) — config packages, `config|shells|mounts` CLI surfaces.
- [`mounts.md`](.claude/rules/mounts.md) — mount plan, auth isolation, profiles, catalog auth whitelist.
- [`sdd.md`](.claude/rules/sdd.md) — SDD skill packs, `.gitignore` fence.
- [`rules.md`](.claude/rules/rules.md) — the rule files themselves: what holds their `paths:` to their prose.

Releases: `v*` tag → GoReleaser + Homebrew. Merge to `main` → image push to GHCR; after merging an image change, follow that run to green with `gh run watch --compact --exit-status` — a red publish leaves `:latest` on the previous image with nothing else signalling it. See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Code intelligence

Code questions go to `codegraph_explore` (CLI: `codegraph explore`, bounded with `--max-files`). No graphify here: after a `graphify install`, remove the `## graphify` block it adds to `.claude/CLAUDE.md` and its `hook-guard` hooks in `.claude/settings.json`.

## Agent skills

### Change workflow

Branches are `feat/<slug>` or `fix/<slug>` (a dependency bump takes `fix/`), enforced by `.github/scripts/check-branch-name.sh`. Unless the user selects a branch, record the starting HEAD and branch before the first commit. Before opening a PR, run `code-review:code-review` (not the built-in `code-review`, which checks neither axis) against that SHA, or the merge-base with `main`; if a design session re-scoped the issue, update the issue first — the Spec axis reads the tracker. Without a delegation tool, run the Standards and Spec axes as sequential passes and say so.

### Issue tracker

Filing or reading an issue or spec (GitHub Issues) → [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md).

### Triage labels

Triaging an issue → [`docs/agents/triage-labels.md`](docs/agents/triage-labels.md).

### Domain docs

Glossary or ADR work (single context: `CONTEXT.md` + `docs/adr/`) → [`docs/agents/domain.md`](docs/agents/domain.md).
