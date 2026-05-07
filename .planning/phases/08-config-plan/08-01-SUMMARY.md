---
phase: 08-config-plan
plan: 01
subsystem: internal/config
tags: [config, plan-seam, walk-up, scaffold, tdd]
requires:
  - cmd/root.go::findProjectConfig (verbatim relocation source)
  - internal/config/config.go::Config / Mount / ValidateShell / ValidateMountsRoot
  - internal/mountplan/plan.go (Module template — banner pattern + Seam doc style)
provides:
  - internal/config/plan.go::Plan (signature stub — D-04)
  - internal/config/plan.go::Merge (signature stub — D-07)
  - internal/config/plan.go::walkUp (verbatim body — D-06)
  - internal/config/plan.go six section banners in D-03 order
  - internal/config/plan_test.go five CFG-04 walk-up invariant tests
affects:
  - none (Plan / Merge are stubs; cmd/root.go untouched, runtime call graph unchanged)
tech-stack:
  added: []
  patterns:
    - "Sectioned banner layout (Phase 06/07 lock): six `=` banners in D-03 order — Seam → Walk-Up → Defaults Seeding → File Load → Validation → Helpers."
    - "Test-via-public-API exception during scaffolding: 5 TestWalkUp* tests cross the unexported helper directly (legal because `package config`); 3 TestPlan* tests are t.Skip bridging stubs Plan 02 unskips."
    - "Pitfall 3 hygiene: only `t.Setenv` for HOME overrides; no non-restoring stdlib env mutation; no global viper reset."
key-files:
  created:
    - path: internal/config/plan.go
      role: "External Seam (Plan + Merge stubs) + Walk-Up helper relocated verbatim from cmd/root.go::findProjectConfig"
    - path: internal/config/plan_test.go
      role: "CFG-04 walk-up invariant tests (5) + Plan-level bridging stubs (3, t.Skip)"
  modified: []
decisions:
  - "Wired Plan to call walkUp via `_ = walkUp(searchFrom)` already in Task 1 (originally a Task 2 step) — required to satisfy golangci-lint `unused` checker at Task 1's commit boundary while Plan body is still a stub. Behaviour-equivalent to the plan's spec: walkUp result is intentionally discarded, error path unchanged."
metrics:
  duration: ~6m wall
  tasks_completed: 2
  files_changed: 2
  tests_added: 8 (5 active, 3 skipped)
  completed: 2026-05-06
---

# Phase 08 Plan 01: Scaffold internal/config Plan + Merge Seams Summary

Scaffolded `internal/config/plan.go` with the locked D-03 six-banner layout, declared the D-04/D-07 Plan + Merge signatures as stub-bodied seams, and relocated the walk-up logic verbatim from `cmd/root.go::findProjectConfig` into a new unexported `walkUp` helper. Pinned five CFG-04 walk-up termination invariants via `internal/config/plan_test.go` using `t.Setenv("HOME", ...)` and `t.TempDir()` only — no global env mutation, no global viper reset. Added three Plan-level bridging stubs marked `t.Skip` so the test surface is grep-shaped right while waiting for Plan 02 to wire the body.

## Commits

| Task | Hash      | Type | Description                                                                                |
| ---- | --------- | ---- | ------------------------------------------------------------------------------------------ |
| 1    | `a3f04c5` | feat | scaffold `internal/config/plan.go` with Plan/Merge seams + six section banners + empty walkUp |
| 2    | `22e9583` | test | relocate walkUp body verbatim from `cmd/root.go::findProjectConfig` + pin CFG-04 invariants |

## What Landed

### `internal/config/plan.go`

- **Six section banners** in D-03 order:
  1. `Seam (Plan + Merge)` — `Plan` and `Merge` declarations.
  2. `Walk-Up` — `walkUp` helper.
  3. `Defaults Seeding` — empty (Plan 02 lands `seedToolDefaults`).
  4. `File Load` — empty (Plan 02 lands global / project read helpers if extracted).
  5. `Validation` — empty (Plan 02 lands the validation tail helper if extracted).
  6. `Helpers` — empty (catch-all).
- **`Plan(searchFrom string, explicitOverride string) (*Config, error)`**: D-04 signature. Body returns `nil, errors.New("config.Plan: not yet implemented")`. Calls `walkUp(searchFrom)` once with the result discarded so the unused-symbol lint check stays green during the scaffolding window.
- **`Merge(global, project, explicit []byte) (*Config, error)`**: D-07 signature. Body returns `nil, errors.New("config.Merge: not yet implemented")`.
- **`walkUp(start string) string`**: body relocated verbatim from `cmd/root.go::findProjectConfig` lines 121-145. Same `home, _ := os.UserHomeDir()` (silent error discard — Pitfall 5), same `filepath.Clean`, same `info.IsDir()` skip, same `parent == cur` filesystem-root short-circuit. Doc comment adapted to drop the cmd-level "global is handled separately" sentence and to call out the Pitfall 5 silent-failure contract.

### `internal/config/plan_test.go`

Five active tests pinning CFG-04 walk-up invariants directly through the unexported helper (test file lives in `package config`):

