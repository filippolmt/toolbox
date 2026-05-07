---
phase: 10-init-sequence
plan: 05
subsystem: init-sequence
tags: [extraction, entrypoint, playwright-cli, verbatim-move, self-gate, subshell-wrapper]
requires:
  - "Plan 10-01 (catalog InitScript=40-playwright-cli.sh, embed.FS init.d subtree, Dockerfile COPY+chmod, placeholder 40-playwright-cli.sh)"
  - "Plan 10-02 (rtk extraction proved the verbatim-move + single-binary self-gate pattern)"
  - "Plan 10-03 (cf extraction reaffirmed the D-04 outer-gate / Pitfall-5 inner-gate split)"
  - "Plan 10-04 (graphify extraction repeated the same pattern with non-fatal echo fallback)"
provides:
  - "internal/build/assets/init.d/40-playwright-cli.sh — verbatim playwright-cli install body, wrapped in single-binary self-gate, with the (cd $HOME && ...) subshell wrapper preserved byte-for-byte"
  - "entrypoint.sh shorter by 26 lines (playwright-cli block excised); playwright-cli SKILL install becomes inert at runtime until plan 10-07 wires the iterator"
affects:
  - "internal/build/assets/init.d/40-playwright-cli.sh"
  - "internal/build/assets/entrypoint.sh"
tech-stack:
  added: []
  patterns:
    - "Single-binary self-gate (D-04): outer triple-AND guard `playwright-cli && claude && [ -d ~/.claude ]` replaced with `command -v playwright-cli >/dev/null 2>&1 || exit 0` at script top — playwright-cli is the script's owner; the iterator (10-07) does not branch on tool presence"
    - "Inner double gate preserved (Pitfall 5): `command -v claude && [ -d \"$HOME/.claude\" ]` stays inside the body because the bind-mount auto-creates ~/.claude even when tools.claude=false; a dir-only check would invoke `playwright-cli install --skills claude` against a directory Claude Code never reads"
    - "Subshell wrapper preserved verbatim (Pitfall 4): `(cd \"$HOME\" && playwright-cli install --skills claude)` byte-for-byte. `playwright-cli install` writes `.claude/skills/playwright-cli/` and `.playwright/cli.config.json` into the **current working directory** with no global-target flag — running it from `/workspace` (default CWD) would pollute every repo. The cd ensures the skill lands in `~/.claude/skills/playwright-cli/` and the config in `~/.playwright/cli.config.json`."
    - "Non-fatal echo fallback preserved verbatim: `>/dev/null 2>&1 || echo \"toolbox: playwright-cli install --skills failed (non-fatal — run \\`(cd ~ && playwright-cli install --skills claude)\\`...)\"` — body errors print the canonical retry hint but never abort the boot. Iterator (plan 10-07) owns hard failure semantics."
key-files:
  created: []
  modified:
    - "internal/build/assets/init.d/40-playwright-cli.sh"
    - "internal/build/assets/entrypoint.sh"
decisions:
  - "Triple-AND outer guard collapsed to a single self-gate on playwright-cli only (D-04). Claude presence becomes an inner concern because the script's reason-for-being is the playwright-cli binary; without playwright-cli installed the body is a no-op and we exit 0 immediately."
  - "Inner `command -v claude && [ -d \"$HOME/.claude\" ]` retained verbatim — Pitfall 5 explicitly forbids collapsing this into the outer gate; the bind-mount auto-creates ~/.claude even when tools.claude=false."
  - "Subshell wrapper `(cd \"$HOME\" && playwright-cli install --skills claude)` copied byte-for-byte — Pitfall 4 hard rule. `playwright-cli install` writes to CWD with no global-target flag; missing the cd would pollute every workspace under `/workspace/.claude/skills/playwright-cli/`."
  - "Non-fatal echo fallback string preserved verbatim including the literal backtick-wrapped retry hint `(cd ~ && playwright-cli install --skills claude)` — escaping kept exactly so the user sees the same actionable message after extraction."
metrics:
  duration: ~10min
  tasks_completed: 1
  commits: 1
  files_modified: 2
  completed: 2026-05-07
---

# Phase 10 Plan 05: Extract playwright-cli install to init.d/40-playwright-cli.sh Summary

