---
phase: 07-tool-catalog
plan: 03
subsystem: tool-catalog
tags: [catalog, refactor, single-source-of-truth, CAT-02, CAT-03]
requires:
  - 07-01  # internal/catalog package + accessors
  - 07-02  # tag.go consumes catalog
provides:
  - Sole source of truth for tool keys + Dockerfile build-args lives in internal/catalog
  - Adding a tool requires exactly one catalog entry + one Dockerfile install layer (CAT-03)
  - internal/config/tools.go reduced to 13-line thin-shim delegating to catalog
affects:
  - cmd/root.go
  - cmd/config_test.go
  - internal/build/tag.go
  - internal/config/config.go
  - internal/config/tools.go
  - internal/config/config_test.go
  - internal/config/config_shell_test.go
  - internal/catalog/catalog_test.go
tech-stack:
  added: []
  patterns:
    - "Function-shim delegation (DefaultTools / IsDefaultTools forward to catalog.Defaults / catalog.IsDefault) — preserves call-site ergonomics for callers that already imported internal/config for the Config type, while keeping the catalog as the lone data declaration"
key-files:
  created: []
  modified:
    - cmd/root.go
    - cmd/config_test.go
    - internal/build/tag.go
    - internal/config/config.go
    - internal/config/tools.go
    - internal/config/config_test.go
    - internal/config/config_shell_test.go
    - internal/catalog/catalog_test.go
decisions:
  - "Option A (thin shim) chosen over Option B (full move): preserves call-site ergonomics — every caller that already imports internal/config for the Config type continues to use config.DefaultTools / config.IsDefaultTools without a second import; satisfies D-05 because the shim is function delegation, not a parallel data declaration."
  - "Same-package callers inside internal/config (config_test.go, config_shell_test.go) migrated to catalog.Keys() / catalog.BuildArg() as part of Task 3's literal deletion — necessary to keep that commit compiling. Plan 07-03's pre-flight grep used the `config.` prefix and missed bare-identifier same-package references; this is documented under Deviations."
metrics:
  duration: "~28 minutes"
  completed: "2026-05-06"
  tasks: 3
  files_modified: 8
  commits: 3
---

# Phase 07 Plan 03: Legacy Tool-Literal Removal Summary

Delete the legacy `KnownTools` slice + `ToolBuildArg` map from `internal/config/tools.go`; migrate every remaining caller to `internal/catalog`; reduce `internal/config/tools.go` to a 13-line thin-shim file. Phase 07 single-source-of-truth invariant (CAT-02) is now structurally enforced — the catalog is the only declaration, and adding a tool is a single-file edit (CAT-03).

## Outcome

After this plan, the only place a tool is declared is `internal/catalog/catalog.go::Entries`. The Dockerfile install layer (`internal/build/assets/Dockerfile`) carries the runtime install pinning. There is no third edit site in `internal/config/tools.go`, `internal/build/tag.go`, or `cmd/root.go` — those packages all consume the catalog accessors (`Keys`, `BuildArg`, `Defaults`, `IsDefault`).

The atomic-commit guarantee held throughout: each of Tasks 1, 2, 3 individually leaves the tree compilable and `make go-test`-green. The corrected ordering (Task 2 — bridging-test removal — before Task 3 — literal deletion) was the structural fix for the planner-checker BLOCKER 1 raised on the original draft.

## Task-by-task (chosen disposition: Option A thin shim)

### Task 1 — caller sweep (commit `c0fe72d`)

Migrated the five PATTERNS.md call sites off `config.KnownTools` / `config.IsDefaultTools` to the catalog accessors. After this commit the legacy literals still exist in `internal/config/tools.go` and Plan 07-01's two bridging tests still reference them — that intentional residue keeps the commit compiling.

