---
phase: 07-tool-catalog
plan: 02
subsystem: tool-catalog
tags: [catalog, image-hash, build-args, canonical-encoding, hash-pin, ci-invariant]

requires:
  - phase: 07-tool-catalog
    plan: 01
    provides: "internal/catalog package: Entries table, Keys/BuildArg/Defaults/IsDefault accessors, WriteCanonical/WriteCanonicalEntries encoders, and the D-10 mutation test (TestCanonicalEncodingIsNeutralToOptionalFieldPopulation) that locks optional fields out of the canonical encoding"
provides:
  - "internal/build/tag.go consuming internal/catalog for both Dockerfile build-arg construction and the local image hash"
  - "BuildArgsFromTools rewritten as a single iteration over catalog.Entries (no parallel ToolBuildArg map lookup)"
  - "computeImageHashFromFS delegating its tools-section encoding to catalog.WriteCanonical"
  - "TestComputeImageHashPinnedDigest — canonical-encoding invariant lock pinned at 12-char hex digest a94fa8dacf9e (D-12)"
  - "TestComputeImageHashUsesCatalogCanonicalEncoder — wiring assertion that the build hash transitively inherits the catalog-layer D-10 guarantee"
affects: [07-03, 07-04, 07-05, 07-06]

tech-stack:
  added: []
  patterns:
    - "Two-layer invariant pattern: structural mutation test at the canonical layer (Plan 07-01) + grep-based wiring assertion at the consumer layer (this plan), avoiding duplicate / structurally-weaker mutation tests further from the source of truth"
    - "Pinned-digest invariant lock: a fixed (assets, dir, version, tools) fixture hashed against a known hex digest, with first-run procedure (placeholder → real digest after reproducibility check) baked into the test docstring"

key-files:
  created: []
  modified:
    - internal/build/tag.go
    - internal/build/tag_test.go

key-decisions:
  - "D-09 / CAT-05 enforced — image hash now flows through catalog.WriteCanonical; the inline `for _, k := range keys { fmt.Fprintf(h, \"tool:%s=%s\\n\", ...) }` block is gone"
  - "D-10 enforced transitively at the build layer via the wiring assertion (TestComputeImageHashUsesCatalogCanonicalEncoder); per checker BLOCKER 2, no parallel build-layer mutation test was shipped — the catalog-layer mutation test (Plan 07-01) is the canonical structural guarantee, and the wiring assertion ensures the build hash actually consumes that encoder"
  - "D-11 preserved verbatim — config.IsDefaultTools(cfg.Tools) at the top of ResolveImage stays untouched in this plan; Plan 07-03 owns the swap to catalog.IsDefault"
  - "D-12 enforced — TestComputeImageHashPinnedDigest pins a 12-char hex digest (a94fa8dacf9e) so any future canonical-encoding drift is observable in CI"

patterns-established:
  - "Pattern: Pin-digest test for hash-stability invariants — fixture-driven, decoupled from production catalog size, with explicit first-run procedure documented in the test"
  - "Pattern: Source-grep wiring assertion for cross-package delegation invariants — read the consumer's source, locate the function body, assert the producer's symbol appears within it"

metrics:
  duration: "~25min"
  completed: "2026-05-06"
  tasks_completed: 2
  files_created: 0
  files_modified: 2
---

# Phase 07 Plan 02: Tag Migration to Catalog Summary

Migrated `internal/build/tag.go` to consume `internal/catalog` for both Dockerfile build-arg construction and the local image hash, locking the new canonical encoding behind a pinned-digest test (D-12) and a wiring assertion (D-10 transitive guarantee) without duplicating the catalog-layer mutation test at the build layer.

## What changed

### `internal/build/tag.go`

**Imports.** Added `"github.com/filippolmt/toolbox/internal/catalog"`. Removed `"strconv"` — the catalog encoder absorbed the only `strconv.FormatBool` call site. `"sort"` stays (the asset-section iteration still uses it). `"github.com/filippolmt/toolbox/internal/config"` stays (D-11: `ResolveImage` still calls `config.IsDefaultTools(cfg.Tools)` at the top; Plan 07-03 owns the swap to `catalog.IsDefault`).

**`BuildArgsFromTools` rewrite (lines 45-60).** Replaced the two-step `for _, k := range config.KnownTools { … arg, ok := config.ToolBuildArg[k] … }` body with a single iteration over `catalog.Entries`:

```go
for _, e := range catalog.Entries {
    enabled, ok := tools[e.Key]
    if !ok { continue }
    if enabled { continue }
    v := "false"
    out[e.BuildArg] = &v
}
```

The defensive `arg, ok := config.ToolBuildArg[k]; if !ok { continue }` branch disappeared — every catalog `Entry` carries `BuildArg` by construction (Plan 07-01 Test 1 enforces `BuildArg != ""`), so the `ok` check was unreachable.

**`computeImageHashFromFS` rewrite (lines 99-107).** Replaced the inline tools-map sort+write loop:

```go
keys := make([]string, 0, len(tools))
for k := range tools { keys = append(keys, k) }
sort.Strings(keys)
for _, k := range keys {
    _, _ = fmt.Fprintf(h, "tool:%s=%s\n", k, strconv.FormatBool(tools[k]))
}
```

with a single delegated call:

```go
if err := catalog.WriteCanonical(h, tools); err != nil {
    return "", err
}
```

The asset-section iteration above this block (version line, sorted asset names, byte writes) stays verbatim — those bytes feed the same hash and any change there would shift every user's digest.

