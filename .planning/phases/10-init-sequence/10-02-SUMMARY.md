---
phase: 10-init-sequence
plan: 02
subsystem: init-sequence
tags: [extraction, entrypoint, rtk, verbatim-move, self-gate]
requires:
  - "Plan 10-01 (catalog InitScript=10-rtk.sh, embed.FS init.d subtree, Dockerfile COPY+chmod, placeholder 10-rtk.sh)"
provides:
  - "internal/build/assets/init.d/10-rtk.sh — verbatim rtk init block (config.toml seed + claude init -g --auto-patch + codex init -g --codex), wrapped in single-binary self-gate"
  - "entrypoint.sh shorter by 70 lines (rtk block excised); rtk init becomes inert at runtime until plan 10-07 wires the iterator"
affects:
  - "internal/build/assets/init.d/10-rtk.sh"
  - "internal/build/assets/entrypoint.sh"
tech-stack:
  added: []
  patterns:
    - "Single-binary self-gate (D-04): `command -v rtk >/dev/null 2>&1 || exit 0` at script top replaces the inline `if command -v rtk` wrapper; one less indentation level inside the body"
    - "Three independent sub-blocks preserved (Pitfall 5): config.toml seed, claude init, codex init are sequential top-level statements — no shared `if`/`elif`, so a missing `claude` binary cannot skip codex init or the telemetry seed"
    - "No trailing `unset` (Pitfall 6): the rtk block had no top-level locals to unset; the script is a subshell-equivalent (`set -euo pipefail` + standalone exec via the future iterator), so lexical scope ends with the script"
key-files:
  created: []
  modified:
    - "internal/build/assets/init.d/10-rtk.sh"
    - "internal/build/assets/entrypoint.sh"
decisions:
  - "Outer self-gate replaces the existing `if command -v rtk` wrapper rather than nesting inside it (D-04, simpler control flow, matches the placeholder shape from plan 10-01)"
  - "Inner per-tool gates (`claude`, `codex`) stay as written — Pitfall 5 explicitly forbids collapsing them into the outer gate because the rtk init runs once but seeds two CLIs, and either may be opted out"
  - "Comment block (lines 141-161 in old entrypoint.sh) moved verbatim to the top of 10-rtk.sh; provides standalone documentation for anyone reading the script in isolation"
metrics:
  duration: ~10min
  tasks_completed: 1
  commits: 1
  files_modified: 2
  completed: 2026-05-07
---

# Phase 10 Plan 02: Extract rtk init to init.d/10-rtk.sh Summary

**One-liner:** Verbatim move of the rtk init block (config.toml seed + claude `init -g --auto-patch` + codex `init -g --codex`) from `entrypoint.sh` to `internal/build/assets/init.d/10-rtk.sh`, wrapped in a single-binary self-gate at script top; entrypoint loses 70 lines and rtk init goes inert until plan 10-07 wires the iterator.

## Objective Recap

Replace the placeholder body shipped by plan 10-01 in `internal/build/assets/init.d/10-rtk.sh` with the real rtk init logic. Move is byte-for-byte verbatim with three structural changes:

1. **Header**: `#!/usr/bin/env bash` + `set -euo pipefail` (script independence — the iterator landing in plan 10-07 will invoke each `init.d/*.sh` as its own process).
2. **Self-gate**: `command -v rtk >/dev/null 2>&1 || exit 0` at the top, replacing the inline `if command -v rtk; then … fi` wrapper (D-04 — every script in `init.d/` self-gates on its primary binary; the iterator does not branch on tool presence).
3. **Trailing `unset` lines dropped (Pitfall 6)**: not actually applicable here — the rtk block had no top-level locals to unset. Noted for completeness.

Then delete the rtk block from `entrypoint.sh`. The iterator that will invoke `init.d/*.sh` lands in plan 10-07; between this commit and that one, rtk init is intentionally **inert** in the runtime image (Pitfall observed during planning: this is a deliberate, time-bound regression — the alternative would be to interleave the iterator wiring with each extraction, which violates the single-task-per-plan invariant).

## What Shipped

### Task 1 — commit `33c2219`

- **`internal/build/assets/init.d/10-rtk.sh`**: replaced the 8-line placeholder with the full rtk init body, prefaced by:
  - `#!/usr/bin/env bash` (already present in placeholder, preserved)
  - `set -euo pipefail` (already present, preserved)
  - The 21-line documentation comment block describing flags, gating rationale, and non-fatality guarantees (verbatim from old `entrypoint.sh` lines 141-161)
  - `command -v rtk >/dev/null 2>&1 || exit 0` (the new outer self-gate, line 24 of the new file)
  - The config.toml seed block (mkdir + `cat > config.toml <<'EOF' … EOF`) — verbatim, dedented one level
  - The claude init sub-block (`if command -v claude … && [ -d "$HOME/.claude" ]; then rtk init -g --auto-patch …`) — verbatim, dedented one level
  - The codex init sub-block (`if command -v codex … && [ -d "$HOME/.codex" ]; then rtk init -g --codex …`) — verbatim, dedented one level
  - Trailing newline; no `exec "$@"`, no `exit 0` (script ends; the iterator collects exit status downstream)

- **`internal/build/assets/entrypoint.sh`**: removed lines 141-211 (the rtk block plus the trailing blank line). Surrounding context preserved: line 139 `unset _plugins_cache` is followed by a single blank line, then directly by the `# Install the cf Cloudflare CLI Claude Code skill on every shell start.` comment header for the cf block. Net change: -75 / +5 line diff in entrypoint.sh (the +5 is the dedented re-flow of the surviving cf-block comment header — actually 0 line additions, just the deletion + already-existing surrounding lines staying put).

  Verified post-edit: `grep -n "rtk" internal/build/assets/entrypoint.sh` returns only two matches, both inside comment text in the cf and graphify blocks describing how those blocks copy rtk's gating pattern. No live rtk code remains in entrypoint.sh.

