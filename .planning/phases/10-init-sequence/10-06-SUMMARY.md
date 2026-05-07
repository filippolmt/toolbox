---
phase: 10-init-sequence
plan: 06
subsystem: init-sequence
tags: [extraction, entrypoint, mcp-plugins, verbatim-move, self-gate, npm, node, nullglob, marker-file]
requires:
  - "Plan 10-01 (catalog InitScript=50-mcp-plugins.sh, embed.FS init.d subtree, Dockerfile COPY+chmod, placeholder 50-mcp-plugins.sh)"
  - "Plan 10-02 (rtk extraction proved the verbatim-move + single-binary self-gate pattern)"
  - "Plan 10-03 (cf extraction reaffirmed the D-04 outer-gate / Pitfall-5 inner-gate split)"
  - "Plan 10-04 (graphify extraction repeated the same pattern with non-fatal echo fallback)"
  - "Plan 10-05 (playwright-cli extraction repeated the same pattern with verbatim subshell wrapper preservation)"
provides:
  - "internal/build/assets/init.d/50-mcp-plugins.sh — verbatim MCP plugin auto-build body, wrapped in single-binary `command -v npm` self-gate, with the inner `node` and plugins-cache directory checks preserved as independent gates per Pitfall 5"
  - "entrypoint.sh shorter by ~65 lines (MCP plugin auto-build block excised); MCP plugin auto-build becomes inert at runtime until plan 10-07 wires the iterator"
affects:
  - "internal/build/assets/init.d/50-mcp-plugins.sh"
  - "internal/build/assets/entrypoint.sh"
tech-stack:
  added: []
  patterns:
    - "Single-binary self-gate (D-04): outer triple-AND guard `[ -d $_plugins_cache ] && command -v npm && command -v node` replaced with `command -v npm >/dev/null 2>&1 || exit 0` at script top — npm is the script's owner; the iterator (10-07) does not branch on tool presence"
    - "Inner double gate preserved (Pitfall 5): the `[ -d \"$_plugins_cache\" ] && command -v node` check stays inside the body because (a) the cache dir is absent on first boot before Claude Code installs any plugin, (b) node may exist without npm in some image variants — these are independent failure modes that must not be collapsed into the outer self-gate"
    - "Per-plugin marker file `.toolbox-built` preserved verbatim — written next to the built `dist/` so a plugin upgrade (new versioned path) naturally invalidates it; signals 'build already attempted' to the next shell"
    - "Per-plugin error log `.toolbox-build-error.log` (D-07 forerunner) preserved verbatim — captures stderr on failure, lives next to the marker so it survives container restarts via the bind-mounted plugin dir; removed on success to keep the dir tidy"
    - "`node -e` script with `|| _has_build=\"\"` fallback (Pitfall 3 hard rule) preserved byte-for-byte — protects against missing `package.json` fields, malformed JSON, and node parse failures that would otherwise bubble through `set -e`"
    - "`shopt -s nullglob` / `shopt -u nullglob` discipline preserved — without it the `*/*/*/mcp` glob would yield the literal `*/*/*/mcp` string when no plugins exist, then the loop would attempt to `cd` into that non-existent path"
    - "Header-printed-once counter `_header_printed` preserved verbatim — the 'Building Claude Code MCP plugins:' header prints exactly once even when the loop iterates over many plugins; checked-and-set inside the loop body so plugins with no build step (skip-continue) don't trigger the header"
    - "No trailing `unset` (Pitfall 6) — the script-per-tool layout means the next iteration starts with a fresh subshell, so explicit cleanup of `_plugins_cache`, `_mcp_dirs`, `_mcp_dir`, `_header_printed`, `_has_build`, `_build_log` is dead weight"
key-files:
  created: []
  modified:
    - "internal/build/assets/init.d/50-mcp-plugins.sh"
    - "internal/build/assets/entrypoint.sh"
