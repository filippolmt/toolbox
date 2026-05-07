---
phase: 08-config-plan
plan: 06
subsystem: docs
tags: [docs, glossary, context, claude-md, config-plan, deepening-pattern]
dependency_graph:
  requires:
    - "Phase 06 / Mount Plan glossary entry (sibling structural anchor)"
    - "Phase 07 / Tool Catalog glossary entry (sibling structural anchor)"
    - "08-04 (cmd/root.go::initConfig delegates to config.Plan; findProjectConfig deleted)"
    - "08-05 (config.Load deprecated to thin Plan(cwd, \"\") wrapper)"
  provides:
    - "DOCS-01 closed: CONTEXT.md ### Config Plan glossary entry, sibling depth + paragraph order to Mount Plan and Tool Catalog"
    - "CLAUDE.md §Architecture lines no longer literally false post-Plans 04 + 05 (D-20 exception scope honoured)"
    - "Three-Seam deepening pattern visible to future contributors: Mount Plan (Phase 06) → Tool Catalog (Phase 07) → Config Plan (Phase 08)"
  affects:
    - "CONTEXT.md (root domain glossary, +27 lines)"
    - "CLAUDE.md (3 line patches: Host CLI bullet, internal/config bullet, Config-load-order gotcha)"
tech-stack:
  added: []
  patterns:
    - "Glossary parity: every named Seam in the codebase carries a CONTEXT.md entry with identical paragraph order — definition / pipeline + owning package + Seams / why-the-term-exists"
    - "D-20 minimum-diff CLAUDE.md patches: only literally-false phrases are replaced; broader rewrite deferred to DOCS-02 (Phase 10) once Phase 09 (Session Plan) lands"
key-files:
  created:
    - ".planning/phases/08-config-plan/08-06-SUMMARY.md"
    - ".planning/phases/08-config-plan/deferred-items.md"
  modified:
    - "CONTEXT.md"
    - "CLAUDE.md"
decisions:
  - "Config Plan glossary entry mirrors Mount Plan + Tool Catalog verbatim in structure: 3 paragraphs, ~189 words, same tense/voice. Sibling depth (level-3 heading) is load-bearing — D-19 docs-test."
  - "CLAUDE.md patches scoped to lines that became literally false post-migration: cmd/root.go bullet (deleted findProjectConfig), internal/config bullet (Load no longer side-effect-free), Config-load-order gotcha (findProjectConfig source-of-truth claim). Broader Architecture rewrite deliberately deferred to DOCS-02 (Phase 10) per D-20 — would otherwise pre-empt Phase 09 (Session Plan) and stale-date again."
  - "Treated Config-load-order gotcha (line 89) as in-scope under the same D-20 exception — the bullet referenced `findProjectConfig` as the source of truth, identical literal-falsehood class to the Architecture bullet. Acceptance criterion `! grep -n 'findProjectConfig' CLAUDE.md` formalised this expectation. Documented as one cohesive D-20 patch in the commit message."
metrics:
  duration: "~12 minutes"
  completed: "2026-05-07"
  tasks_completed: 2
  files_changed: 2
  commits: 2
---

# Phase 08 Plan 06: DOCS-01 Glossary + CLAUDE.md D-20 patches Summary

DOCS-only closure plan for Phase 08: ship the `### Config Plan` glossary entry in `CONTEXT.md` (sibling to `### Mount Plan` and `### Tool Catalog`), and patch the three CLAUDE.md lines that became literally false during the Plans 04 + 05 migration. Broader Architecture rewrite stays deferred to DOCS-02 (Phase 10).

## What changed

### CONTEXT.md — `### Config Plan` glossary entry (+27 lines)

Appended after `### Tool Catalog`, mirroring the structural template Phase 06 / 07 established:

| Paragraph | Mount Plan | Tool Catalog | Config Plan |
|-----------|-----------|--------------|-------------|
| 1 — Definition | "full pipeline that turns a Config and a workspace path into typed bind set" | "canonical declaration of every bundled tool" | "full pipeline that turns the cobra `--config` flag plus host's `.toolbox.yaml` files into a fully-resolved, fully-validated `*Config`" |
| 2 — Pipeline + Owner + Seams | `defaults() → applyMountsRoot → mergeMounts → resolveAll → workspace bind → DooD mirror`. Owned by `internal/mountplan`. Seams: `mountplan.Plan(cfg, workspace)` + `mountplan.Merge(cfg)` | `Entries → Keys / BuildArg / Defaults / IsDefault / WriteCanonical`. Owned by `internal/catalog` | `viper-defaults → walk-up → file-load → tool-defaults → mount-defaults (no-op) → validate`. Owned by `internal/config`. Seams: `config.Plan(searchFrom, explicitOverride)` + `config.Merge(global, project, explicit []byte)` |
| 3 — Why the term exists | logic split across config + mount + container/lifecycle; bugs hid in handoffs | three parallel hand-maintained literals for the same 30 tools | logic split across `cmd/root.go::initConfig` (walk-up + viper-seeding) and `internal/config/Load` (unmarshal + validate); test ceremony around the global viper singleton |

Word count: 189 (within the 150–220 target band; Mount Plan = 147, Tool Catalog = 175). Each invocation now uses a fresh `*viper.Viper`, retiring `viper.Reset()` from the test surface — called out explicitly in the Why-paragraph because that property is the load-bearing change Plan 02 made and is invisible from the Seam signature alone.

### CLAUDE.md — three line patches (D-20 exception scope)

**Host CLI bullet (line 39).** Removed the deleted `findProjectConfig` reference; named `cmd/root.go::initConfig` as a thin call site delegating to `config.Plan(cwd, cfgFile)` (the Config Plan Seam, with a CONTEXT.md cross-reference). Cobra wiring + `cmd/shell.go` hot-path framing preserved.