| File | Pre | Post |
| --- | --- | --- |
| `internal/build/tag.go:28` | `if config.IsDefaultTools(cfg.Tools)` | `if catalog.IsDefault(cfg.Tools)` — `:latest` short-circuit semantics unchanged (D-11) |
| `cmd/root.go:154` | `for _, k := range config.KnownTools` | `for _, k := range catalog.Keys()` — drops the now-unused `internal/config` import |
| `cmd/config_test.go:43` | `for _, k := range config.KnownTools` | `for _, k := range catalog.Keys()` — adds `internal/catalog` alongside the retained `internal/config` (used for `config.Load`) |
| `cmd/config_test.go:214` | `for _, k := range config.KnownTools` | `for _, k := range catalog.Keys()` |
| `internal/config/config.go:145` | `for _, k := range KnownTools` (bare same-package ref in `Load()`) | `for _, k := range catalog.Keys()` — adds `internal/catalog` import |

### Task 2 — bridging-test removal + import drop (commit `a1eaba8`)

Removed `TestCatalogContainsLegacyKnownTools` and `TestCatalogBuildArgMatchesLegacyMap` from `internal/catalog/catalog_test.go`. Dropped the now-unused `"github.com/filippolmt/toolbox/internal/config"` import. Test count went from 10 to 8. **This task ran BEFORE Task 3** — the structural fix for checker BLOCKER 1.

### Task 3 — legacy-literal deletion + thin-shim file (commit `4f9cd47`)

Replaced `internal/config/tools.go` (102 lines: `var KnownTools` + `var ToolBuildArg` + two helpers built on them) with a 13-line thin-shim file: `package config` + import of `internal/catalog` + `func DefaultTools()` (delegates to `catalog.Defaults()`) + `func IsDefaultTools(m)` (delegates to `catalog.IsDefault(m)`).

Pre-flight grep confirmed `config.KnownTools` / `config.ToolBuildArg` were absent everywhere in the repo before deletion. **Pre-flight had a gap** (see Deviations) — same-package bare-identifier references inside `internal/config` were caught at compile time and migrated alongside the deletion in this commit.

## Atomic-commit guarantee — verified

| Commit | `make go-lint` | `make go-test` | `internal/config/tools.go` state |
| --- | --- | --- | --- |
| `c0fe72d` (Task 1) | 0 issues | all packages green | 102 lines, KnownTools + ToolBuildArg present |
| `a1eaba8` (Task 2) | 0 issues | all packages green | 102 lines, KnownTools + ToolBuildArg present |
| `4f9cd47` (Task 3) | 0 issues | all packages green | 13 lines, thin shims only |

The pinned-digest test from Plan 07-02 (`TestComputeImageHashPinnedDigest`) stays green at the original `a94fa8dacf9e` literal — this plan does not change canonical-encoding inputs (catalog Entries, Dockerfile, CLI version). D-11 invariant (`TestResolveImageDefaultsToRegistry`) verified explicitly post-Task-3.

## Acceptance criteria (final state)

- `internal/config/tools.go` does NOT contain `var KnownTools` (`grep -c 'var KnownTools' internal/config/tools.go` == 0).
- `internal/config/tools.go` does NOT contain `var ToolBuildArg` (`grep -c 'var ToolBuildArg' internal/config/tools.go` == 0).
- `internal/config/tools.go` is 13 lines (≤ 25 line budget).
- `internal/config/tools.go` imports `internal/catalog`; declares exactly two functions.
- `grep -rE "config\\.(KnownTools|ToolBuildArg)" --include='*.go' .` returns zero matches across the repo.
- `grep -rE "^var KnownTools" --include='*.go' .` returns zero matches (no back-compat re-exports anywhere — D-05).
- `internal/catalog` does NOT import `internal/config` (clean dependency direction; no cycle).
- `internal/catalog/catalog_test.go` has exactly 8 test functions (the two bridging tests removed).
- `make go-test` exits 0; `make go-lint` exits 0.
- `TestComputeImageHashPinnedDigest` stays green at `a94fa8dacf9e` (Wave 2 pinned digest, encoding inputs unchanged).
- `TestResolveImageDefaultsToRegistry` stays green (D-11 invariant — `:latest` short-circuit semantics unchanged by `IsDefaultTools` → `IsDefault` symbol swap).

## Deviations from plan

### [Rule 3 — Blocking issue] Pre-flight grep missed same-package bare-identifier references in `internal/config`