decisions:
  - "Outer self-gate selected as `command -v npm` rather than `command -v node` because npm is the strict superset dependency — every npm invocation runs node, but node alone (without npm) cannot run `npm install` / `npm run build`. Picking npm as the owner means the script exits early on the more restrictive condition, matching the original entrypoint behaviour where npm absence skipped the entire block."
  - "Both inner gates (`[ -d \"$_plugins_cache\" ]` and `command -v node`) kept inside the body per Pitfall 5. Collapsing them into the outer gate (e.g. `command -v npm && command -v node && [ -d $cache] || exit 0`) would lose the independence of the failure modes — the iterator (10-07) treats early exit and inner short-circuit as the same operational state, but the script's own readability benefits from leaving the directory check next to the variable that defines it."
  - "All six invariants enumerated in the plan summary (triple gate semantics, marker file, error log, `node -e` `|| _has_build=\"\"` fallback, `nullglob` discipline, header-once counter) preserved byte-for-byte with zero re-indentation or 'improvement' rewrites — this is the most complex extraction in phase 10 and the bug surface is on the loop body, not on stylistic refactors."
  - "Trailing `unset _mcp_dirs _mcp_dir _header_printed _has_build _build_log` and `unset _plugins_cache` from entrypoint.sh dropped per Pitfall 6: each init.d/*.sh script runs in its own bash invocation under the iterator (10-07), so locals never leak across scripts."
metrics:
  duration: ~12min
  tasks_completed: 1
  commits: 1
  files_modified: 2
  completed: 2026-05-07
---

# Phase 10 Plan 06: Extract MCP plugin auto-build to init.d/50-mcp-plugins.sh Summary

**One-liner:** Verbatim move of the MCP plugin auto-build block (the most complex extraction in phase 10) from `entrypoint.sh` to `internal/build/assets/init.d/50-mcp-plugins.sh`, with the outer triple-AND guard (`[ -d cache] && npm && node`) collapsed to a single `command -v npm` self-gate per D-04 while node and the plugins-cache dir remain as independent inner gates per Pitfall 5; all six byte-for-byte invariants — triple gate semantics, per-plugin `.toolbox-built` marker, per-plugin `.toolbox-build-error.log`, `node -e` `|| _has_build=""` fallback (Pitfall 3 hard rule), `shopt -s nullglob` discipline, header-printed-once counter — preserved verbatim; trailing `unset` dropped per Pitfall 6; entrypoint loses ~65 lines and MCP plugin auto-build goes inert until plan 10-07 wires the iterator.

## Objective Recap

Replace the placeholder body shipped by plan 10-01 in `internal/build/assets/init.d/50-mcp-plugins.sh` with the real MCP plugin auto-build logic. Move is byte-for-byte verbatim with the standard structural changes shared by plans 10-02..10-05:

1. **Header**: `#!/usr/bin/env bash` + `set -euo pipefail` (already present in placeholder, preserved — script independence for the iterator landing in plan 10-07).
2. **Outer self-gate restructured (D-04)**: `if [ -d "$_plugins_cache" ] && command -v npm && command -v node; then …; fi` becomes `command -v npm >/dev/null 2>&1 || exit 0` at the top followed by `if [ -d "$_plugins_cache" ] && command -v node >/dev/null 2>&1; then …; fi` as the inner block. npm is the script's owner; node + cache-dir presence stay as inner concerns per Pitfall 5.
3. **All six invariants preserved verbatim (the explicit hard requirements of this plan)**:
   - **Triple gate** (npm + node + plugins-cache dir) preserved — but split into outer-self-gate + inner-double-gate per D-04 + Pitfall 5
   - **Per-plugin marker** `.toolbox-built` written after a successful build; lives in the versioned plugin dir so an upgrade naturally invalidates it
   - **Per-plugin error log** `.toolbox-build-error.log` captures stderr on failure; survives container restarts via bind-mount; removed on success
   - **`node -e` script with `|| _has_build=""` fallback** (Pitfall 3 hard rule) — the literal `) || _has_build=""` after the command substitution is preserved byte-for-byte; protects against missing `package.json` fields and node parse failures bubbling through `set -e`
   - **`shopt -s nullglob` / `shopt -u nullglob` bracket** preserved — the `*/*/*/mcp` glob expands to nothing rather than the literal `*/*/*/mcp` when no plugins exist
   - **Header-printed-once counter** `_header_printed=0` set once before the loop, set to `1` only after the first plugin needing a build prints "Building Claude Code MCP plugins:"; the literal phrase is preserved
