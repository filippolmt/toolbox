---
phase: 07-tool-catalog
plan: 06
subsystem: docs
tags:
  - documentation
  - tool-catalog
  - glossary
  - phase-exit
requires:
  - "07-01 (catalog package skeleton with public surface) — DOCS-01 entry references all six accessors"
  - "07-02 (catalog adoption in internal/build) — DOCS-01 entry names internal/build/tag.go as a consumer"
  - "07-03 (KnownTools/ToolBuildArg deletion) — Task 2 patches CLAUDE.md only because the legacy symbols are now literally gone"
  - "07-04 (CAT-04 bijection test) — confirms the Dockerfile parity invariant cited in the glossary's why-it-exists paragraph"
  - "07-05 (Dockerfile dedupe) — phase-exit verify confirms `^ARG INSTALL_RTK` count = 1 holds"
provides:
  - "Tool Catalog glossary entry in root CONTEXT.md, sibling to Mount Plan, same heading depth (`###`), same three-paragraph structure"
  - "Factually accurate CLAUDE.md: §Architecture `internal/config` line + §Non-obvious gotchas hash-invalidation entry both repointed to internal/catalog"
  - "Phase-exit gate: /verify skill runs clean (lint OK, go-test OK, smoke-test SKIPPED — no toolbox:local image present)"
affects:
  - CONTEXT.md
  - CLAUDE.md
tech-stack:
  added: []
  patterns:
    - "Documentation parity-by-template — Tool Catalog glossary entry uses Mount Plan as the literal structural template"
    - "Surgical CLAUDE.md correctness patches under D-17 — change only literally-false content; full §Architecture rewrite reserved for Phase 10 DOCS-02"
key-files:
  created: []
  modified:
    - CONTEXT.md
    - CLAUDE.md
decisions:
  - "Glossary entry placed AFTER Mount Plan (emergence order, not alphabetical) — D-16 leaves alphabetisation to planner discretion; emergence order matches how the glossary will accumulate one entry per phase (Tool Catalog → Config Plan → Session Plan → Init Sequence)"
  - "Two surgical CLAUDE.md edits, no new sub-bullets — D-17 reserves full §Architecture rewrite for DOCS-02 (Phase 10); Architecture bullet count and §Non-obvious gotchas bullet count both unchanged"
  - "CLAUDE.md gotcha entry adds explicit canonical-encoding source (`(Key, Default, BuildArg)` tuples) plus a closing clause linking to `catalog.IsDefault` as the actual short-circuit symbol — both factually-accurate enrichments aligned with D-09/D-10/D-11"
  - "Smoke-test phase correctly SKIPPED per skill rules (no `toolbox:local` image on this machine) — not a failure"
metrics:
  duration: "~3 minutes wall clock"
  completed: "2026-05-06"
  tasks: 3
  files_modified: 2
---

# Phase 07 Plan 06: Tool Catalog Glossary + CLAUDE.md Patches + Phase Exit Gate Summary

**One-liner:** DOCS-01 closes Phase 07 with a `### Tool Catalog` glossary entry in root `CONTEXT.md` (sibling to `Mount Plan`, same depth, same paragraph order) plus two surgical CLAUDE.md correctness patches that drop every stale `KnownTools` reference; `/verify` confirms phase exits green.

## Tasks Executed

| # | Name | Commit | Files |
|---|------|--------|-------|
| 1 | Add `### Tool Catalog` glossary entry to root CONTEXT.md | `7d0a4ac` | `CONTEXT.md` |
| 2 | Patch CLAUDE.md §Architecture `internal/config` line + §Non-obvious gotchas hash-invalidation entry | `968ba0e` | `CLAUDE.md` |
| 3 | Run `/verify` skill end-to-end and confirm Phase 07 ships green | (no commit — verification step) | none |

## Task 1 — `### Tool Catalog` glossary entry (DOCS-01)

Inserted after the closing line of the existing `### Mount Plan` entry. Three paragraphs in fixed order matching the Mount Plan template:

