---
phase: 08-config-plan
plan: 05
subsystem: internal/config
tags: [config, deprecation, test-migration, load, plan, viper]
requires:
  - 08-02 # Plan/Merge bodies (delegate target)
  - 08-03 # merge_test.go (provides byte-equivalent coverage of deleted Load tests)
  - 08-04 # cmd/root.go thinned (no consumers of the old Load body remained in cmd)
provides:
  - "config.Load() as deprecated thin wrapper around config.Plan(cwd, \"\")"
  - "Load() // Deprecated: doc-comment naming Phase 09 / Phase 10 as migration target"
  - "config_test.go + config_shell_test.go migrated off the viper-singleton priming pattern"
  - "TestLoadSmoke — clean-fs end-to-end regression guard for the deprecated wrapper"
affects:
  - cmd/build.go (unchanged — call site preserved by Load() signature)
  - cmd/shell.go (unchanged — call site preserved)
  - cmd/stop.go (unchanged — call site preserved)
tech-stack:
  added: []
  patterns:
    - "Deprecated wrappers carry godoc-conventional `// Deprecated: ` lines and cite the migration target plan(s)"
    - "Tests that primed the global viper singleton via viper.Reset() + viper.ReadConfig(bytes) are dead — equivalent coverage moved into Merge byte-driven table tests (Plan 03)"
key-files:
  created: []
  modified:
    - internal/config/config.go (Load body 60→7 lines, doc-comment Deprecated, viper+catalog imports dropped, os import added)
    - internal/config/config_test.go (3 deletes + helper delete + 1 add + 2 retained; bytes/strings/viper imports dropped, os/path/filepath added)
    - internal/config/config_shell_test.go (3 deletes + 3 retained; bytes/viper imports dropped)
decisions:
  - "Test migration is delete-not-rewrite. RESEARCH §Test Migration Surface proves merge_test.go covers every byte-input scenario: pure_defaults, single_tool_disable_preserves_others, mounts_root_bare_tilde_rejected, mounts_root_relative_rejected, shell_default_zsh, shell_explicit_bash, shell_invalid_rejected. Re-implementing them as Load() smoke tests would re-introduce the global viper anti-pattern."
  - "Single TestLoadSmoke is the regression guard. Calls Load() under t.TempDir HOME + t.TempDir CWD. Asserts cfg.Shell==\"zsh\" and every catalog tool defaults to true. Catches the most likely future failure mode (someone modifies Load() to do anything other than `return Plan(cwd, \"\")`)."
  - "tools.go (DefaultTools/IsDefaultTools shims over catalog) is intentionally NOT deleted. Phase 07 D-17 + Phase 08 D-02 leave them as-is — they are still consumed by cmd/build.go (image-hash decision) and TestIsDefaultTools."
metrics:
  duration_min: 85
  completed: 2026-05-07
---

# Phase 08 Plan 05: Load() Deprecation Summary

`config.Load()` is now a deprecated thin wrapper around `config.Plan(cwd, "")`. Per-call test priming of the global viper singleton is gone; equivalent coverage lives in `merge_test.go` (Plan 03) and the new `TestLoadSmoke` guards the wrapper itself.

## What Shipped

### internal/config/config.go

- **Load body shrank from 60 lines to 7.** Old body called `viper.Unmarshal` against the global singleton, then ran `ValidateMountsRoot` → shell-default fallback → `ValidateShell` → tool-defaults backstop loop. New body: `cwd, _ := os.Getwd(); return Plan(cwd, "")`. The pipeline lives entirely behind `Plan` / `Merge` (Plan 02), which uses a fresh `viper.New()` per call (D-09).
- **`// Deprecated:` godoc-conventional doc comment** sits immediately above `func Load`, citing Phase 09 (Session Plan) as the migration target and Phase 10 as the deletion target. IDEs surface deprecation warnings at every `config.Load()` call site automatically.
- **Imports trimmed.** `github.com/spf13/viper` and `github.com/filippolmt/toolbox/internal/catalog` dropped (Plan owns both now). Added `os` for `os.Getwd`. Final import set: `fmt`, `os`, `path`, `slices`, `strings` — pure stdlib.
- **Validators, types, constants untouched.** `ValidateShell`, `ValidateMountsRoot`, `Config`, `Mount`, `HomeMountParents`, `SupportedShells` all retained verbatim. The deprecation contract is body-only.

### internal/config/config_test.go

- **Deleted** (4 tests + 1 helper, all covered byte-equivalently in `merge_test.go`):
  - `TestLoadWithoutConfig` → `merge_test.go::pure_defaults`
  - `TestLoadUserOverridePreservesOtherTools` → `merge_test.go::single_tool_disable_preserves_others`
  - `TestLoadMountsRootBareTildeRejected` → `merge_test.go::mounts_root_bare_tilde_rejected`
  - `TestLoadMountsRootRelativeRejected` → `merge_test.go::mounts_root_relative_rejected`
  - `setToolsDefaults` helper (only the deleted tests used it)
- **Retained:** `TestIsDefaultTools`, `TestToolBuildArgGo` (catalog shims, orthogonal to Load).
- **Added:** `TestLoadSmoke`. Sets `HOME=t.TempDir()`, `CWD=t.TempDir()` via `os.Chdir` + `t.Cleanup`, calls `Load()`, asserts `cfg.Shell == "zsh"` and `cfg.Tools[k] == true` for every `catalog.Keys()` entry. Includes a fixture sanity check that `~/.toolbox.yaml` does not exist.
- **Imports:** dropped `bytes`, `strings`, `viper`. Added `os`, `path/filepath`. Final: `os`, `path/filepath`, `testing`, `catalog`.

