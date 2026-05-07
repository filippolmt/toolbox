---
phase: 10-init-sequence
plan: 09
type: summary
wave: 9
status: complete
phase_status: complete
---

# Plan 10-09 SUMMARY — CLAUDE.md milestone-wide final pass (DOCS-02)

## Objective

Walk every `internal/*` package one-liner in `CLAUDE.md` §Architecture and update against post-Phases-06-10 reality (D-15), then sweep §Non-obvious gotchas / §Runtime container for entries invalidated by Phases 06-10 (D-16). LAST commit of Phase 10 and milestone v1.3.

## What was built

Two atomic edits on `CLAUDE.md`:

### Task 1 — §Architecture additions

Two new package one-liners inserted between `internal/build` and `internal/ui`:

- `internal/catalog` — Tool Catalog owner. Calls out the typed `Entries` table (Key/Default/BuildArg + optional Description/InitScript/SmokeTest), the consumers (`internal/build/tag.go` via `WriteCanonical`, `internal/config` shims, `internal/build/assets/init.d/`), and the hash-neutrality of optional fields.
- `internal/build/assets/init.d/` — Init Sequence owner. Per-tool boot scripts (`<NN>-<tool>.sh`, currently 5: rtk, cf, graphify, playwright-cli, mcp-plugins) iterated by `entrypoint.sh`. Bijection enforcement called out (Go-side `TestCatalogInitDBijection` + shell-side smoke-test bijection block; mode 0755 verified inside built image since `embed.FS` strips exec bits). Marker-log path `~/.toolbox-state/init/<name>.log`.

Existing one-liners verified accurate (per D-15 — planner verifies, doesn't blindly trust): `internal/config`, `internal/mountplan`, `internal/sessionplan`, `internal/shellcmd`, `internal/container`, `internal/build`, `internal/ui`, `internal/version` all kept as-is. `internal/container` correctly NOT claiming port-parsing/naming/env-synthesis (those moved to `internal/sessionplan` in Phase 09).

### Task 2 — §Runtime container gotcha rewrites

Three location pointers updated from `entrypoint.sh` to the corresponding `init.d/<NN>-<tool>.sh`:

- **MCP plugin auto-build on shell start**: `internal/build/assets/entrypoint.sh` → `internal/build/assets/init.d/50-mcp-plugins.sh`. Behaviour description (npm install + dist/ check + `.toolbox-built` marker + `.toolbox-build-error.log` capture + tail-5 inline + non-fatal) preserved verbatim.
- **rtk hook auto-wiring on shell start**: `entrypoint.sh` runs `rtk init -g` → `internal/build/assets/init.d/10-rtk.sh` runs `rtk init -g`. Privacy paragraph (RTK_TELEMETRY_DISABLED + RTK_TEE env-layer defenses, the load-bearing pre-seed of `~/.config/rtk/config.toml`) preserved verbatim — env-layer defenses live in Dockerfile, NOT in init scripts (D-13 unchanged).
- **`cf` Cloudflare CLI skill auto-install**: `entrypoint.sh` writes → `internal/build/assets/init.d/20-cf.sh` writes. Idempotency guarantee preserved.

No new gotcha introduced for "Init Sequence" — the `internal/build/assets/init.d/` Architecture one-liner + the CONTEXT.md glossary entry (Plan 08) cover it without duplication.

## Files modified

- `CLAUDE.md` (+5 lines added, 3 lines rewritten in place)
- `AGENTS.md` — symlink to CLAUDE.md, propagates automatically (no edit needed)

## Acceptance criteria

All grep checks pass:

- `grep -c '^- `internal/catalog`' CLAUDE.md` → 1 ✓
- `grep -c '^- `internal/build/assets/init.d/`' CLAUDE.md` → 1 ✓
- `grep -c '^- `internal/build`' CLAUDE.md` → 1 (existing bullet, NOT duplicated) ✓
- `grep -c 'TestCatalogInitDBijection' CLAUDE.md` → 1 ✓
- `grep -c 'WriteCanonical' CLAUDE.md` → 1 ✓
- Order: `internal/catalog` appears AFTER `internal/build` ✓
- `grep -c 'init.d/50-mcp-plugins.sh' CLAUDE.md` → 1 ✓
- `grep -c 'init.d/10-rtk.sh' CLAUDE.md` → 1 ✓
- `grep -c 'init.d/20-cf.sh' CLAUDE.md` → 1 ✓
- `grep -c 'RTK_TELEMETRY_DISABLED=1' CLAUDE.md` → 1 (privacy paragraph preserved) ✓
- `grep -c 'RTK_TEE=0' CLAUDE.md` → 1 (privacy paragraph preserved) ✓
- `AGENTS.md` still a symlink to CLAUDE.md ✓

## Deviations

None. Inline orchestrator edit (no subagent) — pure docs, no Go/build/runtime side-effects. Same approach as Plan 10-08.

## Phase 10 status

**COMPLETE.** All 9 plans landed:
- 10-01 catalog InitScript + embed + Dockerfile + 5 placeholders + bijection test
- 10-02..10-06 verbatim extractions of rtk / cf / graphify / playwright-cli / mcp-plugins from `entrypoint.sh` into `init.d/<NN>-<tool>.sh`
- 10-07 iterator wiring + smoke-test bijection block (KEYSTONE — first time runtime image actually invokes the 5 scripts at boot)
- 10-08 CONTEXT.md `### Init Sequence` glossary entry
- 10-09 CLAUDE.md milestone-wide final pass (DOCS-02)

Ready for `/gsd-verify-work` (Wave 10 — orchestrator verification gate).
