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

Before pushing, mirror the PR CI: `make go-check` runs the same `go test` + `golangci-lint` checks as `.github/workflows/ci.yml`, and `make test` covers the Docker smoke-test that runs separately in `.github/workflows/docker-ci.yml`. The full gate table and architecture notes live in [`CLAUDE.md`](CLAUDE.md).

## Release flow

- Push a `v*` tag → `.github/workflows/release.yml` runs GoReleaser, publishes GitHub release assets, and pushes the Homebrew formula to `filippolmt/homebrew-tap` (needs `HOMEBREW_TAP_TOKEN`).
- Push to `main` → `.github/workflows/docker-publish.yml` builds multi-arch (amd64+arm64), smoke-tests amd64 locally, then pushes `ghcr.io/filippolmt/toolbox:{latest,sha-<short>}`.
- PR CI (`.github/workflows/ci.yml`) runs `go test` + `golangci-lint` on every PR, docs-only ones included, because its jobs are required status checks on `main` and a path-filtered workflow never reports them; the Docker smoke-test runs in `.github/workflows/docker-ci.yml` on image-affecting changes. Run `make go-check` locally to mirror the Go CI in one pass, or `make go-test` / `make go-lint` individually for faster feedback.
- SonarQube (`.github/workflows/sonar.yml`) analyses every PR and `main`. Its `analyze` job is a required status check on `main` and fails on a red Quality Gate (80% coverage on new code), so it can block a merge; it is skipped — and a skipped required check counts as satisfied — while the server is powered down. The same 80%, measured over total statements instead, is enforced by the `test` job in `ci.yml` and always holds. Neither is covered by `make go-check`; see [`docs/internals/sonarqube.md`](docs/internals/sonarqube.md).
