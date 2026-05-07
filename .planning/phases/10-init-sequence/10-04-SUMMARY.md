---
phase: 10-init-sequence
plan: 04
subsystem: init-sequence
tags: [extraction, entrypoint, graphify, verbatim-move, self-gate]
requires:
  - "Plan 10-01 (catalog InitScript=30-graphify.sh, embed.FS init.d subtree, Dockerfile COPY+chmod, placeholder 30-graphify.sh)"
  - "Plan 10-02 (rtk extraction proved the verbatim-move + single-binary self-gate pattern)"
  - "Plan 10-03 (cf extraction reaffirmed the D-04 outer-gate / Pitfall-5 inner-gate split)"
provides:
  - "internal/build/assets/init.d/30-graphify.sh — verbatim graphify install body, wrapped in single-binary self-gate"
  - "entrypoint.sh shorter by 22 lines (graphify block excised); graphify SKILL install becomes inert at runtime until plan 10-07 wires the iterator"
affects:
  - "internal/build/assets/init.d/30-graphify.sh"
  - "internal/build/assets/entrypoint.sh"
tech-stack:
  added: []
  patterns:
    - "Single-binary self-gate (D-04): outer triple-AND guard `graphify && claude && [ -d ~/.claude ]` replaced with `command -v graphify >/dev/null 2>&1 || exit 0` at script top — graphify is the script's owner; the iterator (10-07) does not branch on tool presence"
    - "Inner double gate preserved (Pitfall 5): `command -v claude && [ -d \"$HOME/.claude\" ]` stays inside the body because the bind-mount auto-creates ~/.claude even when tools.claude=false; a dir-only check would invoke `graphify install` against a directory Claude Code never reads"
    - "Non-fatal echo fallback preserved verbatim: `graphify install >/dev/null 2>&1 || echo \"toolbox: graphify install failed (non-fatal …)\"` — body errors print but never abort the boot. Iterator (plan 10-07) owns hard failure semantics; per-script body is soft-fail."
key-files:
  created: []
  modified:
    - "internal/build/assets/init.d/30-graphify.sh"
    - "internal/build/assets/entrypoint.sh"
decisions:
  - "Triple-AND outer guard collapsed to a single self-gate on graphify only (D-04). Claude presence becomes an inner concern because the script's reason-for-being is the graphify CLI; without graphify installed the body is a no-op and we exit 0 immediately."
  - "Inner `command -v claude && [ -d \"$HOME/.claude\" ]` retained verbatim — Pitfall 5 explicitly forbids collapsing this into the outer gate; the bind-mount auto-creates ~/.claude even when tools.claude=false."
  - "`graphify install` invocation copied byte-for-byte from entrypoint.sh including the `>/dev/null 2>&1 || echo …` non-fatal fallback. The error string contains a literal backtick-wrapped `\\\`graphify install\\\`` shell hint — escaping preserved exactly so the user sees the same actionable message after extraction."
metrics:
  duration: ~10min
  tasks_completed: 1
  commits: 1
  files_modified: 2
  completed: 2026-05-07
---

# Phase 10 Plan 04: Extract graphify install to init.d/30-graphify.sh Summary

**One-liner:** Verbatim move of the graphify Claude Code skill install block from `entrypoint.sh` to `internal/build/assets/init.d/30-graphify.sh`, with the outer triple-AND guard (graphify + claude + ~/.claude dir) collapsed to a single `command -v graphify` self-gate per D-04 while the inner Claude-presence double gate stays per Pitfall 5; entrypoint loses 22 lines and graphify SKILL install goes inert until plan 10-07 wires the iterator.

## Objective Recap

Replace the placeholder body shipped by plan 10-01 in `internal/build/assets/init.d/30-graphify.sh` with the real graphify install logic. Move is byte-for-byte verbatim with three structural changes:

