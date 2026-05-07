# Phase 08 — Deferred Items

Out-of-scope discoveries surfaced during execution. Tracked here for future phases.

## 08-06 — pre-existing lint failures (Phase 09 sweep target)

`make go-lint` reports two `SA1019` staticcheck deprecation warnings:

- `cmd/build.go:33` — `config.Load is deprecated`
- `cmd/shell.go:36` — `config.Load is deprecated`

Both are **expected** and **out of scope for Phase 08**: Plan 05 deprecated
`config.Load` deliberately, leaving the wrapper in place and the call sites
unchanged. The deprecation warnings are the early-warning signal that the
Phase 09 (Session Plan) sweep needs to migrate `cmd/build.go`, `cmd/shell.go`,
and `cmd/stop.go` to consume the resolved `*Config` directly. The
`internal/config` bullet in CLAUDE.md (patched in this plan) names "Phase 09
sweep" as the resolution target.

These warnings predate Plan 06 (the worktree base commit `f28710fb` already
shows them); not introduced by this plan, not fixed here.