**`internal/config` bullet (line 44).** Replaced "Pure data + validation; no filesystem side-effects in `Load()`" — false post-Plan-05 because `Load()` now wraps `Plan` which DOES walk the filesystem — with the accurate split: schema + validators in `config.go`, Plan/Merge Seams in `plan.go`, `Load()` as a deprecated wrapper retained for one release while subcommands migrate (Phase 09 sweep). Tool-catalog sentence preserved verbatim (Phase 07 work intact).

**Config-load-order gotcha (line 89).** Treated as the same D-20 exception class — the bullet still claimed `findProjectConfig in cmd/root.go is the source of truth`, identical literal-falsehood after Plan 04 deleted the helper. Replaced with `the Config Plan Seam (config.Plan in internal/config/plan.go) is the source of truth`.

## Verification

All `<acceptance_criteria>` grep gates from the PLAN execute green:

- `grep -nE '^### (Mount Plan|Tool Catalog|Config Plan)$' CONTEXT.md` returns 3 matches in line-number order Mount → Tool → Config (10, 28, 53). Sibling-depth + narrative-order contracts both honoured.
- `grep -c 'Why the term exists' CONTEXT.md` returns 3 (one per glossary entry).
- Both Seams named in CONTEXT.md: `config.Plan(searchFrom, explicitOverride)` + `config.Merge(global, project, explicit []byte)`.
- Pipeline tokens all present: `viper-defaults`, `walk-up`, `file-load`, `tool-defaults`, `validate`.
- `grep -c 'findProjectConfig' CLAUDE.md` returns 0 (literally-false references gone).
- `grep -cF 'no filesystem side-effects in `Load()`' CLAUDE.md` returns 0.
- `grep -c 'Config Plan' CLAUDE.md` returns 3 (Host CLI bullet + internal/config bullet + Config-load-order gotcha).
- Mount Plan + Tool Catalog references preserved (`Mount Plan`: 2 occurrences; `Tool source-of-truth lives in internal/catalog` preserved verbatim).
- `make go-test`: green across all packages (`cmd`, `internal/build`, `internal/catalog`, `internal/config`, `internal/container`, `internal/mountplan`, `internal/ui`).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 — Bug] CLAUDE.md line 89 (Config-load-order gotcha) referenced the deleted `findProjectConfig` helper as source-of-truth**
- **Found during:** Task 2 verification — `grep -c 'findProjectConfig' CLAUDE.md` returned 1 after the planned two patches, but the acceptance criterion is `! grep -n 'findProjectConfig' CLAUDE.md` (zero matches).
- **Issue:** The Config-load-order bullet in §Runtime container (line 89) still claimed `findProjectConfig in cmd/root.go is the source of truth`. `findProjectConfig` was deleted in Plan 04; the claim is literally false in the same way as the two §Architecture lines D-20 explicitly enumerated.
- **Decision:** D-20 is "patch literally-false lines"; the gotcha bullet falls inside that exception class even though the PLAN's `<read_first>` only enumerated lines 38–44. Patching it now is consistent with the D-20 charter and required by the existing acceptance criterion.
- **Fix:** Replaced with `the **Config Plan** Seam (config.Plan in internal/config/plan.go) is the source of truth`. Bullet structure + surrounding wording untouched.
- **Files modified:** `CLAUDE.md`
- **Commit:** `c71a8c5`

### Deferred Issues

**1. [Out of scope — Phase 09 sweep] Pre-existing `make go-lint` SA1019 deprecation warnings on `cmd/build.go:33` + `cmd/shell.go:36`**
- These are the deliberate early-warning signals from Plan 05's `config.Load` deprecation. They predate Plan 06 (worktree base `f28710f` already showed them) and are explicitly scoped to the Phase 09 (Session Plan) sweep — the patched `internal/config` bullet in CLAUDE.md names that resolution target.
- Logged at `.planning/phases/08-config-plan/deferred-items.md`.
- Not fixed here; doing so would pre-empt Phase 09's design conversation about how `cmd/*` consumes the resolved `*Config`.

## Authentication gates

None encountered (DOCS-only plan, no network or credential interactions).

## Phase 08 closure

With 08-06 shipped, every CFG-01..CFG-06 + DOCS-01 requirement closes:

| Requirement | Closed by |
|-------------|-----------|
| CFG-01 (Plan + Merge Seams in `internal/config`) | 08-02 |
| CFG-02 (per-call `viper.New()` retires `viper.Reset()`) | 08-02 + 08-05 |
| CFG-03 (mount-defaults stay a no-op on `*Config`) | 08-02 (delegated to Mount Plan) |
| CFG-04 (`cmd/root.go::initConfig` delegates to `config.Plan`) | 08-04 |
| CFG-05 (test surface migrates off viper singleton priming) | 08-05 |
| CFG-06 (`Load()` deprecated, one-release wrapper) | 08-05 |
| DOCS-01 (`### Config Plan` glossary entry) | 08-06 (this plan) |

Phase 09 (Session Plan) is the next deepening target: migrate `cmd/build.go`, `cmd/shell.go`, `cmd/stop.go` to consume the resolved `*Config` produced by `initConfig` directly, retire the `Load()` wrapper, and clear the two SA1019 lint warnings.

## Self-Check: PASSED

- File `CONTEXT.md` modified: FOUND (commit `a103dd8`).
- File `CLAUDE.md` modified: FOUND (commit `c71a8c5`).
- File `.planning/phases/08-config-plan/deferred-items.md` created: FOUND.
- Commit `a103dd8` exists in `git log`: FOUND.
- Commit `c71a8c5` exists in `git log`: FOUND.
- All grep acceptance criteria from PLAN: GREEN.
- `make go-test`: GREEN.