### `internal/build/tag_test.go`

**Imports.** Added `"bytes"` and `"os"` to support the wiring-assertion test.

**`TestComputeImageHashPinnedDigest` (D-12, CAT-05 invariant lock).** A fixture-driven pin: `(2 assets, dir="a", version="v1.2.3-pin", tools={azure:true, go:false, rtk:true})` → `a94fa8dacf9e`. Verified reproducible across two `make go-test` runs before committing the literal. Decoupled from `catalog.Defaults()` so the pin does not auto-shift when the catalog table grows.

**`TestComputeImageHashUsesCatalogCanonicalEncoder` (D-10 transitive guarantee, per checker BLOCKER 2).** Reads `tag.go`, locates the `computeImageHashFromFS` function body (between `func computeImageHashFromFS(` and the next top-level `\nfunc `), and asserts `catalog.WriteCanonical` appears within it. The canonical D-10 mutation test (Plan 07-01 `TestCanonicalEncodingIsNeutralToOptionalFieldPopulation`) is the structural guarantee at the catalog layer; this wiring assertion ensures the build hash actually consumes that encoder, so the guarantee transitively applies to every user's `toolbox:local-<hash>`. Per checker BLOCKER 2, no parallel build-layer mutation test was added — the duplicate would be structurally weaker (the build layer has no public function signature accepting custom `[]Entry` slices).

The five pre-existing `TestResolveImage*` / `TestBuildArgsFromTools*` tests and the two pre-existing `TestComputeImageHashStable*` / `*ChangesOnAssetEdit` tests stay unmodified and stay green.

## Verification

`make go-test` and `make go-lint` both exit 0 (run via `TOOLBOX_HOST_WORKSPACE=$(pwd) make …` to point the in-container workspace at this worktree).

Acceptance criteria checks:

| Check | Result |
| --- | --- |
| `grep -c '"github.com/filippolmt/toolbox/internal/catalog"' internal/build/tag.go` | 1 |
| `grep -c "for _, e := range catalog.Entries" internal/build/tag.go` | 1 |
| `grep -E "config\.(KnownTools\|ToolBuildArg)" internal/build/tag.go` | 0 matches |
| `grep -c "catalog.WriteCanonical(h, tools)" internal/build/tag.go` | 1 |
| `grep -E 'fmt\.Fprintf\(h, "tool:%s=%s' internal/build/tag.go` | 0 matches |
| `grep -c "config.IsDefaultTools(cfg.Tools)" internal/build/tag.go` | 1 (D-11 preserved) |
| `grep -c "func TestComputeImageHashPinnedDigest" internal/build/tag_test.go` | 1 |
| `grep -c "func TestComputeImageHashUsesCatalogCanonicalEncoder" internal/build/tag_test.go` | 1 |
| `grep -c "populatedEntries" internal/build/tag_test.go` | 0 (no parallel mutation test at build layer) |
| `grep -c "PLACEHOLDER_DIGEST_REPLACE_ON_FIRST_RUN" internal/build/tag_test.go` | 0 (real digest pinned) |
| `grep -oE 'const want = "[0-9a-f]{12}"' internal/build/tag_test.go` | `const want = "a94fa8dacf9e"` |
| `git diff --stat HEAD~2 -- internal/catalog internal/config cmd` | empty (this plan touches only internal/build/) |

## Commits

- `347ec38` — `refactor(07-02): migrate internal/build/tag.go to consume internal/catalog`
- `0f6767c` — `test(07-02): pin canonical-encoded image hash and assert catalog wiring`

## Deviations from Plan

None — plan executed exactly as written. The pinned digest captured on first run (`a94fa8dacf9e`) was reproducible on the second run, so it was committed as-is per the first-run procedure documented in the plan.

## Release notes (for Phase 07 release notes — not committed by this plan)

Users with non-default `tools:` configs will see a one-time `toolbox:local-<hash>` rebuild on next `toolbox shell` because the canonical encoding format shifted from `tool:%s=%s\n` to the catalog's `tool:%s|%s|%s\n`. Default-tools users are unaffected — the `IsDefaultTools` short-circuit at the top of `ResolveImage` (D-11) returns `:latest` from GHCR before any hash computation runs.

## D-10 enforcement model

D-10 (optional `Entry` fields hash-neutral) is enforced once, at the catalog layer, by Plan 07-01's `TestCanonicalEncodingIsNeutralToOptionalFieldPopulation` — a structural mutation test that constructs bare-vs-populated `[]catalog.Entry` slices and asserts `catalog.WriteCanonicalEntries` produces byte-identical output. This plan extends that guarantee transitively to image hashes via `TestComputeImageHashUsesCatalogCanonicalEncoder`, which asserts (by source grep) that `computeImageHashFromFS` actually delegates the tools-section encoding to `catalog.WriteCanonical`. Per checker BLOCKER 2, no parallel build-layer mutation test was added — that would be a structurally weaker form of the same invariant, because `computeImageHashFromFS` has no public function signature accepting custom `[]Entry` slices.

## Self-Check: PASSED

- `internal/build/tag.go` exists and contains `catalog.WriteCanonical(h, tools)` (verified).
- `internal/build/tag_test.go` exists and contains both new tests with the real pinned digest (verified).
- Commit `347ec38` exists in `git log --oneline --all` (verified).
- Commit `0f6767c` exists in `git log --oneline --all` (verified).
- `make go-test` exits 0 (verified).
- `make go-lint` exits 0 (verified).
