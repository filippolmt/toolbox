---
phase: 10-init-sequence
plan: 03
subsystem: init-sequence
tags: [extraction, entrypoint, cf, verbatim-move, self-gate]
requires:
  - "Plan 10-01 (catalog InitScript=20-cf.sh, embed.FS init.d subtree, Dockerfile COPY+chmod, placeholder 20-cf.sh)"
  - "Plan 10-02 (rtk extraction proved the verbatim-move + single-binary self-gate pattern)"
provides:
  - "internal/build/assets/init.d/20-cf.sh — verbatim cf SKILL.md seed (heredoc body byte-for-byte preserved), wrapped in single-binary self-gate"
  - "entrypoint.sh shorter by ~70 lines (cf block excised); cf SKILL seeding becomes inert at runtime until plan 10-07 wires the iterator"
affects:
  - "internal/build/assets/init.d/20-cf.sh"
  - "internal/build/assets/entrypoint.sh"
tech-stack:
  added: []
  patterns:
    - "Single-binary self-gate (D-04): outer triple-AND guard `cf && claude && [ -d ~/.claude ]` replaced with `command -v cf >/dev/null 2>&1 || exit 0` at script top — cf is the script's owner; the iterator (10-07) does not branch on tool presence"
    - "Inner double gate preserved (Pitfall 5): `command -v claude && [ -d \"$HOME/.claude\" ]` stays inside the body because Claude Code may be installed without the user's config dir present yet (first boot); the SKILL seed needs both checks before writing into ~/.claude/skills/cf/"
    - "No trailing `unset` (Pitfall 6): the `_cf_skill_file` local goes away with the script process when the future iterator invokes 20-cf.sh as its own subshell; lexical scope ends with the script"
key-files:
  created: []
  modified:
    - "internal/build/assets/init.d/20-cf.sh"
    - "internal/build/assets/entrypoint.sh"
decisions:
  - "Triple-AND outer guard collapsed to a single self-gate on cf only (D-04). Claude Code presence becomes an inner concern because the script's reason-for-being is the cf CLI; without claude installed the body is a no-op but cf-related diagnostics (e.g. `cf agent-context --list`) might still want to run independently of skill seeding in future iterations"
  - "Inner `command -v claude && [ -d \"$HOME/.claude\" ]` retained verbatim — Pitfall 5 explicitly forbids collapsing this into the outer gate; the bind-mount auto-creates ~/.claude even when tools.claude=false, so a dir-only check would write into a directory Claude Code never reads"
  - "SKILL.md heredoc body copied byte-for-byte (em-dashes, backticks, list markers, blank lines all preserved). The body is user-facing text that ships as-is to ~/.claude/skills/cf/SKILL.md; any reformatting would be visible to end users and is out of scope"
metrics:
  duration: ~10min
  tasks_completed: 1
  commits: 1
  files_modified: 2
  completed: 2026-05-07
---

# Phase 10 Plan 03: Extract cf SKILL seed to init.d/20-cf.sh Summary

**One-liner:** Verbatim move of the cf Cloudflare CLI SKILL.md seeding block from `entrypoint.sh` to `internal/build/assets/init.d/20-cf.sh`, with the outer triple-AND guard (cf + claude + ~/.claude dir) collapsed to a single `command -v cf` self-gate per D-04 while the inner Claude-presence double gate stays per Pitfall 5; entrypoint loses ~70 lines and cf SKILL seeding goes inert until plan 10-07 wires the iterator.

## Objective Recap

Replace the placeholder body shipped by plan 10-01 in `internal/build/assets/init.d/20-cf.sh` with the real cf SKILL seeding logic. Move is byte-for-byte verbatim with three structural changes:

