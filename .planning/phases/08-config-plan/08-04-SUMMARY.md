---
phase: 08-config-plan
plan: 04
subsystem: cmd
tags: [refactor, viper, cobra, config-pipeline]
requires:
  - "internal/config.Plan (Plan 02)"
  - "internal/config.walkUp (Plan 01)"
  - "internal/config.seedToolDefaults (Plan 01/02)"
provides:
  - "cmd/root.go::initConfig — thin call site delegating to config.Plan"
  - "cmd/root.go::cfg (package-level *config.Config var)"
affects:
  - "cmd/build.go, cmd/shell.go, cmd/stop.go (no code change — keep using config.Load(); Plan 05 makes Load a wrapper around Plan)"
  - "Phase 09 sweep target: subcommand call sites move from config.Load() to the cfg var directly"
tech_stack:
  added: []
  patterns:
    - "Package-level resolved-config var populated by cobra OnInitialize"
key_files:
  created: []
  modified:
    - cmd/root.go
    - cmd/config_test.go
decisions:
  - "D-13: cmd/root.go::initConfig is a thin call site that delegates to config.Plan and stashes the result on a package-level *Config var."
  - "D-14: --config flag binding (cobra) stays in cmd/root.go — flag wiring is a cobra concern, not a config-pipeline concern."
  - "Pitfall 6 option (3): rewrite tests to assert against the package-level cfg var rather than the global viper singleton — cleanest path now that initConfig no longer touches viper."
metrics:
  duration: "~10 min"
  completed: "2026-05-07"
  files_modified: 2
  loc_delta: "+70 / -157 (net -87)"
  tasks_completed: 2
---

# Phase 08 Plan 04: cmd/root.go Thinning + cmd/config_test.go Rewrite Summary

Thinned `cmd/root.go::initConfig` to a 6-line call site that delegates to `config.Plan(cwd, cfgFile)` and stashes the resolved `*Config` on a package-level `cfg` var; deleted `findProjectConfig` and `setDefaults`; rewrote 5 tests in `cmd/config_test.go` to assert via the new `cfg` var instead of the now-untouched global viper singleton.

## What Shipped

### Task 1 — `cmd/root.go` (commit `b98a44d`)
- `initConfig` body shrinks from ~50 lines to 6: `os.Getwd` → `config.Plan(cwd, cfgFile)` → `cfg = c` (or `os.Exit(1)` on err).
- Deleted `findProjectConfig` (logic now lives in `internal/config.walkUp` from Plan 01).
- Deleted `setDefaults` (logic now lives in `internal/config.seedToolDefaults` from Plan 01/02).
- New package-level `var cfg *config.Config` declared next to `var cfgFile string`.
- Imports dropped: `bytes`, `path/filepath`, `github.com/spf13/viper`, `github.com/filippolmt/toolbox/internal/catalog`.
- Imports added: `github.com/filippolmt/toolbox/internal/config`.
- `--config` flag binding (`StringVar(&cfgFile, "config", ...)`) preserved untouched at the same call site (D-14).
- `usageError` machinery + `Execute()` + cobra root command untouched.
- File is now 86 LOC (down from 158).

### Task 2 — `cmd/config_test.go` (commit `2fbc898`)
- All 5 tests rewritten to assert on `cfg.MountsRoot` / `cfg.Tools[k]` instead of `viper.GetString` / `viper.GetBool`.
- `viper` import removed; zero `viper.` calls in actual code (only in comments referring to historical bug context).
- New `resetCmdState(t, origCfgFile)` helper clears `cfgFile` and `cfg` between tests — replaces the old `viper.Reset()` pattern (D-09: no global viper churn).
- `TestInitConfigProjectFileStopsAtHome` no longer calls `findProjectConfig` directly (deleted in Task 1) — instead asserts that `cfg.MountsRoot` resolves to the global value, with the deeper walk-up-stops-at-HOME invariant pinned by `internal/config/plan_test.go::TestWalkUpStopsAtHome` (Plan 01).
- `t.Setenv("HOME", ...)` and `os.Chdir` patterns retained — these are integration-of-cobra-init tests that genuinely walk the filesystem.

## Verification

| Gate | Result |
|------|--------|
| `make go-test` (TOOLBOX_HOST_WORKSPACE=$PWD) | green — all 8 packages pass |
| `make go-lint` | `0 issues.` |
| `! grep -nE '^func (findProjectConfig\|setDefaults)' cmd/root.go` | empty (deleted) |
| `grep -n 'config\.Plan(cwd, cfgFile)' cmd/root.go` | matches at line 80 |
| `grep -n 'var cfg \*config\.Config' cmd/root.go` | matches at line 21 |
| `grep -n 'StringVar(&cfgFile, "config"' cmd/root.go` | matches at line 68 (D-14 preserved) |
| `! grep -nE 'viper\.(SetDefault\|SetEnvPrefix\|MergeConfig\|ReadInConfig\|AddConfigPath\|AutomaticEnv)' cmd/*.go` | empty |
| `! grep -nE '(ValidateMountsRoot\|ValidateShell)' cmd/*.go` | empty (CFG-05 holds) |
| `! grep -n '"github.com/spf13/viper"' cmd/root.go cmd/config_test.go` | empty |
| `! grep -n 'viper\.' cmd/config_test.go` (excluding comments) | empty (only 2 matches, both in `//` lines) |
| `grep -c '^func TestInitConfig' cmd/config_test.go` | 5 |
| `grep -c 'cfg\.' cmd/config_test.go` | 10 (≥ required 8) |
| `grep -c 'resetCmdState' cmd/config_test.go` | 7 (≥ required 5) |
| `grep -n 'cfg = nil' cmd/config_test.go` | matches in resetCmdState |

## Deviations from Plan

None — plan executed exactly as written. Sub-command audit confirmed zero `viper.Get*` hits in `cmd/build.go` / `cmd/shell.go` / `cmd/stop.go`, so the transitional `viper.Set` shim hinted at in RESEARCH §A4 was not required (matches plan's "Important verified fact" note).

## Notable Findings

- `cmd/config_test.go` retains 2 textual matches for `viper.` — both are inside `//`-style comments referencing the previous bug history (`viper.Reset()` and `viper.SetConfigFile`). The CFG-02 audit semantics target callable code; comments stay because they document why the new pattern exists.
- Per plan §verification_strategy, Task 1's intermediate state would have failed the test suite if committed alone (the old `cmd/config_test.go` was still asserting on the now-untouched viper singleton). Task 1 + Task 2 land back-to-back in the same wave/PR; the executor staged Task 1's edits and committed them, then immediately executed Task 2 before running the full test gate. Both gates run after Task 2.

## Hand-off to Plan 05

Plan 05 (`08-05-PLAN.md`) deprecates `internal/config.Load()` to a wrapper around `config.Plan` so subcommands keep working without touching `cmd/build.go` / `cmd/shell.go` / `cmd/stop.go`. Phase 09 (Session Plan) is the eventual sweep that moves those call sites onto the `cfg` package-level var directly.

## Self-Check: PASSED

- `cmd/root.go` exists at 86 LOC with `config.Plan(cwd, cfgFile)` at line 80 and `var cfg *config.Config` at line 21 — verified.
- `cmd/config_test.go` exists with 5 `TestInitConfig*` tests asserting via `cfg.*` — verified.
- Commit `b98a44d` (refactor 08-04: thin cmd/root.go) — verified in `git log`.
- Commit `2fbc898` (test 08-04: rewrite cmd/config_test.go) — verified in `git log`.
- `make go-test` + `make go-lint` green against worktree — verified.
