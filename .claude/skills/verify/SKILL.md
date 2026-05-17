---
name: verify
description: Run the toolbox repo's pre-push validation — golangci-lint, go tests, and (when the image is built) the bundled-CLI smoke test. Mirrors the PR CI in `.github/workflows/ci.yml`, so green locally means green on CI. Use this before marking any code change "done", before opening a PR, or any time the user says things like "verify", "check it passes", "are we good to push", "è tutto a posto prima del commit". Always prefer this over running `go test` or `golangci-lint` ad-hoc, because Go is not installed on the host and this skill already encodes the containerised pattern.
---

# /verify

Validate the toolbox repo the way the human would before pushing. These are the same three jobs PR CI runs (`.github/workflows/ci.yml`: `lint`, `test`, `docker-build`), so passing locally means passing on CI — just faster feedback. The host has no Go toolchain; every Go command has to go through the `golang:1.26` container via the Makefile. Skipping that turns into "command not found: go", which wastes a round trip.

## Order matters

Run the three checks in ascending order of cost. Stop on the first failure — downstream checks can't pass on a broken build.

1. **`make go-lint`** — golangci-lint v2 in a container. Cheapest signal (static analysis, no test run). Catches errcheck / staticcheck / unused before they get masked by a test failure.
2. **`make go-test`** — `go test ./... -count=1` in a container. Runs the unit suite.
3. **Smoke test (conditional).** Only if the image already exists locally. Check with `docker image inspect ghcr.io/filippolmt/toolbox:latest >/dev/null 2>&1`:
   - If present → `internal/build/assets/smoke-test.sh` (default arg targets the same registry tag). This validates every bundled CLI in the runtime image.
   - If absent → skip. Do **not** trigger `make build` implicitly: it's a multi-minute rebuild and the user hasn't asked for it.

## Reporting

After every run, print a single line per check so the status is grep-able:

```
lint:       OK | FAIL
go-test:    OK | FAIL
smoke-test: OK | SKIPPED | FAIL
```

On failure, also show the first failing case (lint rule + file:line, failing test name, or the first smoke-test tool that errored). Don't paste the whole log — the human can rerun the target themselves if they want it.

## What not to do

- Don't try to "fix" lint/test failures automatically. Report and stop unless the user explicitly asked for a fix.
- Don't invoke `go test`, `go build`, `gofmt`, or `golangci-lint` directly on the host — they won't be found. The Makefile targets wrap `docker run … golang:1.26 …`; that's the only supported entry point.
- Don't run `make go-test-verbose` (race detector, `-v`) by default. It's opt-in — only when the user asks or you suspect a data race.

## Preconditions

If `docker info` fails (daemon not running), every step fails — say so up front and stop. There's no fallback; this project's toolchain is Docker-bound.
