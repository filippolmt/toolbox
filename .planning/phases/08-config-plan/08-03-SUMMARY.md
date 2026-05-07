---
plan: 08-03
phase: 08
slug: config-plan
status: complete
wave: 3
created: 2026-05-07
requirements: [CFG-03, CFG-06]
threat_model_ids: [T-08-03, T-08-04]
tags: [config, testing, merge, table-driven]
key_files:
  created:
    - internal/config/merge_test.go
  modified: []
key_decisions:
  - "Byte-literal-only test surface for Merge — CFG-06 satisfied"
  - "No env-precedence subtest (Pitfall 1: AutomaticEnv does not round-trip through Unmarshal)"
  - "Subtests sequential, no t.Parallel() — keeps the table form drift-proof against future contributors who might add t.Setenv"
metrics:
  completed_date: 2026-05-07
  task_count: 1
  file_count: 1
  test_count: 13
---

# Plan 08-03 Summary — Merge Byte-Input Table Test

## One-liner

Added `internal/config/merge_test.go` with `TestMergeScenarios` — 13 byte-literal subtests exercising `Merge(global, project, explicit []byte) (*Config, error)` with zero filesystem touches, zero env-var manipulation, and zero `viper.*` references (CFG-06 grep gates).

## What Shipped

**File:** `internal/config/merge_test.go` (new, 144 lines)

Single test function `TestMergeScenarios` driving a table of 13 cases:

| # | Name | What it locks in |
|---|------|------------------|
| 1 | `pure_defaults` | Empty bytes → all catalog tools true, Shell="zsh" |
| 2 | `global_only_disables_gcloud` | global flips one tool; others stay default |
| 3 | `project_only_disables_go` | project flips one tool; others stay default |
| 4 | `project_overrides_global` | gcloud=false (global) vs gcloud=true (project) → project wins |
| 5 | `explicit_override_short_circuits_layers` | `--config` bytes ignore global+project |
| 6 | `single_tool_disable_preserves_others` | Iterates `catalog.Keys()` to assert all non-flipped keys remain true |
| 7 | `shell_default_zsh` | Empty/comment-only project → Shell="zsh" |
| 8 | `shell_explicit_bash` | `shell: bash` → Shell="bash" |
| 9 | `shell_invalid_rejected` | `shell: fish` → err contains "fish" |
| 10 | `mounts_root_bare_tilde_rejected` | `mounts_root: "~"` → err contains "isolation" |
| 11 | `mounts_root_relative_rejected` | `mounts_root: ./relative` → err contains "mounts_root" |
| 12 | `mounts_root_valid_absolute` | `mounts_root: /opt/state` → cfg.MountsRoot |
| 13 | `mounts_root_valid_home_relative` | `mounts_root: ~/toolbox-state` → cfg.MountsRoot |

Imports: `strings`, `testing`, `github.com/filippolmt/toolbox/internal/catalog` — exactly 3 (acceptance gate).

## CFG-06 Grep Gates (all green)

- `! grep -n 't\.TempDir' internal/config/merge_test.go` ✓
- `! grep -n 'os\.Setenv\|t\.Setenv' internal/config/merge_test.go` ✓
- `! grep -n 'viper\.' internal/config/merge_test.go` ✓ (banner originally said "viper.Get* layer"; rephrased to "Get-style accessor layer" so the grep stays clean while the Pitfall 1 reference survives — Rule 1-style inline fix during verification, not a deviation)
- `grep -c 'TestMergeScenarios/'` from `go test -v` returns 26 (=== RUN + --- PASS each = 13 subtests × 2 lines)

## Verification

- `make go-test` — green across all packages (cmd, build, catalog, config, container, mountplan, ui).
- `make go-lint` — `0 issues.`
- All 13 subtests pass under `TestMergeScenarios`.

## Commit

- `62f44d3`: test(08-03): add Merge byte-input table test (CFG-06)

## Deviations

**Inline rewording (not a Rule deviation, but worth recording):** the plan's verbatim file banner contained the phrase "the viper.Get* layer in cmd/*". The CFG-06 acceptance gate is `! grep -n 'viper\.' internal/config/merge_test.go`, which the literal phrase trips on. Rephrased to "the Get-style accessor layer in cmd/*" — preserves the Pitfall 1 reference while satisfying the strict grep gate the plan itself requires. Test semantics unchanged.

## Threat Model

T-08-03 / T-08-04 mitigations satisfied:
- Each subtest calls `Merge` with three byte slices and inspects the returned `*Config` — no shared `*viper.Viper`, no shared `*Config`, no shared mutable state across subtests.
- Test never calls `os.Setenv` / `t.Setenv` (Pitfall 3) so a future move to `t.Parallel()` cannot accidentally race against viper's process-wide env reads.

## Hand-off

- **Plan 04 (cmd/root.go thinning + cmd/config_test.go rewrite):** unblocked. The Plan / Merge seam is now both implemented (Plan 02) and externally verified (this plan). Plan 04 can replace `cmd/root.go::initConfig` with a `config.Plan(cwd, cfgFile)` call and migrate `cmd/config_test.go` to byte-input fixtures.
- **Plan 05 (Load() deprecation wrapper):** decides what to do with `internal/config/config_test.go` and `config_shell_test.go`. Several of their cases are now semantically duplicated by `merge_test.go` cases 1, 6, 7, 8, 9, 10, 11 — Plan 05 owns the prune/keep call, this plan does not pre-empt it.

## Out of Scope (Carried Forward)

- `cmd/root.go::initConfig` thinning — Plan 04
- `Load()` deprecation wrapper — Plan 05
- Pruning `internal/config/config_test.go` + `config_shell_test.go` redundancies — Plan 05
- DOCS-01 glossary entry + CLAUDE.md patches — Plan 06

## Self-Check: PASSED

- `internal/config/merge_test.go` — FOUND.
- Commit `62f44d3` — FOUND in `git log`.
- Subtest count = 13 (verified by `go test -v -run TestMergeScenarios`).
- Grep gates: TempDir / Setenv / viper. — all empty (verified before commit).
