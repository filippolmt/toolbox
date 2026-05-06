---
phase: 07-tool-catalog
plan: 05
subsystem: build
tags:
  - cleanup
  - dockerfile
  - tool-catalog
  - dedupe
requires:
  - "07-04 (CAT-04 bijection test, set semantics) — must stay green after dedupe"
provides:
  - "Deduplicated `ARG INSTALL_RTK=true` in internal/build/assets/Dockerfile"
  - "Test-doc note clarifying TestComputeImageHashPinnedDigest fixture is decoupled from real Dockerfile bytes"
affects:
  - internal/build/assets/Dockerfile
  - internal/build/tag_test.go
tech-stack:
  added: []
  patterns:
    - "Single-purpose atomic Dockerfile dedupe — review-readable diff"
key-files:
  created: []
  modified:
    - internal/build/assets/Dockerfile
    - internal/build/tag_test.go
decisions:
  - "Drop only line 174 (opt-out flags block); keep line 38 (rtk-builder stage) — line 38 is required for the multi-stage Rust build and gates the cargo install at line 45"
  - "No re-pin of TestComputeImageHashPinnedDigest needed — fixture is a synthetic fstest.MapFS, decoupled from the real embedded Dockerfile (deviation Rule 1: plan misread the architecture)"
  - "Add a doc-comment note on the pinned-digest test explaining the decoupling so future Dockerfile cleanups don't expect a re-pin step"
metrics:
  duration: "~77s wall clock"
  completed: "2026-05-06"
  tasks: 2
  files_modified: 2
---

# Phase 07 Plan 05: Dockerfile Dedupe — `ARG INSTALL_RTK` Cleanup Summary

**One-liner:** Atomic single-line Dockerfile dedupe (drop redundant `ARG INSTALL_RTK=true` from opt-out flags block; keep load-bearing rtk-builder-stage declaration); pinned digest unchanged because the fixture is decoupled.

## Objective

Execute D-14 from the phase context: drop the duplicate `ARG INSTALL_RTK=true` at `internal/build/assets/Dockerfile:174` (opt-out flags block) so the diff against `internal/catalog/catalog.go` stays review-readable in CI logs going forward. Confirm the CAT-04 bijection test (set semantics, Plan 07-04) stays green and that the pinned-digest invariant from Plan 07-02 is unaffected.

## What Shipped

### Task 1 — Dockerfile dedupe (commit `febf0c3`)

Dropped exactly one line: the `ARG INSTALL_RTK=true` declaration at line 174, which sat in the late opt-out flags block alongside the other `INSTALL_*` tool ARGs. Kept the line-38 declaration (inside the `FROM rust:1-slim-bookworm AS rtk-builder` stage), which is the load-bearing one — the rtk-builder stage's gate logic at line 45 (`if [ "${INSTALL_RTK}" != "true" ]`) reads it, and the main image's gate at line 1079 inherits the build-arg value via the standard Docker build-arg plumbing.

Verification snapshot:

| Check | Before | After |
|-------|--------|-------|
| `grep -c "^ARG INSTALL_RTK" internal/build/assets/Dockerfile` | 2 | 1 |
| Line of remaining declaration | 38 + 174 | 38 (rtk-builder stage) |
| `TestCatalogDockerfileBijection` | PASS | PASS |
| `TestComputeImageHashPinnedDigest` | PASS | PASS (unchanged) |
| `make go-lint` | 0 issues | 0 issues |

### Task 2 — Doc-comment note on the pinned-digest test (commit `e428820`)

