# Contributing

## Commits & PRs

- Conventional-commits style: `feat:`, `fix:`, `chore:`, `docs:`, `refactor:`…
- **No `Co-Authored-By: Claude` / Anthropic trailer** on commits.
- Branches: `feat/<slug>`, `fix/<slug>`. Renovate owns `renovate/*`.
- PRs merged to `main` trigger the Docker image publish workflow.

## Local development

Go is **not** installed on the host — every Go command runs inside a `golang` container via the Makefile (a cache volume keeps module downloads warm). The runtime image builds the same way. Common targets:

| Target | What it does |
|--------|--------------|
| `make go-test` | Run the Go test suite. |
| `make go-lint` | Run `golangci-lint` (CI-matched config). |
| `make go-run` | Build the CLI and open `toolbox shell`. |
| `make build` / `make test` | Build the runtime image / build + smoke-test it. |

Before pushing, mirror the PR CI with the `/verify` skill — it runs the same `go test` + `golangci-lint` checks as `.github/workflows/ci.yml` (the Docker smoke-test runs separately in `.github/workflows/docker-ci.yml`). The full target table and architecture notes live in [`CLAUDE.md`](CLAUDE.md).

## Release flow

- Push a `v*` tag → `.github/workflows/release.yml` runs GoReleaser, publishes GitHub release assets, and pushes the Homebrew formula to `filippolmt/homebrew-tap` (needs `HOMEBREW_TAP_TOKEN`).
- Push to `main` → `.github/workflows/docker-publish.yml` builds multi-arch (amd64+arm64), smoke-tests amd64 locally, then pushes `ghcr.io/filippolmt/toolbox:{latest,sha-<short>}`.
- PR CI (`.github/workflows/ci.yml`) runs `go test` + `golangci-lint` (Go changes only — `**.md` / `docs/**` are path-ignored); the Docker smoke-test runs in `.github/workflows/docker-ci.yml` on image-affecting changes. Run `make go-test` / `make go-lint` locally for faster feedback, or use the `/verify` skill which mirrors the Go CI.