```markdown
### Tool Catalog

The canonical declaration of every bundled tool: a single typed table
whose entries describe each tool's key, default state, Dockerfile
`INSTALL_*` ARG name, and (for later phases) description, init script,
and smoke-test hook.

Concretely: `Entries → Keys / BuildArg / Defaults / IsDefault /
WriteCanonical`. Owned by `internal/catalog`. Consumers are
`internal/build/tag.go` (build args + canonical-encoded image hash via
`WriteCanonical`) and `internal/config` (thin shims over `Defaults` and
`IsDefault`); the future Phase 10 init manifest reads the optional
`Description` / `InitScript` / `SmokeTest` fields. Optional fields are
excluded from the canonical hash encoding so populating them is
hash-neutral for users.

Why the term exists: before this concept was named, three parallel
hand-maintained literals described the same 30 tools — a `KnownTools`
slice and a `ToolBuildArg` map in `internal/config/tools.go`, with the
upcoming Phase 10 init manifest poised to be the third. Adding a tool
meant editing three files plus the Dockerfile install layer; missing
one site silently broke either the build args, the image hash, or
(eventually) the boot init. The "Tool Catalog" name turns three
fan-outs into one declaration with typed accessors.
```

Verification (acceptance criteria from plan):
- `grep -c "^### Tool Catalog" CONTEXT.md` == 1
- `grep -E "^#{3} (Mount Plan|Tool Catalog)" CONTEXT.md | wc -l` == 2 (same depth)
- `grep -c "internal/catalog" CONTEXT.md` >= 1 (== 1; only the new entry mentions it)
- `grep -c "Why the term exists" CONTEXT.md` == 2 (Mount Plan + Tool Catalog)
- `grep -cE "Entries → Keys" CONTEXT.md` == 1 (Unicode arrow style matches Mount Plan)
- All six public accessors present: `Entries`, `Keys`, `BuildArg`, `Defaults`, `IsDefault`, `WriteCanonical`
- Mount Plan entry untouched (diff is pure addition after line 26)

## Task 2 — CLAUDE.md correctness patches (D-17 minimal scope)

### Patch 1 — §Architecture `internal/config` line (line 44)

**Before:**
```markdown
- `internal/config` — `.toolbox.yaml` schema (`Config`, `Mount`), `KnownTools`, `ValidateMountsRoot`, `ValidateShell`. Pure data + validation; no filesystem side-effects in `Load()`. The `tools:` map drives which Dockerfile `INSTALL_<TOOL>` ARGs flip and feeds into the local-image hash.
```

**After:**
```markdown
- `internal/config` — `.toolbox.yaml` schema (`Config`, `Mount`), `ValidateMountsRoot`, `ValidateShell`. Pure data + validation; no filesystem side-effects in `Load()`. Tool source-of-truth lives in `internal/catalog` — the `tools:` map is validated against the catalog's keys and feeds into the local-image hash via `internal/build`.
```

Changes: drop `KnownTools` from symbols list (deleted in 07-03); redirect "tool source-of-truth" sentence to point at `internal/catalog`; mention that the local image hash flows through `internal/build`. No new sub-bullet added (D-17 reserves the full §Architecture rewrite for Phase 10 DOCS-02).

### Patch 2 — §Non-obvious gotchas hash-invalidation entry (line 78, per checker WARNING 3)

**Before:**
```markdown
- **Adding (or removing) an entry in `internal/config/tools.go` `KnownTools` invalidates the local image hash for every user with a non-default `tools:` config**. The hash is computed over the sorted Tools map, so a new key shifts the digest even when the user never set it. Practical effect: the next `toolbox shell` shows "Image not found locally — building toolbox:local-…" once and rebuilds. Document this in the release notes when bumping the list. Users on the canonical defaults are unaffected (they pull `:latest` from GHCR).
```