1. **Header**: `#!/usr/bin/env bash` + `set -euo pipefail` (already present in placeholder, preserved — script independence for the iterator landing in plan 10-07).
2. **Outer self-gate restructured (D-04)**: `if command -v cf && command -v claude && [ -d "$HOME/.claude" ]; then …; fi` becomes `command -v cf >/dev/null 2>&1 || exit 0` at the top followed by `if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then …; fi` as the inner block. cf is the script's owner; claude presence stays an inner concern.
3. **Trailing `unset _cf_skill_file` dropped (Pitfall 6)**: the `_cf_skill_file` local lives only inside the script's process; when the iterator (plan 10-07) invokes `20-cf.sh` as its own subshell, the local dies with `exit`. No top-level leak to clean up.

Then delete the cf block from `entrypoint.sh`. Until plan 10-07 lands, `cf` SKILL seeding is intentionally **inert** in the runtime image — same time-bound regression as plan 10-02's rtk extraction, accepted because the alternative (interleaving iterator wiring with each extraction) violates the single-task-per-plan invariant.

## What Shipped

### Task 1 — commit `fb59b1c`

- **`internal/build/assets/init.d/20-cf.sh`**: replaced the 8-line placeholder with the full cf SKILL seed body, structured as:
  - `#!/usr/bin/env bash` (line 1, preserved from placeholder)
  - `set -euo pipefail` (line 2, preserved from placeholder)
  - 17-line documentation comment block describing idempotency, why the SKILL is hand-written rather than generated, and the double-CLI gating rationale (verbatim from old `entrypoint.sh` lines 141-156)
  - `command -v cf >/dev/null 2>&1 || exit 0` (line 21, the new outer self-gate)
  - Blank line
  - Inner `if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then` block, dedented one level: declares `_cf_skill_file`, mkdir's the parent, runs the heredoc-cat-only-if-absent guard
  - `cat > "$_cf_skill_file" <<'EOF' … EOF` heredoc — body byte-for-byte identical to old entrypoint.sh lines 162-206 (frontmatter `name: cf`, the "First step" section, Authentication / Output discipline / Context resolution / Schema introspection / Error handling sections, all em-dashes and backticks preserved)
  - Trailing newline; no `exec "$@"`, no `exit 0`, no `unset` (the iterator collects exit status downstream)

- **`internal/build/assets/entrypoint.sh`**: removed lines 141-209 (the cf block plus the trailing blank line). Surrounding context preserved: the rtk extraction left a blank line after `_plugins_cache` cleanup on line 139, then directly the graphify block's `# Install the graphify Claude Code skill on every shell start.` comment header. Net change in entrypoint.sh: -74 / +0 lines (file shrinks from 278 to 208 lines).

  Verified post-edit: `grep -i cf entrypoint.sh` returns 2 matches, both inside comment text in the graphify and playwright blocks describing how those blocks copy cf's gating pattern. No live cf code remains in entrypoint.sh. (These stale-reference comments are intentionally left untouched — the plan instructs "DO NOT remove anything else from entrypoint.sh"; they're factually accurate references to the now-extracted block's pattern, identical to the rtk-reference comments that survived plan 10-02.)

## Deviations from Plan

None — plan executed exactly as written.

The plan instructed "drop trailing `unset` (Pitfall 6)". The source block (entrypoint.sh line 208) had exactly one `unset _cf_skill_file` line — dropped as instructed. The variable's scope dies with the script process when the iterator invokes `20-cf.sh` as a subshell, so no leak.

## Verification

- **`make go-lint`** (default Make target, runs `golangci-lint run ./...` inside golangci-lint:v2.12.2-alpine) → **0 issues**. No Go code changed in this plan; this is a contract-mandated regression check.
- **`make go-test`** (default Make target, runs `go test ./... -count=1` inside golang:1.26) → **all 8 test-bearing packages green**. Specifically:
  - `internal/catalog` — `TestCatalogInitDBijection` stays GREEN (20-cf.sh declared in catalog ↔ 20-cf.sh shipped on disk; the test only checks the filename, not the body).
  - `internal/build` — `TestEmbedAssetsContainsInitDDir`, `TestTarEmbeddedContextShipsInitDDir`, `TestTarEmbeddedContext`, `TestComputeImageHashPinnedDigest` all green. The image hash test is computed against an `fstest.MapFS` fixture, not the real assets, so the body change in 20-cf.sh does not affect the pinned digest.