### internal/config/config_shell_test.go

- **Deleted** (3 tests, all covered byte-equivalently in `merge_test.go`):
  - `TestLoadDefaultShellIsZsh` → `merge_test.go::shell_default_zsh`
  - `TestLoadShellBash` → `merge_test.go::shell_explicit_bash`
  - `TestLoadShellInvalid` → `merge_test.go::shell_invalid_rejected`
- **Retained:** `TestValidateShellAcceptsSupported`, `TestValidateShellRejectsUnknown`, `TestKnownToolsIncludesZsh` (validator + catalog unit tests).
- **Imports:** dropped `bytes`, `viper`. Final: `strings`, `testing`, `catalog`.

## Test Migration Tally

| File                          | Before | After | Delta                                              |
| ----------------------------- | ------ | ----- | -------------------------------------------------- |
| `config_test.go`              | 5      | 3     | -3 byte-input tests, -1 helper, +1 smoke           |
| `config_shell_test.go`        | 6      | 3     | -3 byte-input tests                                |
| **internal/config/ total**    | 11     | 6     | **-7 deleted, +1 added (TestLoadSmoke). Net: -6** |

Coverage is preserved: the 7 deleted tests are equivalent-or-stronger covered by the byte-driven table cases in `merge_test.go` (Plan 03 produced 20+ scenarios). The single addition is the wrapper-integrity guard.

## Pitfall Retired: Pitfall 6

RESEARCH §Pitfall 6 flagged the `viper.Reset() + viper.ReadConfig(bytes) + Load()` priming antipattern as the bridge that would silently start producing empty configs once Load() began delegating to Plan (Plan uses `viper.New()`, ignores the global singleton). After this plan:

- `grep -rn 'viper\.' internal/config/*_test.go` → matches only doc comments (zero code calls).
- `grep -rn 'viper\.Reset' internal/config/` → zero matches.

The antipattern is no longer present anywhere under `internal/config/`. Future regressions cannot reintroduce it without explicit re-import.

## Verification

| Check                                                               | Result |
| ------------------------------------------------------------------- | ------ |
| `make go-test` (full)                                               | PASS   |
| `make go-lint`                                                      | PASS   |
| `grep -n 'TestLoadWithoutConfig' internal/config/config_test.go`    | absent (only in comment) |
| `grep -n 'setToolsDefaults' internal/config/config_test.go`         | absent |
| `grep -n '"github.com/spf13/viper"' internal/config/config.go`      | absent |
| `grep -n '"github.com/filippolmt/toolbox/internal/catalog"' config.go` | absent |
| `grep -n '^// Deprecated: Load is a thin compatibility wrapper'`    | matches |
| `grep -n 'Plan(cwd, "")' internal/config/config.go`                 | matches |
| `grep -n 'TestLoadSmoke' internal/config/config_test.go`            | matches |

## Deviations from Plan

None — plan executed exactly as written.

The PLAN's `wc -l internal/config/config.go ~95 lines` estimate was slightly off (actual 125). The discrepancy comes from preserving the full doc comments on `Config` / `Mount` / `HomeMountParents` / `SupportedShells` / `ValidateShell` / `ValidateMountsRoot`, which the plan body did not touch. Load body itself shrank exactly as planned (60 → 7 lines = -53 LOC of body; +14 LOC of new doc comment = net -29). Not flagged as a deviation because the LOC target was an estimate, not a contract; the substantive acceptance criteria (Deprecated comment present, imports trimmed, validators retained, `make go-test` green) all hold.

## Hand-Off to Plan 06

Plan 06 (DOCS-01) updates the human-facing artifacts:

- **CLAUDE.md root** — the "Internal packages" entry for `internal/config` should reflect that `Load()` is deprecated and `Plan` / `Merge` are the new external seams. Today it still says "Pure data + validation; no filesystem side-effects in `Load()`" — the `Load()` reference is now misleading.
- **CONTEXT.md / glossary** — add a "Plan / Merge" glossary entry documenting the seam pair (Plan = filesystem walk-up + IO; Merge = pure byte-level merge; both produce a `*Config`).
- **Phase 09 docket** — record that the Phase 09 Session Plan owes `cmd/build.go` / `cmd/shell.go` / `cmd/stop.go` migration off `config.Load()` onto the `*Config` that `cmd/root.go::initConfig` already produces. The deprecation comment cites Phase 09 by name, so the link is bidirectional.

No code changes are owed to Phase 08 from here — Plan 06 is the closing docs-only commit.

## Self-Check: PASSED

- [x] `internal/config/config.go` modified — Load body wrapper, imports trimmed, deprecation comment present.
- [x] `internal/config/config_test.go` rewritten — 3 deletes, 1 helper delete, 1 add, 2 retained.
- [x] `internal/config/config_shell_test.go` rewritten — 3 deletes, 3 retained.
- [x] Commit `72b739a` (Task 1: test migration) exists in worktree git log.
- [x] Commit `f75cefc` (Task 2: Load body deprecation) exists in worktree git log.
- [x] `make go-test` and `make go-lint` both exit zero post-commit.
- [x] No modifications to STATE.md, ROADMAP.md, cmd/*, internal/config/plan.go, internal/config/plan_test.go, internal/config/merge_test.go (per parallel-execution constraints).
