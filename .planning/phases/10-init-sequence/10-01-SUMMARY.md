---
phase: 10-init-sequence
plan: 01
subsystem: init-sequence
tags: [bootstrap, catalog, embed, dockerfile, bijection-test, wave-0]
requires:
  - "internal/catalog.Entry.InitScript field (Phase 07 D-06)"
  - "internal/build embed.FS (Phase 07)"
  - "internal/catalog/dockerfile_bijection_test.go (CAT-04 pattern)"
provides:
  - "5 populated InitScript values in internal/catalog.Entries"
  - "Wave 0 Go-side INIT-04 bijection test (TestCatalogInitDBijection)"
  - "5 placeholder init.d/<NN>-<tool>.sh scripts (real bodies extracted in Plans 02-06)"
  - "embed.FS recursion + tar mode-0755 forcing for init.d/* (research hazard #2 + #3)"
  - "Dockerfile COPY init.d/ + chmod -R 0755"
affects:
  - "internal/catalog/catalog.go"
  - "internal/catalog/catalog_test.go (TestCatalogShape relaxed — research hazard #4 / Pitfall 8)"
  - "internal/catalog/init_d_bijection_test.go (NEW)"
  - "internal/build/embed.go"
  - "internal/build/build.go (tarEmbeddedContext flat→WalkDir)"
  - "internal/build/build_test.go (existing TestTarEmbeddedContext relaxed; 2 new tests)"
  - "internal/build/tag.go (computeImageHashFromFS flat→WalkDir — Rule-1 deviation)"
  - "internal/build/assets/Dockerfile (COPY init.d/ + chmod)"
  - "internal/build/assets/init.d/10-rtk.sh (NEW)"
  - "internal/build/assets/init.d/20-cf.sh (NEW)"
  - "internal/build/assets/init.d/30-graphify.sh (NEW)"
  - "internal/build/assets/init.d/40-playwright-cli.sh (NEW)"
  - "internal/build/assets/init.d/50-mcp-plugins.sh (NEW)"
tech-stack:
  added: []
  patterns:
    - "fs.WalkDir over embed.FS for both build-context tar and image hash"
    - "in-tar mode hardcode for non-host filesystem assets (embed.FS exec-bit strip workaround)"
    - "RED-then-GREEN within one plan (bijection test + placeholders in two atomic commits)"
key-files:
  created:
    - "internal/catalog/init_d_bijection_test.go"
    - "internal/build/assets/init.d/10-rtk.sh"
    - "internal/build/assets/init.d/20-cf.sh"
    - "internal/build/assets/init.d/30-graphify.sh"
    - "internal/build/assets/init.d/40-playwright-cli.sh"
    - "internal/build/assets/init.d/50-mcp-plugins.sh"
  modified:
    - "internal/catalog/catalog.go"
    - "internal/catalog/catalog_test.go"
    - "internal/build/embed.go"
    - "internal/build/build.go"
    - "internal/build/build_test.go"
    - "internal/build/tag.go"
    - "internal/build/assets/Dockerfile"
decisions:
  - "claude Entry owns InitScript=50-mcp-plugins.sh (CONTEXT.md Claude's Discretion recommendation; honored)"
  - "Dockerfile COPY+chmod co-located with entrypoint COPY (Pitfall 7 — cache locality)"
  - "Belt-and-braces: tar mode 0755 + Dockerfile chmod -R 0755 (defense in depth on embed.FS exec-bit strip)"
  - "Rule-1 inline fix: tag.go::computeImageHashFromFS extended to fs.WalkDir alongside build.go::tarEmbeddedContext"
metrics:
  duration: ~25min
  tasks_completed: 2
  commits: 2
  files_modified: 13
  completed: 2026-05-07
---

# Phase 10 Plan 01: Init Sequence Bootstrap Summary

**One-liner:** Populated 5 catalog InitScript fields, extended embed.FS + tarEmbeddedContext + image-hash walker to ship `assets/init.d/` recursively, dropped 5 placeholder scripts, added Wave 0 bijection test, and stitched Dockerfile COPY+chmod for executability inside the built image — all atomic, lint+test green.

## Objective Recap

Bootstrap the Init Sequence infrastructure so Plans 02–06 can verbatim-extract one inline `entrypoint.sh` block per commit. Three scaffolds had to land atomically (per CONTEXT.md D-18 step 1):

1. The catalog declares the destination filenames (`Entry.InitScript`).
2. The embed plumbing ships the new `init.d/` subtree (both into the build-context tar and into the local-image hash).
3. The Wave 0 bijection test gates every future extraction (RED-then-GREEN within this plan).

## What Shipped

### Task 1 — commit `9263339`

