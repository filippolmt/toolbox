---
phase: 07-tool-catalog
plan: 04
subsystem: tool-catalog
tags: [catalog, test, bijection, CAT-04]
requires:
  - 07-01  # internal/catalog package + Entries
  - 07-02  # internal/build consumes catalog (build → catalog import direction is what motivates the external test package)
  - 07-03  # legacy KnownTools / ToolBuildArg removed; catalog is the sole declaration
provides:
  - Compile-time + test-time enforcement of catalog ↔ Dockerfile ARG bijection (CAT-04)
  - Regression guard for the "add a tool" workflow — adding a catalog entry without the matching `ARG INSTALL_*` line (or vice versa) fails CI
affects:
  - internal/catalog/dockerfile_bijection_test.go
tech-stack:
  added: []
  patterns:
    - "Bijection test analog of internal/build/build_test.go::TestDockerfilePreCreatesMountParents — read embedded Dockerfile via build.Assets fs.ReadFile, iterate a typed source-of-truth, assert containment in both directions with set semantics."
    - "External-test-package form (`package catalog_test`) for tests that import a package which itself depends on the package under test — keeps the production dependency direction (build → catalog) acyclic."
key-files:
  created:
    - internal/catalog/dockerfile_bijection_test.go
  modified: []
decisions:
  - "External test package: `package catalog_test` (not `package catalog`). The bijection test imports both `internal/catalog` and `internal/build`; since `internal/build` already imports `internal/catalog` for `WriteCanonical`, a same-package test would form an import cycle. The external test package form is the only acyclic option — Plan 07-03's next-wave note flagged this and the production direction stays clean."
  - "Strict set-equality bijection (not just one-way). Two assertions: catalog ⊆ Dockerfile AND Dockerfile ⊆ catalog. Either alone is insufficient — a stale `ARG INSTALL_*` left behind after a tool removal would pass a one-way 'every catalog entry has an ARG' check while still violating the single-source-of-truth invariant CAT-02 / CAT-04."
  - "Set semantics via `map[string]struct{}` (not slice equality). The Dockerfile currently declares `ARG INSTALL_RTK=true` twice (lines 38 and 174 — the rtk-builder stage scopes it before the runtime stage re-declares). Plan 07-05 dedupes, but the bijection test stays green either way because duplicates collapse into a single set member."
  - "Regex line-scan over a full Dockerfile parser. Pattern `^ARG INSTALL_([A-Z0-9_]+)(=true|=false)?\\s*$` (multi-line mode) is anchored to the start of a line and rejects commented-out lines (`# ARG …`) and `${INSTALL_…}` references inside RUN blocks. No new dependency, no parsing fragility — the only legal way to introduce a new `ARG INSTALL_*` matches the pattern."
  - "Read Dockerfile via `build.Assets` `embed.FS` (not `os.ReadFile`). The host-less `make go-test` golang container has no idea where the source tree lives; `embed.FS` is the only supported access path, identical to what `internal/build/build_test.go::TestDockerfilePreCreatesMountParents` already does."
metrics:
  duration: "~12 minutes"
  completed: "2026-05-06"
  tasks: 1
  files_created: 1
  files_modified: 0
  commits: 1
---

# Phase 07 Plan 04: Catalog ↔ Dockerfile Bijection Test Summary