Added four lines of doc comment to `TestComputeImageHashPinnedDigest` in `internal/build/tag_test.go` explaining that the fixture is a synthetic `fstest.MapFS` (lines 186-189) and therefore decoupled from the real embedded `internal/build/assets/Dockerfile`. This documents what Plan 07-05 discovered (and what Plan 07-02 already implicitly relied on): single-line Dockerfile cleanups don't churn this pin. The test still tests what it should — any change to `internal/catalog.WriteCanonical`'s byte format or to the asset-section hash logic in `computeImageHashFromFS` will still trip it (Plan 07-02's `TestComputeImageHashChangesOnAssetEdit` and `TestComputeImageHashChangesOnAssetAdd` cover real-asset churn).

## Pinned Digest

| Metric | Value |
|--------|-------|
| Old pinned digest (Plan 07-02) | `a94fa8dacf9e` |
| New pinned digest (post-dedupe, Plan 07-05) | `a94fa8dacf9e` (UNCHANGED) |
| Reason unchanged | Fixture is `fstest.MapFS` synthetic asset tree, not the real `Assets` `embed.FS`. The dedupe modifies real-asset bytes, but the pin's input bytes never include those. |
| Reproducibility | Confirmed: `go test -v -run TestComputeImageHashPinnedDigest ./internal/build/... -count=2` returned identical PASS twice. |

## Bijection Test (CAT-04, Plan 07-04)

`TestCatalogDockerfileBijection` uses set semantics (`map[string]struct{}` over the matched ARG names) — any positive count of `ARG INSTALL_RTK=true` satisfies "Dockerfile mentions every catalog entry". The dedupe (count: 2 → 1) was specifically designed for by Plan 07-04. Re-confirmed PASS on the post-dedupe Dockerfile.

## Deviations from Plan

### Auto-corrected planning misread

**1. [Rule 1 — Bug] Plan 07-05 anticipated a pinned-digest shift; the digest does not actually shift**

- **Found during:** Task 1 verification step (`make go-test` after dedupe).
- **Issue:** The plan's narrative (objective, must_haves invariant 4, threat T-07-11, Task 2 RED expectation) all assumed `TestComputeImageHashPinnedDigest` would fail because "the Dockerfile bytes change → the canonical hash WILL shift". This is incorrect — the test fixture is a synthetic `fstest.MapFS` (lines 186-189 of `internal/build/tag_test.go`) seeded with two inline string literals (`"FROM scratch\n"`, `"#!/bin/sh\nexec \"$@\"\n"`), explicitly chosen by Plan 07-02 to decouple the pin from the catalog table size and from real-asset churn. The real embedded Dockerfile bytes never enter this hash computation.
- **Fix:** Skipped the (unnecessary) re-pin step in Task 2. Replaced it with a 4-line doc-comment update on the test that explains the decoupling for future maintainers, so the next Dockerfile cleanup doesn't again expect a re-pin step. No `const want` literal change.
- **Files modified:** `internal/build/tag_test.go` (doc comment only).
- **Commit:** `e428820`.
- **Verification:** Two consecutive `make go-test` runs (`-count=2`) returned identical PASS for `TestComputeImageHashPinnedDigest`; pin literal `a94fa8dacf9e` unchanged.

No other deviations. CAT-04 bijection test, `TestResolveImageDefaultsToRegistry` (D-11 invariant), and all other tests in the suite remain green.

## Acceptance Criteria

| Criterion | Status |
|-----------|--------|
| All tasks in 07-05-PLAN.md executed | Done (2/2) |
| Each task committed individually | Done (`febf0c3`, `e428820`) |
| `grep -c "^ARG INSTALL_RTK" internal/build/assets/Dockerfile` == 1 | PASS |
| Remaining `ARG INSTALL_RTK` is at rtk-builder stage (line 38) | PASS |
| `internal/build/tag_test.go::TestComputeImageHashPinnedDigest` updated with new digest | N/A — digest unchanged; doc comment updated to explain why |
| `grep -c "PLACEHOLDER" internal/build/tag_test.go` == 0 | PASS (0 matches) |
| Reproducibility across two `make go-test` runs | PASS (identical output) |
| `internal/catalog/dockerfile_bijection_test.go` UNCHANGED, still passes | PASS |
| `internal/catalog/catalog.go`, `internal/build/tag.go`, `internal/config/tools.go` UNCHANGED | PASS (only `internal/build/assets/Dockerfile` and `internal/build/tag_test.go` modified) |
| `make go-lint` exits 0 | PASS (0 issues) |
| `make go-test` exits 0 | PASS (all packages ok) |

## Files Touched

| File | Change |
|------|--------|
| `internal/build/assets/Dockerfile` | -1 line (dropped duplicate `ARG INSTALL_RTK=true` at the opt-out flags block; kept the rtk-builder-stage declaration at line 38) |
| `internal/build/tag_test.go` | +5 lines (doc-comment note explaining the pinned-digest fixture is decoupled from real embedded Dockerfile bytes) |

## Threat Flags

None — the dedupe is a single-line, no-semantic-change cleanup. Both pre- and post-dedupe states resolve `INSTALL_RTK` to `true` by default in the main image stage; opt-out via `--build-arg INSTALL_RTK=false` works identically. Default-tools `:latest` invariant (D-11) unaffected — `TestResolveImageDefaultsToRegistry` still PASS.

## Self-Check: PASSED

- File `internal/build/assets/Dockerfile` modified — single line removed (verified by `grep -c "^ARG INSTALL_RTK" == 1`).
- File `internal/build/tag_test.go` modified — doc comment added; pinned digest literal unchanged at `a94fa8dacf9e`.
- Commit `febf0c3` exists in `git log`.
- Commit `e428820` exists in `git log`.
- `make go-test` exits 0 on the worktree HEAD.
- `make go-lint` exits 0 on the worktree HEAD.