- **`bash -n`** on both modified scripts (`init.d/20-cf.sh` and `entrypoint.sh`) → 0 syntax errors. Belt-and-braces sanity check since shellcheck isn't on the host (Makefile has no `shellcheck` target; plan 10-07 lands the smoke-test gate).
- **Manual diff inspection** — confirmed:
  - The SKILL.md heredoc body is byte-for-byte identical to the old entrypoint.sh content (em-dashes, backticks, frontmatter, all section headers preserved).
  - The mkdir + heredoc-if-absent idempotency guard appears in the same order and structure as in the old entrypoint.sh (Pitfall 5: no early `exit` between sub-blocks).
  - The inner double-CLI gate `command -v claude && [ -d "$HOME/.claude" ]` is preserved verbatim (Pitfall 5).
  - All comment text preserved verbatim, including the "(matches the rtk pattern above)" cross-reference — kept as historical context, even though the rtk block is also extracted now (the comment now references plan 10-02's extraction pattern, which is still correct).

## Hash-Neutrality Note

`computeImageHashFromFS` includes the body of every file under `internal/build/assets/` in the digest, so the cf body change shifts the local image hash for every user with a non-default `tools:` config. Same documented "Adding (or removing) an entry … invalidates the local image hash" gotcha applied to a body change rather than a structural change — practical effect: next `toolbox shell` shows "Image not found locally — building toolbox:local-…" once and rebuilds. Aggregate the rebuild cost into the phase 10 release notes (plans 02-06 each shift the hash for the same reason).

The pinned digest in `TestComputeImageHashPinnedDigest` stays at `"a94fa8dacf9e"` because that test computes against an `fstest.MapFS` fixture defined inline in `tag_test.go` — not the real `assets/` tree.

## Runtime Behaviour Window

Between commit `fb59b1c` (this plan) and the as-yet-unmerged plan 10-07 commit (iterator wiring), the cf SKILL seed is **inert** in any toolbox image built from this branch. Documented in the plan; the alternative (atomic iterator + extractions) would require either a single mega-commit covering all 5 tools or a per-tool flag-flip dance, both rejected by the phase 10 plan structure.

User-visible impact during the window: a fresh toolbox container on this branch with `tools.cf=true` and Claude Code present would not auto-materialise `~/.claude/skills/cf/SKILL.md` on first shell. Workaround for anyone testing this branch: `claude` will simply not have the cf skill until plan 10-07 lands. Acceptable for the phase 10 development branch — main never sees this state because phase 10 lands as one merge with iterator + all extractions present.

## Follow-ups

- Plan 10-04: extract graphify install to `init.d/30-graphify.sh` (same verbatim-move pattern, single-binary self-gate `command -v graphify || exit 0`).
- Plan 10-05: extract playwright-cli install to `init.d/40-playwright-cli.sh`.
- Plan 10-06: extract MCP plugin builder to `init.d/50-mcp-plugins.sh`.
- Plan 10-07: wire the iterator + failure envelope in `entrypoint.sh` (D-06); brings the image-build gate (`make build` + smoke test) into scope and ends the inert window for cf, rtk, graphify, playwright-cli, and mcp-plugins.

## Self-Check: PASSED

- `internal/build/assets/init.d/20-cf.sh` — exists; line 21 contains `command -v cf >/dev/null 2>&1 || exit 0`; line 23 contains `if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then`; heredoc body contains `name: cf` and `cf agent-context <product>` markers ✓
- `internal/build/assets/entrypoint.sh` — `grep -E "^if command -v cf" entrypoint.sh` returns 0 hits (no live cf code remains) ✓
- commit `fb59b1c` (Task 1) — present in `git log` ✓
- `make go-lint` exit 0 ✓
- `make go-test` exit 0 ✓
- `bash -n init.d/20-cf.sh` exit 0; `bash -n entrypoint.sh` exit 0 ✓