1. **Header**: `#!/usr/bin/env bash` + `set -euo pipefail` (already present in placeholder, preserved — script independence for the iterator landing in plan 10-07).
2. **Outer self-gate restructured (D-04)**: `if command -v graphify && command -v claude && [ -d "$HOME/.claude" ]; then …; fi` becomes `command -v graphify >/dev/null 2>&1 || exit 0` at the top followed by `if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then …; fi` as the inner block. graphify is the script's owner; claude presence stays an inner concern.
3. **Non-fatal fallback preserved verbatim**: `graphify install >/dev/null 2>&1 || echo …` keeps the soft-fail behaviour. Iterator (10-07) owns hard failure semantics; per-script body is soft-fail.

Then delete the graphify block from `entrypoint.sh`. Until plan 10-07 lands, `graphify` SKILL install is intentionally **inert** in the runtime image — same time-bound regression as plan 10-02's rtk and 10-03's cf extractions, accepted because the alternative (interleaving iterator wiring with each extraction) violates the single-task-per-plan invariant.

## What Shipped

### Task 1 — commit `d8f8344`

- **`internal/build/assets/init.d/30-graphify.sh`**: replaced the 8-line placeholder with the full graphify install body, structured as:
  - `#!/usr/bin/env bash` (line 1)
  - `set -euo pipefail` (line 2)
  - blank line
  - 18-line documentation comment block describing the always-run rationale, the user-edit overwrite side-effect, and the double-CLI gating rationale (verbatim from old `entrypoint.sh` lines 141-158)
  - `command -v graphify >/dev/null 2>&1 || exit 0` (line 22, the new outer self-gate)
  - blank line
  - Inner `if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then` block, dedented one level: invokes `graphify install >/dev/null 2>&1 || echo "…"`
  - Trailing newline; no `exec "$@"`, no `exit 0`, no `unset` (no locals declared in this body)

- **`internal/build/assets/entrypoint.sh`**: removed lines 141-162 (the graphify block plus the trailing blank line). The cf-block extraction in 10-03 had already shifted line numbers; with graphify removed, `entrypoint.sh` shrinks from 209 to 185 lines (Δ −22 net after counting the now-absent blank line). Surrounding context preserved: the `_plugins_cache` cleanup on line 139 is followed by a blank line then directly the playwright-cli block's `# Install the playwright-cli Claude Code skills on every shell start.` comment header.

  Verified post-edit: `grep -n graphify entrypoint.sh` returns 2 hits (lines 156, 160), both inside comment text in the playwright-cli block describing how that block copies graphify's install-overwrite trade-off and the cf/graphify gating pattern. No live graphify code remains in entrypoint.sh. Same policy as 10-03's cf extraction: the plan instructs "DO NOT remove anything else from entrypoint.sh"; these factually-accurate cross-references survive untouched.

## Deviations from Plan

None — plan executed exactly as written.