**One-liner:** Verbatim move of the playwright-cli Claude Code skills install block from `entrypoint.sh` to `internal/build/assets/init.d/40-playwright-cli.sh`, with the outer triple-AND guard (playwright-cli + claude + ~/.claude dir) collapsed to a single `command -v playwright-cli` self-gate per D-04 while the inner Claude-presence double gate stays per Pitfall 5 and the `(cd "$HOME" && playwright-cli install --skills claude)` subshell wrapper is preserved byte-for-byte per Pitfall 4; entrypoint loses 26 lines and playwright-cli SKILL install goes inert until plan 10-07 wires the iterator.

## Objective Recap

Replace the placeholder body shipped by plan 10-01 in `internal/build/assets/init.d/40-playwright-cli.sh` with the real playwright-cli install logic. Move is byte-for-byte verbatim with three structural changes:

1. **Header**: `#!/usr/bin/env bash` + `set -euo pipefail` (already present in placeholder, preserved — script independence for the iterator landing in plan 10-07).
2. **Outer self-gate restructured (D-04)**: `if command -v playwright-cli && command -v claude && [ -d "$HOME/.claude" ]; then …; fi` becomes `command -v playwright-cli >/dev/null 2>&1 || exit 0` at the top followed by `if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then …; fi` as the inner block. playwright-cli is the script's owner; claude presence stays an inner concern.
3. **Subshell wrapper preserved verbatim (Pitfall 4)**: the `(cd "$HOME" && playwright-cli install --skills claude)` invocation is copied byte-for-byte. `playwright-cli install` writes to CWD with no global flag; without the cd the skill would land under `/workspace/.claude/skills/playwright-cli/` polluting every repo. This is a hard rule.

Then delete the playwright-cli block from `entrypoint.sh`. Until plan 10-07 lands, `playwright-cli` SKILL install is intentionally **inert** in the runtime image — same time-bound regression as plan 10-02's rtk, 10-03's cf, and 10-04's graphify extractions, accepted because the alternative (interleaving iterator wiring with each extraction) violates the single-task-per-plan invariant.

## What Shipped

### Task 1 — commit `a3846f5`

- **`internal/build/assets/init.d/40-playwright-cli.sh`**: replaced the 7-line placeholder with the full playwright-cli install body, structured as:
  - `#!/usr/bin/env bash` (line 1)
  - `set -euo pipefail` (line 2)
  - blank line
  - 21-line documentation comment block describing the always-run rationale, the CWD-pollution problem, the user-edit overwrite side-effect, and the double-CLI gating rationale (verbatim from old `entrypoint.sh` lines 141-161)
  - `command -v playwright-cli >/dev/null 2>&1 || exit 0` (line 25, the new outer self-gate)
  - blank line
  - Inner `if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then` block, dedented one level: invokes `(cd "$HOME" && playwright-cli install --skills claude) >/dev/null 2>&1 || echo "…"`
  - Trailing newline; no `exec "$@"`, no `exit 0`, no `unset` (no locals declared in this body)

- **`internal/build/assets/entrypoint.sh`**: removed lines 141-165 (the playwright-cli block: 1 blank + 21-line comment + 4-line if/fi). Surrounding context preserved: the `unset _plugins_cache` on line 139 is followed by a blank line then directly the user-defined startup hooks comment header on the next line.

  Verified post-edit: `grep -cE "^if command -v playwright-cli" entrypoint.sh` returns 0 (no live playwright-cli code remains). `grep -nE 'playwright-cli install' entrypoint.sh` returns no matches — entrypoint.sh is now playwright-cli-free at the code level.

## Deviations from Plan

None — plan executed exactly as written.

The Pitfall 4 hard rule (preserve `(cd "$HOME" && playwright-cli install --skills claude)` byte-for-byte) was honoured: `grep -F '(cd "$HOME" && playwright-cli install --skills claude)' init.d/40-playwright-cli.sh` returns the literal subshell on line 28.

## Verification

- **`make go-lint`** (default Make target, runs `golangci-lint run ./...` inside golangci-lint:v2.12.2-alpine) → **0 issues**. No Go code changed in this plan; this is a contract-mandated regression check.
- **`make go-test`** (default Make target, runs `go test ./... -count=1` inside golang:1.26) → **all 8 test-bearing packages green**. Specifically:
  - `internal/catalog` — `TestCatalogInitDBijection` stays GREEN (40-playwright-cli.sh declared in catalog ↔ 40-playwright-cli.sh shipped on disk; the test only checks the filename, not the body).
  - `internal/build` — `TestEmbedAssetsContainsInitDDir`, `TestTarEmbeddedContextShipsInitDDir`, `TestTarEmbeddedContext`, `TestComputeImageHashPinnedDigest` all green. The image hash test is computed against an `fstest.MapFS` fixture, not the real assets, so the body change in 40-playwright-cli.sh does not affect the pinned digest.