| Test                                          | Invariant                                                                  |
| --------------------------------------------- | -------------------------------------------------------------------------- |
| `TestWalkUpStopsAtHome`                       | Walk-up terminates at HOME when HOME has a `.toolbox.yaml` (RESEARCH inv. 1) |
| `TestWalkUpReturnsClosestMatch`               | Closest ancestor wins when two ancestors carry `.toolbox.yaml` (inv. 3)    |
| `TestWalkUpStopsAtFilesystemRoot`             | `parent == cur` short-circuit terminates the loop at `/` (inv. 2)          |
| `TestWalkUpHomeUnsetContinuesToRoot`          | `HOME=""` (Pitfall 5) does not block walk-up — planted file still found    |
| `TestWalkUpIgnoresDirectoryNamedToolboxYaml`  | `!info.IsDir()` guard skips a directory named `.toolbox.yaml`              |

Three skipped bridging stubs (Plan 02 unskips):

- `TestPlanExplicitOverrideShortCircuits`
- `TestPlanWalksUpFromSubdir`
- `TestPlanCanonicalPipeline`

## Walk-Up Relocation: Verbatim Move

The `walkUp` body is byte-for-byte identical to the source body in `cmd/root.go` lines 121-145:

```go
func walkUp(start string) string {
    home, _ := os.UserHomeDir()
    cur := filepath.Clean(start)
    for {
        if home != "" && cur == home { return "" }
        candidate := filepath.Join(cur, ".toolbox.yaml")
        if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
            return candidate
        }
        parent := filepath.Dir(cur)
        if parent == cur { return "" }
        cur = parent
    }
}
```

`cmd/root.go::findProjectConfig` is intentionally **not deleted** in this plan. Per RESEARCH §Migration Commit Order Validation, the deletion happens in Plan 04 commit 6 once `cmd/root.go::initConfig` has been thinned and Plan owns the call graph end-to-end. Keeping `findProjectConfig` in place through commits 1-5 means every intermediate commit still passes the existing `cmd/config_test.go` tests against the unmodified call graph, with no temporary export of `walkUp` required.

## Bridging-Stub Strategy

Three Plan-level tests exist as `t.Skip` placeholders so the test file is grep-shaped correctly today (and Plan 02 only needs to fill bodies, not add new tests):

```go
func TestPlanExplicitOverrideShortCircuits(t *testing.T) {
    t.Skip("Plan 02: Plan body must be wired before this test can assert *Config")
}
```

These are **intentional bridging stubs**, not technical debt. Plan 02 lands the body wiring (defaults seeding + viper merge) and unskips them with the assertions specified in 08-RESEARCH.md §Code Examples §Example 3.

## Threat Model Status

| Threat ID | Disposition | Status                                                                             |
| --------- | ----------- | ---------------------------------------------------------------------------------- |
| T-08-01   | accept      | Plan stubbed; full os.ReadFile path arrives in Plan 02. Skeleton does not widen surface. |
| T-08-02   | mitigate    | Verbatim relocation preserves filepath.Clean, HOME-stop, root-stop, IsDir guard. CFG-04 invariants now pinned by 5 dedicated tests (previously implicit). No `filepath.EvalSymlinks` (out of scope per RESEARCH). |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocker] Wire `_ = walkUp(searchFrom)` in Task 1 instead of Task 2**
- **Found during:** Task 1 verification (`make go-lint`)
- **Issue:** Task 1 alone declared `walkUp` but did not reference it from `Plan`, so golangci-lint's `unused` checker flagged it: `internal/config/plan.go:46:6: func walkUp is unused (unused)`. Acceptance criterion `make go-lint exits zero` would have failed at Task 1's commit boundary.
- **Fix:** Brought forward the `if explicitOverride == "" { _ = walkUp(searchFrom) }` line that Task 2 step 2 was going to add anyway. Net effect on Task 2 was zero — that step was already in scope. Task 1 commit now lints clean; Task 2 only had to fill the walkUp body and the test file.
- **Files modified:** `internal/config/plan.go` (Task 1 commit a3f04c5)

### Out-of-Scope Discoveries

None — package surface, runtime behaviour, and `cmd/root.go` are all unchanged.

## Plan 02 Hand-Off

Plan 02 picks up with:

1. **Defaults seeding** — land `seedToolDefaults(v *viper.Viper)` under the `Defaults Seeding` banner; mirrors `cmd/root.go::setDefaults` but writes into a local `*viper.Viper` (D-09) instead of the global one.
2. **Plan body wiring** — replace the `not yet implemented` stub with the read-global → read-project (via the existing `walkUp`) → read-explicit → unmarshal → validate-tail flow. Defaults application + ValidateShell + ValidateMountsRoot run after unmarshal.
3. **Unskip the three TestPlan* tests** with the assertions specified in 08-RESEARCH.md §Code Examples §Example 3 (canonical pipeline, explicit-override short-circuit, walks-up-from-subdir).
4. **Do not touch `cmd/root.go::findProjectConfig` / `setDefaults`** — those go in Plan 04.

`internal/config/plan.go` is grep-shaped right (banners + signatures + walk-up body), so Plan 02 only fills bodies; no new top-level declarations are needed beyond `seedToolDefaults`.

## Self-Check: PASSED

- `internal/config/plan.go` — FOUND
- `internal/config/plan_test.go` — FOUND
- Commit `a3f04c5` (Task 1) — FOUND in worktree branch
- Commit `22e9583` (Task 2) — FOUND in worktree branch
- `make go-test` — green (all 8 packages OK)
- `make go-lint` — green (0 issues)
- 5 TestWalkUp* tests pass (verbose verified); 3 TestPlan* tests skip
- `cmd/root.go::findProjectConfig` still present (deletion deferred to Plan 04)