The plan instruction "if graphify is installed via npx-on-demand the self-gate may need to test for `command -v claude` instead" was investigated by reading the original entrypoint block: graphify is a real binary on `PATH` (installed via the Dockerfile's `graphifyy` npm package, `tools.graphify=true` in the catalog) — not an npx-on-demand wrapper. The owner-binary self-gate is therefore `command -v graphify`, exactly as the plan's primary guidance recommended.

## Verification

- **`make go-lint`** (default Make target, runs `golangci-lint run ./...` inside golangci-lint:v2.12.2-alpine) → **0 issues**. No Go code changed in this plan; this is a contract-mandated regression check.
- **`make go-test`** (default Make target, runs `go test ./... -count=1` inside golang:1.26) → **all 8 test-bearing packages green**. Specifically:
  - `internal/catalog` — `TestCatalogInitDBijection` stays GREEN (30-graphify.sh declared in catalog ↔ 30-graphify.sh shipped on disk; the test only checks the filename, not the body).
  - `internal/build` — `TestEmbedAssetsContainsInitDDir`, `TestTarEmbeddedContextShipsInitDDir`, `TestTarEmbeddedContext`, `TestComputeImageHashPinnedDigest` all green. The image hash test is computed against an `fstest.MapFS` fixture, not the real assets, so the body change in 30-graphify.sh does not affect the pinned digest.
- **`bash -n`** on both modified scripts (`init.d/30-graphify.sh` and `entrypoint.sh`) → 0 syntax errors. Belt-and-braces sanity check since shellcheck isn't on the host (Makefile has no `shellcheck` target; plan 10-07 lands the smoke-test gate).
- **Manual diff inspection** — confirmed:
  - The `graphify install >/dev/null 2>&1 || echo "toolbox: graphify install failed …"` invocation is byte-for-byte identical to the old entrypoint.sh content (escaped backticks, em-dash, parenthetical hint preserved).
  - The inner double-CLI gate `command -v claude && [ -d "$HOME/.claude" ]` is preserved verbatim (Pitfall 5).
  - The 18-line doc-comment block is verbatim except for the deduplicated outer gate (now expressed in the script's structure, not described in prose); the prose itself was kept intact because the "Gated on `command -v graphify` AND …" sentence is still factually correct as an out-of-band description of the script's overall guard policy.
  - No unintended deletions in the commit (`git diff HEAD~1 HEAD --diff-filter=D --name-only` returns empty).

## Hash-Neutrality Note

`computeImageHashFromFS` includes the body of every file under `internal/build/assets/` in the digest, so the graphify body change shifts the local image hash for every user with a non-default `tools:` config. Same documented "Adding (or removing) an entry … invalidates the local image hash" gotcha applied to a body change rather than a structural change — practical effect: next `toolbox shell` shows "Image not found locally — building toolbox:local-…" once and rebuilds. Aggregate the rebuild cost into the phase 10 release notes (plans 02-06 each shift the hash for the same reason).

The pinned digest in `TestComputeImageHashPinnedDigest` stays at `"a94fa8dacf9e"` because that test computes against an `fstest.MapFS` fixture defined inline in `tag_test.go` — not the real `assets/` tree.

## Runtime Behaviour Window

Between commit `d8f8344` (this plan) and the as-yet-unmerged plan 10-07 commit (iterator wiring), the graphify SKILL install is **inert** in any toolbox image built from this branch. Documented in the plan; the alternative (atomic iterator + extractions) would require either a single mega-commit covering all 5 tools or a per-tool flag-flip dance, both rejected by the phase 10 plan structure.

User-visible impact during the window: a fresh toolbox container on this branch with `tools.graphify=true` and Claude Code present would not auto-materialise `~/.claude/skills/graphify/SKILL.md` on first shell. Workaround for anyone testing this branch: run `graphify install` manually inside the shell. Acceptable for the phase 10 development branch — main never sees this state because phase 10 lands as one merge with iterator + all extractions present.

## Follow-ups

- Plan 10-05: extract playwright-cli install to `init.d/40-playwright-cli.sh` (same verbatim-move pattern, single-binary self-gate `command -v playwright-cli || exit 0`).
- Plan 10-06: extract MCP plugin builder to `init.d/50-mcp-plugins.sh`.
- Plan 10-07: wire the iterator + failure envelope in `entrypoint.sh` (D-06); brings the image-build gate (`make build` + smoke test) into scope and ends the inert window for cf, rtk, graphify, playwright-cli, and mcp-plugins.

## Self-Check: PASSED

- `internal/build/assets/init.d/30-graphify.sh` — exists; line 22 contains `command -v graphify >/dev/null 2>&1 || exit 0`; line 24 contains `if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then`; body contains `graphify install >/dev/null 2>&1 || \\` and the literal `toolbox: graphify install failed` error string ✓
- `internal/build/assets/entrypoint.sh` — `grep -E "^if command -v graphify" entrypoint.sh` returns 0 hits (no live graphify code remains); only 2 stale-reference comment matches in the playwright block ✓
- commit `d8f8344` (Task 1) — present in `git log` ✓
- `make go-lint` exit 0 ✓
- `make go-test` exit 0 ✓
- `bash -n init.d/30-graphify.sh` exit 0; `bash -n entrypoint.sh` exit 0 ✓