- **`bash -n`** on both modified scripts (`init.d/40-playwright-cli.sh` and `entrypoint.sh`) → 0 syntax errors.
- **Pitfall 4 literal-match check** — `grep -nF '(cd "$HOME" && playwright-cli install --skills claude)' init.d/40-playwright-cli.sh` returns line 28; subshell wrapper is preserved BYTE-FOR-BYTE.
- **Manual diff inspection** — confirmed:
  - The `(cd "$HOME" && playwright-cli install --skills claude) >/dev/null 2>&1 || echo "toolbox: playwright-cli install --skills failed …"` invocation is byte-for-byte identical to the old entrypoint.sh content (escaped backticks, em-dash, parenthetical hint preserved).
  - The inner double-CLI gate `command -v claude && [ -d "$HOME/.claude" ]` is preserved verbatim (Pitfall 5).
  - The 21-line doc-comment block is verbatim except for the deduplicated outer gate (now expressed in the script's structure, not described in prose); the prose itself was kept intact because the "Gated on `command -v playwright-cli` AND …" sentence is still factually correct as an out-of-band description of the script's overall guard policy.
  - No unintended deletions in the commit (`git diff HEAD~1 HEAD --diff-filter=D --name-only` returns empty).

## Hash-Neutrality Note

`computeImageHashFromFS` includes the body of every file under `internal/build/assets/` in the digest, so the playwright-cli body change shifts the local image hash for every user with a non-default `tools:` config. Same documented "Adding (or removing) an entry … invalidates the local image hash" gotcha applied to a body change rather than a structural change — practical effect: next `toolbox shell` shows "Image not found locally — building toolbox:local-…" once and rebuilds. Aggregate the rebuild cost into the phase 10 release notes (plans 02-06 each shift the hash for the same reason).

The pinned digest in `TestComputeImageHashPinnedDigest` stays unchanged because that test computes against an `fstest.MapFS` fixture defined inline in `tag_test.go` — not the real `assets/` tree.

## Runtime Behaviour Window

Between commit `a3846f5` (this plan) and the as-yet-unmerged plan 10-07 commit (iterator wiring), the playwright-cli SKILL install is **inert** in any toolbox image built from this branch. Documented in the plan; the alternative (atomic iterator + extractions) would require either a single mega-commit covering all 5 tools or a per-tool flag-flip dance, both rejected by the phase 10 plan structure.

User-visible impact during the window: a fresh toolbox container on this branch with `tools.playwright-cli=true` and Claude Code present would not auto-materialise `~/.claude/skills/playwright-cli/SKILL.md` on first shell. Workaround for anyone testing this branch: run `(cd ~ && playwright-cli install --skills claude)` manually inside the shell. Acceptable for the phase 10 development branch — main never sees this state because phase 10 lands as one merge with iterator + all extractions present.

## Follow-ups

- Plan 10-06: extract MCP plugin builder to `init.d/50-mcp-plugins.sh`.
- Plan 10-07: wire the iterator + failure envelope in `entrypoint.sh` (D-06); brings the image-build gate (`make build` + smoke test) into scope and ends the inert window for cf, rtk, graphify, playwright-cli, and mcp-plugins.

## Self-Check: PASSED

- `internal/build/assets/init.d/40-playwright-cli.sh` — exists; line 25 contains `command -v playwright-cli >/dev/null 2>&1 || exit 0`; line 27 contains `if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then`; line 28 contains the literal `(cd "$HOME" && playwright-cli install --skills claude)` subshell (Pitfall 4) ✓
- `internal/build/assets/entrypoint.sh` — `grep -cE "^if command -v playwright-cli" entrypoint.sh` returns 0 (no live playwright-cli code remains); `grep -nE 'playwright-cli install' entrypoint.sh` returns no matches ✓
- commit `a3846f5` (Task 1) — present in `git log` ✓
- `make go-lint` exit 0 ✓
- `make go-test` exit 0 ✓
- `bash -n init.d/40-playwright-cli.sh` exit 0; `bash -n entrypoint.sh` exit 0 ✓