Add a compile-time + test-time guard that the set of `INSTALL_*` ARG names declared in the embedded Dockerfile equals the set of `BuildArg` strings declared in `catalog.Entries`. CAT-04 (the bijection invariant promised by Phase 07's tool-catalog refactor) is now structurally enforced by `make go-test`.

## Outcome

After this plan, three failure modes that the catalog refactor was supposed to make impossible are now caught by CI:

1. **Catalog entry without Dockerfile install layer.** Adding `{Key: "newtool", BuildArg: "INSTALL_NEWTOOL", …}` to `catalog.Entries` without a matching `ARG INSTALL_NEWTOOL` line in `internal/build/assets/Dockerfile` fails `TestCatalogDockerfileBijection` with: `catalog declares "INSTALL_NEWTOOL" but no `ARG INSTALL_NEWTOOL` line exists in the Dockerfile`.
2. **Stale Dockerfile ARG after tool removal.** Removing an entry from `catalog.Entries` while leaving its `ARG INSTALL_*` line behind fails the test from the opposite direction: `Dockerfile declares `ARG INSTALL_OLD` but no catalog Entry has BuildArg="INSTALL_OLD"`.
3. **Mistyped ARG name.** Catalog `BuildArg: "INSTALL_GOLANG"` paired with Dockerfile `ARG INSTALL_GO=true` fails BOTH directions simultaneously, surfacing the rename in a single test run.

The test does not change the canonical-encoding inputs (catalog `Entries`, Dockerfile, CLI version) — Wave 2's pinned digest test (`TestComputeImageHashPinnedDigest` at `a94fa8dacf9e`) stays green.

## Task — bijection test (commit `0996212`)

Created `internal/catalog/dockerfile_bijection_test.go` (88 lines, `package catalog_test`).

### Test structure

```
TestCatalogDockerfileBijection
├── fs.ReadFile(build.Assets, build.AssetDir+"/Dockerfile")
├── argLineRE.FindAllSubmatch → set of "INSTALL_<X>" strings
├── for e := range catalog.Entries → set of e.BuildArg strings
├── direction 1: catalog \ Dockerfile (sorted, t.Errorf per missing)
└── direction 2: Dockerfile \ catalog (sorted, t.Errorf per extra)
```

Pattern: `(?m)^ARG INSTALL_([A-Z0-9_]+)(?:=(?:true|false))?\s*$`. Anchored to start of line in multi-line mode so commented-out lines and `${INSTALL_…}` references inside RUN blocks do not match.

### Live evidence the test enforces what it claims

- 31 matching `^ARG INSTALL_*` lines in the Dockerfile (verified via `grep -cE '^ARG INSTALL_'`), 30 distinct names — the duplicate `INSTALL_RTK` (lines 38 and 174) collapses under set semantics.
- 30 entries in `catalog.Entries`, 30 distinct `BuildArg` values.
- Set equality holds: 30 == 30, both subset checks empty, test passes.

### Atomic-commit guarantee — verified

| Step | `make go-lint` | `make go-test` | New test |
| --- | --- | --- | --- |
| Pre-commit (test file staged) | 0 issues | all packages green | `TestCatalogDockerfileBijection` PASS |
| Commit `0996212` (HEAD) | 0 issues | all packages green | listed in `go test -list`, PASS |

`TOOLBOX_HOST_WORKSPACE` had to be overridden to point at this worktree (the Makefile defaults it to the main checkout) — without that, `make go-test` would have run against the unchanged main worktree and silently reported the wrong tree as green. Verified with `TOOLBOX_HOST_WORKSPACE="$PWD" make go-test` and the targeted `go test -v -run TestCatalogDockerfileBijection ./internal/catalog/` invocation.

## Acceptance criteria (final state)

- `internal/catalog/dockerfile_bijection_test.go` exists in `package catalog_test`. VERIFIED.
- The test imports both `internal/catalog` and `internal/build` (for `Assets` / `AssetDir`). VERIFIED via `grep` on the file.
- The test reads `internal/build/assets/Dockerfile` via `build.Assets` (`fs.ReadFile`), not `os.ReadFile`. VERIFIED.
- The test uses regex line-scan, no third-party Dockerfile parser. VERIFIED — only `regexp` from stdlib.
- The test asserts BOTH directions of the bijection. VERIFIED — two `for … range` blocks emitting distinct `t.Errorf` messages.
- The test uses set semantics — passes despite duplicate `ARG INSTALL_RTK=true`. VERIFIED — `map[string]struct{}` collapses duplicates; running the test against today's tree (with the dupe) returns PASS.
- `make go-lint` exits 0. VERIFIED.
- `make go-test` exits 0. VERIFIED.
- No changes to `internal/build/assets/Dockerfile`. VERIFIED — `git diff HEAD~1 HEAD --stat` shows only the new test file.
- No changes to `internal/catalog/catalog.go` or `internal/build/tag.go`. VERIFIED.
- `internal/catalog` does not import `internal/build` at the production level (cycle stays absent). VERIFIED — only the `_test.go` file imports `internal/build`.
- Wave 2 pinned digest (`a94fa8dacf9e`) stays valid — no canonical encoding inputs touched. VERIFIED by inspection (no Entries change, no Dockerfile change, no CLI version change).

## Deviations from plan

None — plan executed exactly as written.

The single nuance worth noting (and not a deviation): `make go-test` defaults to mounting `$TOOLBOX_HOST_WORKSPACE` (the main worktree) into the golang container, not `$CURDIR`. Inside parallel-executor worktrees, `TOOLBOX_HOST_WORKSPACE="$PWD"` is required to make `make go-test` actually exercise the worktree's tree. This is a pre-existing Makefile behaviour, not introduced by this plan; flagging it here so the next wave's executor (07-05) does not get caught by silently green tests against the wrong tree.

## Threat model dispositions

- **T-07-08 (T — Tampering, divergent ARG / catalog):** mitigated. The bijection test fails any commit that introduces or removes an `ARG INSTALL_*` line without the matching catalog edit (and vice versa). CAT-04 is now CI-enforced, not just a code-review convention.
- **T-07-09 (R — Repudiation, set-vs-slice ambiguity):** mitigated. Documented in the test's package comment that duplicate `ARG INSTALL_*` lines are intentionally allowed (set semantics) — Plan 07-05's dedupe is hash-neutral and the test stays green either way; future maintainers cannot accidentally introduce a duplicate-detection failure mode that would block 07-05.

## Next-wave note (Plan 07-05)

Plan 07-05 dedupes the `INSTALL_RTK=true` declaration currently at line 174 of the Dockerfile (the rtk-builder stage at line 38 keeps it; the runtime stage gets the redundant copy removed). This bijection test passes both before and after that dedupe — set semantics is the load-bearing property. The pinned digest (`a94fa8dacf9e`) is unaffected by the dedupe because the canonical encoding consumes catalog `Entries`, not Dockerfile bytes.

## Self-Check: PASSED

- `internal/catalog/dockerfile_bijection_test.go` — FOUND.
- Commit `0996212` — FOUND via `git rev-parse --short HEAD`.
- `make go-lint` — exits 0 — VERIFIED.
- `make go-test` (with `TOOLBOX_HOST_WORKSPACE` pointing at this worktree) — exits 0 — VERIFIED.
- `go test -v -run TestCatalogDockerfileBijection ./internal/catalog/` — PASS — VERIFIED.
- No deletions in commit `0996212` — VERIFIED via `git diff --diff-filter=D --name-only HEAD~1 HEAD` (empty output).
- No changes outside `internal/catalog/dockerfile_bijection_test.go` — VERIFIED via `git diff --name-only HEAD~1 HEAD`.
