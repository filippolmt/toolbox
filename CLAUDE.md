# CLAUDE.md

## Project

**Toolbox** — containerised Debian-slim dev environment managed by a host-side Go CLI (`toolbox shell` enters a disposable workspace). Two artefacts, separate release pipelines: Go CLI (host) + runtime image (container).

## Dev commands

**Go is not installed on the host** — reach every Go command through the `make` targets, which run the `golang` image pinned by `GO_IMAGE_VERSION` in the `Makefile` (cache volume `toolbox-gomod`). `make help` lists them; what the target comments can't carry:

- `make go-build` — cross-compiles for the host, preferring `TOOLBOX_HOST_OS` / `TOOLBOX_HOST_ARCH` (injected in every shell by `sessionplan` from the CLI's own `runtime.GOOS`/`GOARCH`) over `uname`, which inside a toolbox shell reports the *container* and would silently yield an unrunnable linux binary. `make go-build-macos` is the explicit override (`MACOS_ARCH=amd64` for an Intel Mac) — still needed in a container created before those vars existed, which needs a `toolbox stop` to pick them up.
- `make build` — overwrites the local cache of the registry tag, so the next `./toolbox shell` picks up the freshly built image. Inspect a build through `toolbox-local:build`, which no pull re-points. → [image selection](docs/configuration.md#image-selection)
- One package: `make go-test-one PKG=./internal/mountplan` (optional `RUN=TestFoo`, `TAGS=...`).
- Daemon gates, new-code preflight and scratch: [development feedback loops](docs/agents/development-feedback.md).

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
| `.github/scripts/**` | `shellcheck` for shell scripts + `make check-devtools` | the workflow that calls it, on the next push — `host-share-git-rewrite.sh` has none, run `make test-host-share` |
| `internal/build/assets/entrypoint.sh` | `make test-host-share` as well, from a Mac running Docker Desktop | none — no runner serves a Host Share |
| `internal/build/assets/**/*.sh` | `find internal/build/assets -name '*.sh' ! -name 'zshrc.sh' -print0 \| xargs -0 shellcheck -S error` — `zshrc.sh` is zsh, which shellcheck cannot parse | `ci.yml` (shellcheck) |

The [daemon runner](docs/agents/development-feedback.md#daemon-gates) makes fixtures host-visible under DooD and preserves caller permissions. Its safety prerequisites apply; CI remains the built-image verification.

Markdown-only and `docs/**`-only changes add `make check-links` (`docs.yml`) as their own gate. `ci.yml` still runs on them — it carries the checks `main` requires, and a filtered-out workflow leaves them pending forever — but they touch nothing a docs change can break.

**Coverage must be at least 80%**, and two gates enforce it:

- `ci.yml` (`test`) enforces the 80% floor on **total statement coverage**, pinned as `COVERAGE_MIN` in the workflow. Always runs. `make go-coverage` reproduces it locally and reads that pin rather than copying it; `make go-check` includes this target.
- `sonar.yml` (`analyze`) is a required check on `main` and goes red on a failing Quality Gate — the same 80%, on **new code**, as a server-side threshold. Skipped, and therefore silently satisfied, whenever the SonarQube server is powered down (it runs 09:00–19:00 Europe/Rome, Mon–Fri).

One threshold, two denominators on purpose. → [sonarqube](docs/internals/sonarqube.md#the-two-coverage-denominators)

## Architecture

Host CLI in `cmd/` (cobra) + internal pipelines `config` → `mountplan` → `sessionplan` → `container` (Docker SDK). Runtime image baked from `internal/build/assets/` (Dockerfile + entrypoint + smoke-test + init.d/), embedded so `toolbox build` runs anywhere. Tool catalog (`internal/catalog`) declares every bundled CLI — it drives the init.d bijection, the smoke-test count literals and `inherit_host_auth` eligibility.

[`CONTEXT.md`](CONTEXT.md) is the glossary: every named concept (Mount Plan, Config Schema, Invalidation Floor, Peer Anchor, …) with its meaning, its owning package and why it was named. Before crossing a pipeline seam, use the [heading-first lookup](docs/agents/domain.md#before-exploring); add an entry when a design conversation names a new concept. Topic guides are mapped in [`docs/README.md`](docs/README.md), section by section. A rule under `.claude/rules/` carries the guardrail and the test names, the glossary carries the meaning and the why — link across, never restate.

Shared fs primitives live in `internal/fsx` — its package doc lists them, and `Home()` sets the tone: strict, with an empty-`$HOME` guard. Call them directly from every package; no package re-exports them under a local name. Soft sites that tolerate an empty home keep calling `os.UserHomeDir` directly. → [shared-fs-primitives](docs/internals/host-cli.md#shared-fs-primitives)

## Code & language

- **Repo content English; chat with user Italian.**
- **Never write a version number, and never a current-state figure.** Renovate bumps the pinned tools continuously, so a version in prose is wrong by the next merge — name the thing that pins it (`GO_IMAGE_VERSION` in the `Makefile`, the `toolchain` directive in `go.mod`, the `*_VERSION` ARGs in the Dockerfile) and let the reader look. The same holds for any value that lives elsewhere and moves: a coverage total, a tool count, a timing. This includes "since vX" and "on vX+" boundaries — describe the behaviour, not the release it changed in. Two things stay: a **threshold the repo enforces**, because writing it *is* the rule, and a **dated measurement in an ADR**, whose header says its figures are as-of the decision. Everywhere else, explain the mechanism and drop the figure it was observed at — a post-mortem reads better as "upstream relocated the tree" than as a version that means nothing a year later.
- `AGENTS.md` is a symlink to this file (Codex CLI). Keep the symlink while Codex is in use.
- Test-first changes: use the `tdd` skill — Specify-Encode-Fulfill, one test at a time; a behavior change and a refactor stay in separate steps.

## Gotchas

Always-on:

- **Host UID mapping**: container runs `--user $(id -u):$(id -g)`; `/home/toolbox` world-writable. Keep the mapping dynamic. → [host-uid](docs/internals/image-build.md#host-uid-mapping)

Path-scoped gotchas live in `.claude/rules/`, each scoped by its own `paths:` frontmatter and lazy-loaded when a matching file is touched. Codex loads none of them — read the relevant file before editing that area:

- [`image-build.md`](.claude/rules/image-build.md) — Dockerfile, catalog, image assets.
- [`container-runtime.md`](.claude/rules/container-runtime.md) — lifecycle, session planning, `cmd/`, bridge + proximo assets.
- [`config.md`](.claude/rules/config.md) — config packages, `config|shells|mounts|worktree` CLI surfaces.
- [`mounts.md`](.claude/rules/mounts.md) — mount plan, auth isolation, profiles, catalog auth whitelist.
- [`sdd.md`](.claude/rules/sdd.md) — SDD skill packs, `.gitignore` fence.
- [`rules.md`](.claude/rules/rules.md) — the rule files themselves: what holds their `paths:` to their prose.

Releases: `v*` tag → GoReleaser + Homebrew. Merge to `main` → image push to GHCR; after merging an image change, follow that run to green with `gh run watch --compact --exit-status` — a red publish leaves `:latest` on the previous image with nothing else signalling it. See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Code intelligence

Code questions — symbols, call paths, blast radius — go to `codegraph_explore` with a specific symbol or owning module. Bound CLI source output with `--max-files`.

This repo does not use graphify; the image still bundles it for other projects. A `graphify install` run here adds a `## graphify` block to `.claude/CLAUDE.md` and `graphify hook-guard` `PreToolUse` hooks to `.claude/settings.json`, both asserting graphify-first: remove both.

## Agent skills

### Change workflow

Branches are `feat/<slug>` or `fix/<slug>` — a dependency bump takes `fix/`;
`.github/scripts/check-branch-name.sh` rejects anything else outside Renovate's
own `renovate/*`, in CI and as a `PreToolUse` hook. Unless the user
selects a branch, record the starting HEAD and create the branch before the
first commit; before opening a PR, run `code-review:code-review` against that
recorded SHA, or the merge-base with `main` when there is none — when a design
session re-scoped the issue, update the issue first so the Spec axis reads it
from the tracker (the built-in
`code-review` shares the slash name and checks neither axis). If the review skill requests sub-agents but the harness
exposes no delegation tool, run the Standards and Spec axes as separate
sequential passes and disclose that fallback.

### Issue tracker

Issues and specs are tracked in GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

Canonical triage roles map directly to GitHub labels. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses a single-context domain layout with `CONTEXT.md` as its glossary and ADRs under `docs/adr/`. See `docs/agents/domain.md`.