- **Found during:** Task 3, immediately after rewriting `internal/config/tools.go`. `make go-test` failed with `undefined: KnownTools` / `undefined: ToolBuildArg` in `internal/config/config_test.go` (4 sites) and `internal/config/config_shell_test.go` (2 sites).
- **Issue:** Plan 07-03 Task 3's pre-flight grep used the `config.` prefix (`grep -rE "config\\.(KnownTools|ToolBuildArg)"`). Same-package callers inside `internal/config` reference these symbols by bare identifier (e.g. `for _, k := range KnownTools`), so the prefix-anchored grep silently missed them. Plan 07-03 Task 1 enumerated `cmd/root.go`, `cmd/config_test.go`, `internal/build/tag.go`, and a one-line check in `internal/config/config.go::Load()` — but did not enumerate `internal/config/config_test.go` or `internal/config/config_shell_test.go`, both of which are inside `package config` and reference the literals directly.
- **Fix:** Migrated the six bare-identifier sites in two test files to `catalog.Keys()` / `catalog.BuildArg()` and added the `internal/catalog` import to both test files. Folded into Task 3's commit because the literal deletion + same-package migration are naturally atomic — splitting them would have violated the atomic-commit guarantee.
- **Files modified:** `internal/config/config_test.go`, `internal/config/config_shell_test.go`.
- **Commit:** `4f9cd47` (rolled into Task 3).
- **Why this is Rule 3 (auto-fix blocking) not Rule 4 (architectural):** the migration is a localised symbol swap with no semantic change — every site iterates the same set in the same order; `catalog.Keys()` returns the same 30 keys in the same alphabetical order as `KnownTools`, and `catalog.BuildArg("go")` / `catalog.BuildArg("zsh")` return the same `INSTALL_GO` / `INSTALL_ZSH` strings as `ToolBuildArg["go"]` / `ToolBuildArg["zsh"]`. No new package, no new architectural seam.

## Threat model dispositions

- **T-07-06 (T — Tampering, missed caller):** mitigated. Compile-time enforcement caught the missed callers above; the gap was closed inside the same commit. After this plan no caller in the repo references `config.KnownTools` / `config.ToolBuildArg`.
- **T-07-07 (E — Elevation, IsDefault widening):** mitigated/accepted. `catalog.IsDefault` has the same semantics as `config.IsDefaultTools`, verified by Plan 07-01's `TestIsDefaultMatchesLegacy` test (still in the catalog test suite) and by Plan 07-02's pinned digest staying stable across the symbol swap.

## Next-wave note (Plan 07-04)

The bijection test at the catalog ↔ Dockerfile boundary lands in Plan 07-04. That test imports `internal/build` (for `Assets` / `AssetDir`) — current dependency direction is `build → catalog` (build imports catalog for `WriteCanonical`). A test in `internal/catalog` that imports `internal/build` would form a cycle if Plan 07-04 uses `package catalog`. The fix is `package catalog_test` (external test package), which Go allows freely — already noted in the plan output spec. No cycle exists today (`internal/catalog` imports only `io` / `fmt` / `strconv`).

## Self-Check: PASSED

- `internal/config/tools.go` — 13 lines, thin-shim only — VERIFIED via `wc -l` and `grep -c 'var KnownTools'` == 0.
- `internal/catalog/catalog_test.go` — 8 test functions — VERIFIED via `grep -c '^func Test'` == 8.
- `internal/catalog/catalog_test.go` — no `internal/config` import — VERIFIED via `grep -c '"github.com/filippolmt/toolbox/internal/config"'` == 0.
- `internal/catalog` does not import `internal/config` — VERIFIED via `grep -rE '"github.com/filippolmt/toolbox/internal/config"' internal/catalog/` (zero matches).
- `grep -rE "config\\.(KnownTools|ToolBuildArg)" --include='*.go' .` — VERIFIED zero matches.
- Commits exist:
  - `c0fe72d` (Task 1) — VERIFIED via `git log --oneline | grep c0fe72d`.
  - `a1eaba8` (Task 2) — VERIFIED.
  - `4f9cd47` (Task 3) — VERIFIED.
- `make go-test` and `make go-lint` both exit 0 at Task 3's commit — VERIFIED.
- Plan 07-02 pinned-digest test (`TestComputeImageHashPinnedDigest`) still green — VERIFIED via targeted `go test -run` invocation.
