---
plan: 08-02
phase: 08
slug: config-plan
status: complete
wave: 2
created: 2026-05-06
---

# Plan 08-02 Summary — Defaults + Merge Body

## One-liner

Wired `Merge` body (fresh `viper.New()` per call, catalog tool-defaults, validation tail; intentionally NO mount-default seeding per Pitfall 8) and `Plan` body (explicit/global/walk-up byte reads delegate to Merge); unskipped 3 bridging `TestPlan*` tests + added 2 validation rejections.

## What Shipped

**File:** `internal/config/plan.go` — 95 → 218 lines (+123)

- **Defaults Seeding:** `seedToolDefaults(vp)` iterates `catalog.Keys()` and emits dotted-key `vp.SetDefault("tools."+k, true)` calls (D-10; Pitfall 2 — flat keys, no nested objects).
- **`Merge`:** fresh `*viper.Viper` per call (D-09 — `viper.New()` plus `SetConfigType("yaml")`), MergeConfig(bytes.NewReader) for each layer (global → project → explicit), explicit short-circuits the other two layers, env-prefix `TOOLBOX` + `AutomaticEnv()` applied to instance only (D-09 — no global state).
- **`fillToolDefaultsBackstop`:** post-Unmarshal helper that re-seeds any `cfg.Tools` entries Unmarshal missed (Pitfall 1 — viper Unmarshal does not see AutomaticEnv values for keys absent from the file).
- **`applyValidationTail`:** runs `ValidateMountsRoot`, applies shell-default fallback, then `ValidateShell` (D-12; CFG-05) — same call order as today's `Load()`.
- **Mount defaults:** intentionally NOT seeded (Pitfall 8 / D-11 amended). `! grep -n 'mountplan\.' internal/config/plan.go` passes.
- **`Plan` body:** explicit `--config` path → `os.ReadFile` (hard fail on miss); global `~/.toolbox.yaml` → `os.ReadFile` with `os.IsNotExist` tolerance (Pitfall 5 — global is optional); project via `walkUp(searchFrom)` → `os.ReadFile` (hard fail on read error); bytes passed to `Merge`. HOME unset / unresolved is non-fatal in the global branch.

**File:** `internal/config/plan_test.go` — 135 → 245 lines (+110)

- 3 bridging `TestPlan*` tests un-skipped: `TestPlanCanonicalPipeline`, `TestPlanWalksUpFromSubdir`, `TestPlanExplicitOverrideShortCircuits` — assert against the real `*Config` returned by `Plan`.
- 2 new validation-rejection tests: `TestPlanRejectsInvalidShell`, `TestPlanRejectsRelativeMountsRoot` — pin the Validation tail (CFG-05).

## Commits

- `3efe9c8`: feat(08-02): wire Merge body — fresh viper.New + catalog defaults + validation tail
- `677a52d`: feat(08-02): wire Plan body + unskip 3 bridging tests + add 2 validation tests

## Verification

- `make go-test`: all packages green (cmd, internal/build, internal/catalog, internal/config, internal/container, internal/mountplan, internal/ui).
- `internal/config/plan_test.go::TestPlanCanonicalPipeline`, `::TestPlanWalksUpFromSubdir`, `::TestPlanExplicitOverrideShortCircuits`, `::TestPlanRejectsInvalidShell`, `::TestPlanRejectsRelativeMountsRoot` — all pass.
- No `t.Skip(` calls remain in `plan_test.go`.

## Deviations

- **Recovery deviation (orchestrator-applied):** the executor agent socket-disconnected immediately after writing the two task commits; the SUMMARY.md commit step did not run. The orchestrator wrote this SUMMARY.md retroactively, sourced from `git show --stat` of the two feat commits + verified `make go-test` green on the merged branch. No semantic change vs. plan; the work landed correctly. SUMMARY commit lands as a 3rd plan-02 commit on `gsd/phase-08-config-plan` (not in a worktree).

## Out of Scope (Carried Forward)

- `cmd/root.go::initConfig` thinning — Plan 04
- `Load()` deprecation wrapper — Plan 05
- DOCS-01 glossary entry + CLAUDE.md patches — Plan 06