- `internal/catalog/catalog.go::Entries`: populated `InitScript` for the 5 tools shipping init logic. Alphabetical-by-Key order preserved (no slice reorder).
  - `cf` → `20-cf.sh`
  - `claude` → `50-mcp-plugins.sh`  *(per Claude's Discretion recommendation — claude Entry owns the MCP-plugins script; the claude Entry already gates on `~/.claude/plugins/cache/`)*
  - `graphify` → `30-graphify.sh`
  - `playwright_cli` → `40-playwright-cli.sh`
  - `rtk` → `10-rtk.sh`
- `internal/catalog/catalog_test.go::TestCatalogShape`: replaced the strict zero-only assertion with a per-field check (research hazard #4 / Pitfall 8). Description and SmokeTest stay strict (Phase 10 only populates InitScript); InitScript may be empty or end in `.sh`.
- `internal/catalog/init_d_bijection_test.go` (NEW): Wave 0 INIT-04 set-equality test, CAT-04 pattern adapted from regex-on-Dockerfile to `fs.ReadDir` on `build.Assets`. Direction-A errors flag orphan catalog declarations; Direction-B errors flag unreachable scripts. Sorted output for determinism. Created RED at this commit (no init.d/ files yet) — turns GREEN in the next commit per the plan's RED-then-GREEN bootstrap clause.

D-10 hash-neutrality invariant verified: `TestCanonicalEncodingIsNeutralToOptionalFieldPopulation` still passes (catalog WriteCanonical excludes optional fields by construction).

### Task 2 — commit `07d17e9`

- `internal/build/embed.go`: extended `//go:embed` directive to include `assets/init.d` (bare-directory pattern → recursive embed of every non-hidden file under it).
- `internal/build/build.go::tarEmbeddedContext`: replaced flat `fs.ReadDir` with `fs.WalkDir` (research hazard #3). The walker now ships the whole asset tree; entries get tar-relative names (`Dockerfile` for top-level, `init.d/10-rtk.sh` for nested). Files under `init.d/` get `header.Mode = 0755` unconditionally (research hazard #2 / Pitfall 2 — `embed.FS` strips exec bits to 0444); top-level files preserve the existing `info.Mode()&0o111` branch so smoke-test.sh keeps its prior mode resolution.
- `internal/build/build_test.go`:
  - Relaxed existing `TestTarEmbeddedContext` (the previous "no nested paths" assertion blocked `init.d/*` legitimately — now permits the `init.d/` prefix and only `init.d/`).
  - Added `TestEmbedAssetsContainsInitDDir`: `//go:embed` ships ≥ 5 entries under `init.d/`.
  - Added `TestTarEmbeddedContextShipsInitDDir`: build-context tar contains all 5 `init.d/<NN>-*.sh` entries, each with `header.Mode == 0755`.
- `internal/build/assets/Dockerfile`: inserted `COPY init.d/ /usr/local/lib/toolbox/init.d/` + `RUN chmod -R 0755 /usr/local/lib/toolbox/init.d/` immediately after the existing `RUN chmod +x /usr/local/bin/entrypoint`. Co-located with the entrypoint COPY per Pitfall 7 (cache locality — edits to `init.d/` do NOT invalidate the downstream zsh / rtk / user-setup layers).
- `internal/build/assets/init.d/{10-rtk,20-cf,30-graphify,40-playwright-cli,50-mcp-plugins}.sh` (5 NEW files): each a minimal-viable placeholder — shebang + comment header + `set -euo pipefail` + outer self-gate (`command -v <tool> >/dev/null 2>&1 || exit 0`) + `exit 0`. Real bodies land verbatim from `entrypoint.sh` in Plans 02–06.

`TestCatalogInitDBijection` (added RED in Task 1) is now GREEN: the 5 placeholder filenames satisfy strict set-equality with the 5 populated `InitScript` declarations.

## Deviations from Plan

### [Rule 1 — Bug] `internal/build/tag.go::computeImageHashFromFS` flat-loop crashed on init.d/

- **Found during:** Task 2, after the embed.FS change shipped the new `init.d/` directory.
- **Issue:** `computeImageHashFromFS` used a flat `fs.ReadDir(assets, dir)` + `fs.ReadFile(dir+"/"+name)` loop. `init.d` appeared as a directory entry in the result; `fs.ReadFile` on a directory returned an error; `ResolveImage` swallowed the error and fell back to `"toolbox:local-unknown"`. Three `tag_test.go` tests failed (`TestResolveImageReturnsLocalHashForOptOut`, `TestResolveImageChangesWithVersion`, `TestResolveImageChangesWithToolsFlip`) plus the cascading `internal/sessionplan/plan_test.go::TestPlanComposesImage`.
- **Fix:** Same flat → `fs.WalkDir` conversion as `tarEmbeddedContext`. Asset records collected in a slice, sorted alphabetically by relative path, then folded into the digest in a stable order. Flat `fstest.MapFS` fixtures (the only kind used in `tag_test.go`) produce the identical record stream as the prior loop, so the pinned digest in `TestComputeImageHashPinnedDigest` stayed at `"a94fa8dacf9e"` — no spurious image-hash invalidation for users.
- **Files modified:** `internal/build/tag.go`.
- **Commit:** `07d17e9`.
- **Why this is Rule 1, not Rule 4:** No architectural change — the function's contract (deterministic 12-hex hash deterministic in CLI version + asset bytes + tools map) is preserved. The fix is the natural extension of the same WalkDir migration `tarEmbeddedContext` needed; the plan listed `tarEmbeddedContext` only because the planner didn't trace the second consumer of the same flat-loop pattern.

### [Rule 1 — Bug] Existing `TestTarEmbeddedContext` over-strict assertion

- **Found during:** Task 2, when the new `init.d/<name>.sh` entries entered the tar.
- **Issue:** The test asserted `strings.Contains(h.Name, "/") => fail` to enforce a flat tar layout. Phase 10's intentional `init.d/` nesting tripped it.
- **Fix:** Allow names with prefix `init.d/`; reject any other nesting. Updated the test docstring to record the exception.
- **Files modified:** `internal/build/build_test.go` (modification of an existing test, alongside the two new tests the plan called for).
- **Commit:** `07d17e9`.

No architectural decisions changed; CONTEXT.md decisions (D-01 through D-18) are honored as written.

## Verification

- `make go-lint` → **0 issues** (clean across all internal/* packages).
- `make go-test` → **all packages green** (`?` for the package-level `toolbox` and `internal/version`, `ok` for every test-bearing package). Specifically asserted green:
  - `TestCatalogShape` (relaxed)
  - `TestCanonicalEncodingIsNeutralToOptionalFieldPopulation` (D-10 invariant)
  - `TestCatalogAlphabeticalByKey`
  - `TestCatalogDockerfileBijection` (CAT-04, unaffected)
  - `TestCatalogInitDBijection` (NEW, GREEN after Task 2)
  - `TestEmbedAssetsContainsInitDDir` (NEW)
  - `TestTarEmbeddedContextShipsInitDDir` (NEW)
  - `TestTarEmbeddedContext` (relaxed)
  - `TestResolveImageReturnsLocalHashForOptOut` / `TestResolveImageChangesWithVersion` / `TestResolveImageChangesWithToolsFlip` (recovered after tag.go fix)
  - `TestComputeImageHashPinnedDigest` — pinned digest `"a94fa8dacf9e"` preserved (image-hash neutrality for users on the prior code path).

`make build` (Docker image build) was deliberately NOT run from this plan — the orchestrator's prompt notes that the image-rebuild gate lands in Plan 10-07's smoke-test, and the placeholders here are unreachable at runtime (the iterator wiring lands in Plan 10-07). Lint + go-test is the bar this plan sets.

## Hash-Neutrality Note

The pinned digest in `TestComputeImageHashPinnedDigest` is computed against a flat `fstest.MapFS` fixture (no subdirectories), so the WalkDir conversion in `computeImageHashFromFS` produces an identical record stream as the prior `ReadDir` loop — flat fixture, alphabetical sort by basename either way. Pinned digest stayed at `"a94fa8dacf9e"`; **no spurious local-image-hash invalidation for users** on the released CLI build the moment Plan 02 ships.

The real `internal/build/assets/` tree DOES grow new asset records (`init.d/10-rtk.sh` etc.) and so the production `toolbox:local-<hash>` will shift for every user with a non-default `tools:` config — this is the documented "Adding (or removing) an entry … invalidates the local image hash" gotcha applied to a new asset rather than a new catalog entry. Document in release notes when Phase 10 ships.

## Follow-ups

- Plans 02–06 each replace one placeholder body with the verbatim block from `entrypoint.sh` (rtk → cf → graphify → playwright-cli → mcp-plugins). Each commit removes the corresponding inline block from `entrypoint.sh` and is gated by `/verify`.
- Plan 07 lands the iterator + failure envelope in `entrypoint.sh` (D-06) and brings the image-build gate into scope.
- Plan 08 / 09 land DOCS-01 (root `CONTEXT.md` Init Sequence glossary entry) and DOCS-02 (CLAUDE.md milestone-wide pass).

## Self-Check: PASSED

- `internal/catalog/init_d_bijection_test.go` — exists ✓
- `internal/build/assets/init.d/10-rtk.sh` — exists ✓
- `internal/build/assets/init.d/20-cf.sh` — exists ✓
- `internal/build/assets/init.d/30-graphify.sh` — exists ✓
- `internal/build/assets/init.d/40-playwright-cli.sh` — exists ✓
- `internal/build/assets/init.d/50-mcp-plugins.sh` — exists ✓
- commit `9263339` (Task 1) — present in `git log` ✓
- commit `07d17e9` (Task 2) — present in `git log` ✓
- `make go-lint` exit 0 ✓
- `make go-test` exit 0 ✓