## Deviations from Plan

None — plan executed exactly as written.

The plan instructed "drop trailing `unset` lines (Pitfall 6)". Inspection of the source block (entrypoint.sh lines 141-210) confirmed there were no trailing `unset` lines to drop in the rtk section — the only `unset` near the rtk block belongs to the MCP plugin block above (`unset _mcp_dirs _mcp_dir _header_printed _has_build _build_log` at line 137 + `unset _plugins_cache` at line 139), both retained in entrypoint.sh because they belong to the still-inline MCP block (which extracts in plan 10-06, not 10-02). So Pitfall 6 was honored vacuously — nothing to drop, nothing dropped.

## Verification

- **`make go-lint`** (run with `TOOLBOX_HOST_WORKSPACE=` pinned to the worktree path) → **0 issues** across all internal/* packages. No Go code changed in this plan, so this is a contract-mandated regression check.
- **`make go-test`** (same pinning) → **all 8 test-bearing packages green**. Specifically asserted:
  - `internal/catalog` — `TestCatalogInitDBijection` stays GREEN (10-rtk.sh declared in catalog ↔ 10-rtk.sh shipped on disk; the test only checks the filename, not the body).
  - `internal/build` — `TestEmbedAssetsContainsInitDDir`, `TestTarEmbeddedContextShipsInitDDir`, `TestTarEmbeddedContext`, `TestComputeImageHashPinnedDigest` all green. The image hash test is computed against an `fstest.MapFS` fixture, not the real assets, so the body change in 10-rtk.sh does not affect the pinned digest.
- **`shellcheck`** — not available on host (Makefile has no `shellcheck` target). Plan acknowledged this; plan 10-07 lands a smoke-test gate that will catch any syntax error before it reaches CI.
- **Manual diff inspection** — confirmed:
  - The three sub-blocks (config.toml seed, claude init, codex init) appear in 10-rtk.sh in the same order as in the old entrypoint.sh (Pitfall 5).
  - No early `exit` between the sub-blocks (Pitfall 5 — a failed claude lookup must not skip codex or the telemetry seed).
  - All comment text preserved verbatim (em-dashes, parentheticals, EOF heredoc body).
  - The cat <<'EOF' heredoc body is byte-for-byte identical (the privacy-defense seed: `[tee] enabled = false`, `[telemetry] enabled = false`, plus the 4-line preamble).

## Hash-Neutrality Note

`computeImageHashFromFS` includes the body of every file under `internal/build/assets/` in the digest, so the rtk body change shifts the local image hash for every user with a non-default `tools:` config. This is the documented "Adding (or removing) an entry … invalidates the local image hash" gotcha applied to a body change rather than a structural change — practical effect: next `toolbox shell` shows "Image not found locally — building toolbox:local-…" once and rebuilds. Aggregate the rebuild cost into the phase 10 release notes (plans 02-06 each shift the hash for the same reason).

The pinned digest in `TestComputeImageHashPinnedDigest` stays at `"a94fa8dacf9e"` because that test computes against an `fstest.MapFS` fixture defined inline in `tag_test.go` — not the real `assets/` tree. That's the fixed contract for the digest function, not a measurement of the production tree.

## Runtime Behaviour Window

Between commit `33c2219` (this plan) and the as-yet-unmerged plan 10-07 commit (iterator wiring), `rtk init` is **inert** in any toolbox image built from this branch. Documented in the plan; the alternative (atomic iterator + extractions) would require either a single mega-commit covering all 5 tools or a per-tool flag-flip dance, both rejected by the phase 10 plan structure.

User-visible impact during the window: `~/.config/rtk/config.toml` is not pre-seeded on first launch (privacy is still enforced by the image-wide `RTK_TEE=0` and `RTK_TELEMETRY_DISABLED=1` env vars — those are load-bearing); rtk hooks are not re-registered on shell start (so a user who deletes `~/.claude/settings.json` and rebuilds an image from this branch loses the Bash-tool hook until they manually run `rtk init -g --auto-patch`). Acceptable for the phase 10 development branch — main never sees this state because phase 10 lands as one merge with iterator + all extractions present.

## Follow-ups

- Plan 10-03: extract cf skill block to `init.d/20-cf.sh` (same verbatim-move pattern, single-binary self-gate `command -v cf || exit 0`).
- Plan 10-04: extract graphify install to `init.d/30-graphify.sh`.
- Plan 10-05: extract playwright-cli install to `init.d/40-playwright-cli.sh`.
- Plan 10-06: extract MCP plugin builder to `init.d/50-mcp-plugins.sh`.
- Plan 10-07: wire the iterator + failure envelope in `entrypoint.sh` (D-06); brings the image-build gate (`make build` + smoke test) into scope and ends the inert-rtk window.

## Self-Check: PASSED

- `internal/build/assets/init.d/10-rtk.sh` — exists, contains `command -v rtk >/dev/null 2>&1 || exit 0`, contains `cat > "$HOME/.config/rtk/config.toml" <<'EOF'`, contains `rtk init -g --auto-patch`, contains `rtk init -g --codex` ✓
- `internal/build/assets/entrypoint.sh` — `grep -n "rtk init" entrypoint.sh` returns 0 hits ✓
- commit `33c2219` (Task 1) — present in `git log` ✓
- `make go-lint` exit 0 ✓
- `make go-test` exit 0 ✓
