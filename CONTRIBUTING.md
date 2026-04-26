# Contributing

## Commits & PRs

- Conventional-commits style: `feat:`, `fix:`, `chore:`, `docs:`, `refactor:`…
- **No `Co-Authored-By: Claude` / Anthropic trailer** on commits.
- Branches: `feat/<slug>`, `fix/<slug>`. Renovate owns `renovate/*`.
- PRs merged to `main` trigger the Docker image publish workflow.

## Release flow

- Push a `v*` tag → `.github/workflows/release.yml` runs GoReleaser, publishes GitHub release assets, and pushes the Homebrew formula to `filippolmt/homebrew-tap` (needs `HOMEBREW_TAP_TOKEN`).
- Push to `main` → `.github/workflows/docker-publish.yml` builds multi-arch (amd64+arm64), smoke-tests amd64 locally, then pushes `ghcr.io/filippolmt/toolbox:{latest,sha-<short>}`.
- PR CI (`.github/workflows/ci.yml`) runs `go test`, `golangci-lint`, and the Docker smoke-test on every PR. Run `make go-test` / `make go-lint` locally for faster feedback, or use the `/verify` skill (`.claude/skills/verify/SKILL.md`) which mirrors CI order.