**After:**
```markdown
- **Adding (or removing) an entry in `internal/catalog/catalog.go` `Entries` invalidates the local image hash for every user with a non-default `tools:` config** — the canonical hash encoding is computed over the catalog's `(Key, Default, BuildArg)` tuples, so a new entry shifts the digest even if the user never sets it. Practical effect: the next `toolbox shell` shows "Image not found locally — building toolbox:local-…" once and rebuilds. Document this in the release notes when bumping the list. Users on the canonical defaults are unaffected (they pull `:latest` from GHCR — `catalog.IsDefault` short-circuits before any hash compute).
```

Changes:
- Path correction: `internal/config/tools.go` `KnownTools` → `internal/catalog/catalog.go` `Entries`.
- Make canonical-encoding source explicit: `(Key, Default, BuildArg)` tuples (matches D-09/D-10 — optional Phase 10 fields are excluded so populating them is hash-neutral for users).
- Preserve verbatim: invalidation semantics, rebuild-once observation, release-notes guidance, default-tools `:latest` short-circuit.
- Add closing clause linking to `catalog.IsDefault` as the actual short-circuit symbol (D-11).

### Whole-file gate

`grep -c "KnownTools" CLAUDE.md` == **0** — both patches landed cleanly; no stale references remain anywhere in the file. Architecture bullet count = 6 (unchanged); §Non-obvious gotchas bullet count = same as pre-plan (one entry modified, none added/removed).

## Task 3 — Phase exit gate (`/verify` skill)

Ran the three checks in skill order:

```
lint:       OK
go-test:    OK
smoke-test: SKIPPED  (no toolbox:local image present — per skill rules, do not implicit-build)
```

### Phase 07 invariants — all green

| Invariant | Command | Expected | Observed |
|---|---|---|---|
| CAT-02/CAT-03: legacy symbols deleted | `grep -rE "config\.(KnownTools\|ToolBuildArg)" --include='*.go' .` | 0 | 0 |
| DOCS-01: Tool Catalog glossary entry exists | `grep -c "^### Tool Catalog" CONTEXT.md` | 1 | 1 |
| Plan 07-05 dedupe holds | `grep -c "^ARG INSTALL_RTK" internal/build/assets/Dockerfile` | 1 | 1 |
| Task 2 whole-file gate | `grep -c "KnownTools" CLAUDE.md` | 0 | 0 |
| Catalog tests | catalog_test.go + dockerfile_bijection_test.go | 9 | 9 (8 + 1) |
| Tag tests | tag_test.go | (delta from phase) | 12 |

### ROADMAP §Phase 07 Success Criteria — all five green

1. **Adding a tool requires editing only the catalog + Dockerfile install layer.** Verified by Plan 07-03 acceptance criteria (KnownTools/ToolBuildArg deleted, no third-file edits remain). Re-confirmed here via the 0-match grep.
2. **`KnownTools` / `ToolBuildArg` derived from the catalog (no hand-maintained literals).** Plan 07-03 deleted both legacy literals; `internal/config/tools.go` is now thin shims over `catalog.Defaults()` / `catalog.IsDefault()`.
3. **Bijection test passes.** Plan 07-04 added `internal/catalog/dockerfile_bijection_test.go`; re-confirmed here via `make go-test` exiting 0.
4. **Local image hash uses catalog encoding; default-tools resolves to `:latest`.** Plan 07-02 pinned digest test (`TestComputeImageHashPinnedDigest`) + `TestResolveImageDefaultsToRegistry` both green inside `make go-test`.
5. **CONTEXT.md `Tool Catalog` entry matches Mount Plan depth.** Task 1 of THIS plan; verified above.

## Auth gates

None.

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check

**Files claimed:**
- `CONTEXT.md` (modified) — FOUND, contains `### Tool Catalog` heading at line 28, 25 lines added.
- `CLAUDE.md` (modified) — FOUND, two surgical edits applied (line 44 architecture line; line 78 gotcha entry).

**Commits claimed:**
- `7d0a4ac` (Task 1) — FOUND in `git log --oneline`.
- `968ba0e` (Task 2) — FOUND in `git log --oneline`.

## Self-Check: PASSED