4. **No trailing `unset`** (Pitfall 6). The original entrypoint had two unsets covering six variables; both dropped because each init.d/*.sh runs in its own bash invocation under the iterator (10-07), so locals never leak across scripts.

Then delete the corresponding block from `entrypoint.sh`. Until plan 10-07 lands, MCP plugin auto-build is intentionally **inert** in the runtime image — same time-bound regression as plans 10-02 (rtk), 10-03 (cf), 10-04 (graphify), and 10-05 (playwright-cli), accepted because the alternative (interleaving iterator wiring with each extraction) violates the single-task-per-plan invariant.

## What Shipped

### Task 1 — commit `9901bf2`

- **`internal/build/assets/init.d/50-mcp-plugins.sh`**: replaced the 7-line placeholder with the full MCP plugin auto-build body, structured as:
  - `#!/usr/bin/env bash` (line 1)
  - `set -euo pipefail` (line 2)
  - blank line
  - 4-line documentation comment block describing the D-04 outer-gate / Pitfall-5 inner-gate split (npm is the owner; node + cache-dir are inner gates because each represents an independent failure mode)
  - `command -v npm >/dev/null 2>&1 || exit 0` (line 8, the new outer self-gate per D-04)
  - blank line
  - 15-line documentation comment block describing the always-run rationale, the marker-file semantics, the per-plugin decision tree, and the upgrade-invalidation property (verbatim from the old `entrypoint.sh` lines 75-89)
  - `_plugins_cache="$HOME/.claude/plugins/cache"` (variable assignment)
  - Inner `if [ -d "$_plugins_cache" ] && command -v node >/dev/null 2>&1; then` block, dedented one level: contains the full `shopt -s nullglob` + glob expansion + per-plugin loop + `node -e` build-step probe + `npm install` / `npm run build` invocation + marker write + error-log capture, all byte-for-byte verbatim
  - Trailing newline; no `exec "$@"`, no `exit 0`, no `unset` (Pitfall 6)

- **`internal/build/assets/entrypoint.sh`**: removed lines 75-139 (the MCP plugin auto-build block: 15-line comment + variable assignment + `if [ -d ... ] && command -v npm && command -v node; then ... fi` + trailing `unset _plugins_cache`, total 65 lines). Surrounding context preserved: the `oci` block on line 73 is followed by a blank line then directly the user-defined startup hooks comment header on the next line.

  Verified post-edit: `grep -nE 'mcp|plugin|_plugins_cache|_mcp_dirs|_has_build' internal/build/assets/entrypoint.sh` returns no matches — entrypoint.sh is now MCP-plugin-free at the code level.

## Deviations from Plan

None — plan executed exactly as written.

The six hard invariants (triple gate semantics, per-plugin marker, per-plugin error log, `node -e` `|| _has_build=""` fallback, `nullglob` bracket, header-printed-once counter) all survived intact. Verified mechanically:

- `grep -c 'shopt -s nullglob'` → 1
- `grep -c '|| _has_build=""'` → 1 (Pitfall 3 hard rule)
- `grep -c '\.toolbox-built'` → 4 (skip-check + marker write + comment references)
- `grep -c '\.toolbox-build-error\.log'` → 1
- `grep -c '_header_printed'` → 3 (init + check + set)
- Triple gate present: outer `command -v npm` (line 8), inner `[ -d "$_plugins_cache" ]` + `command -v node` (line 26)

## Verification

- **`make go-lint`** (default Make target, runs `golangci-lint run ./...` inside golangci-lint:v2.12.2-alpine) → **0 issues**. No Go code changed in this plan; this is a contract-mandated regression check.
- **`make go-test`** (default Make target, runs `go test ./... -count=1` inside golang:1.26) → **all 8 test-bearing packages green**. Specifically:
  - `internal/catalog` — `TestCatalogInitDBijection` stays GREEN (50-mcp-plugins.sh declared in catalog ↔ 50-mcp-plugins.sh shipped on disk; the test only checks the filename, not the body).
  - `internal/build` — `TestEmbedAssetsContainsInitDDir`, `TestTarEmbeddedContextShipsInitDDir`, `TestTarEmbeddedContext`, `TestComputeImageHashPinnedDigest` all green. The image hash test is computed against an `fstest.MapFS` fixture, not the real assets, so the body change in 50-mcp-plugins.sh does not affect the pinned digest.
- **`bash -n`** on both modified scripts (`init.d/50-mcp-plugins.sh` and `entrypoint.sh`) → 0 syntax errors.
- **Pitfall 3 literal-match check** — `grep -F '|| _has_build=""' init.d/50-mcp-plugins.sh` returns one match; the `node -e` script's `|| _has_build=""` fallback survives byte-for-byte.
- **Manual diff inspection** — confirmed:
  - The `node -e` heredoc-style multi-line script (`try { const p = require(...); process.stdout.write(p.scripts && p.scripts.build ? "1" : ""); } catch (e) {}`) is byte-for-byte identical to the old entrypoint.sh content (4-line indented body, single-quote bash string preserved).
  - The `shopt -s nullglob` / `shopt -u nullglob` bracket is preserved verbatim around the `_mcp_dirs=( "$_plugins_cache"/*/*/*/mcp )` array initialisation.
  - The header-printed-once counter idiom (`_header_printed=0` before loop, `if [ "$_header_printed" -eq 0 ]; then echo ...; _header_printed=1; fi` inside loop) is preserved verbatim.
  - The per-plugin marker write (`touch "$_mcp_dir/.toolbox-built"`) and error-log path (`_build_log="$_mcp_dir/.toolbox-build-error.log"`) are preserved verbatim.
  - No unintended deletions in the commit (`git diff HEAD~1 HEAD --diff-filter=D --name-only` returns empty).

## Hash-Neutrality Note

`computeImageHashFromFS` includes the body of every file under `internal/build/assets/` in the digest, so the MCP plugin body change shifts the local image hash for every user with a non-default `tools:` config. Same documented "Adding (or removing) an entry … invalidates the local image hash" gotcha applied to a body change rather than a structural change — practical effect: next `toolbox shell` shows "Image not found locally — building toolbox:local-…" once and rebuilds. Aggregate the rebuild cost into the phase 10 release notes (plans 02-06 each shift the hash for the same reason; this is the last extraction so the cumulative hash drift stops here until the iterator wiring lands in 10-07).

The pinned digest in `TestComputeImageHashPinnedDigest` stays unchanged because that test computes against an `fstest.MapFS` fixture defined inline in `tag_test.go` — not the real `assets/` tree.

## Runtime Behaviour Window

Between commit `9901bf2` (this plan) and the as-yet-unmerged plan 10-07 commit (iterator wiring), the MCP plugin auto-build is **inert** in any toolbox image built from this branch. Documented in the plan; the alternative (atomic iterator + extractions) would require either a single mega-commit covering all 5 tools or a per-tool flag-flip dance, both rejected by the phase 10 plan structure.

User-visible impact during the window: a fresh toolbox container on this branch with one or more Claude Code marketplace plugins installed under `~/.claude/plugins/cache/` would not auto-rebuild plugins missing `dist/`. Affected plugins would log `cannot find module dist/index.js` on first MCP server start. Workaround for anyone testing this branch: manually run `(cd ~/.claude/plugins/cache/<owner>/<plugin>/<version>/mcp && npm install && npm run build && touch .toolbox-built)`. Acceptable for the phase 10 development branch — main never sees this state because phase 10 lands as one merge with iterator + all extractions present.

## Follow-ups

- Plan 10-07: wire the iterator + failure envelope in `entrypoint.sh` (D-06); brings the image-build gate (`make build` + smoke test) into scope and ends the inert window for cf, rtk, graphify, playwright-cli, and mcp-plugins. With plan 10-06 landed, all five extractions are now ready for the iterator — the entrypoint.sh diff for 10-07 will be small and focused on the iterator harness alone.

## Self-Check: PASSED

- `internal/build/assets/init.d/50-mcp-plugins.sh` — exists; line 8 contains `command -v npm >/dev/null 2>&1 || exit 0` (D-04 outer self-gate); line 26 contains the inner double gate `if [ -d "$_plugins_cache" ] && command -v node >/dev/null 2>&1; then`; loop body preserves all six invariants (triple gate, marker, error-log, `node -e` `|| _has_build=""` Pitfall 3, `nullglob` bracket, header-once counter); no trailing `unset` per Pitfall 6 ✓
- `internal/build/assets/entrypoint.sh` — `grep -nE 'mcp|plugin|_plugins_cache|_mcp_dirs|_has_build' entrypoint.sh` returns no matches; entrypoint is MCP-plugin-free at the code level ✓
- commit `9901bf2` (Task 1) — present in `git log` ✓
- `make go-lint` exit 0 ✓
- `make go-test` exit 0 ✓
- `bash -n init.d/50-mcp-plugins.sh` exit 0; `bash -n entrypoint.sh` exit 0 ✓
- Mechanical invariant checks: `shopt -s nullglob` (1), `|| _has_build=""` (1, Pitfall 3), `.toolbox-built` (4), `.toolbox-build-error.log` (1), `_header_printed` (3) — all present ✓
