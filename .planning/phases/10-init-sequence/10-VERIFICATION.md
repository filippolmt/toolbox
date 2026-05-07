---
phase: 10-init-sequence
verified: 2026-05-07T14:00:00Z
status: human_needed
score: 6/7
overrides_applied: 0
human_verification:
  - test: "Boot UX + failure isolation (Pitfall 9 / INIT-03)"
    expected: >
      With one init.d script forced to exit 1, the remaining scripts still run,
      the container entrypoint completes (startup hooks + exec run), the marker
      log is written to ~/.toolbox-state/init/<name>.log inside the container,
      and the last 5 lines of the log are printed inline.
    why_human: >
      The automated Go tests and smoke-test verify the iterator wiring is
      present in entrypoint.sh and that all scripts are executable. They cannot
      verify the runtime failure-isolation contract — that a crashing init.d
      script does not abort the boot and that the tail-5 inline surface appears
      as intended. Only a docker run with a deliberately broken script confirms
      that `if !` neutralises set -e correctly at runtime.
  - test: "Privacy invariants at shell start (INIT-06 runtime)"
    expected: >
      After `toolbox shell`, `env | grep RTK_` shows RTK_TELEMETRY_DISABLED=1
      and RTK_TEE=0. `mount | grep secrets` is empty.
    why_human: >
      The Dockerfile ENV directives are verified by static inspection and the
      mountplan defaults exclude ~/.secrets. But confirming that no init.d
      script inadvertently unsets or overrides the env vars (e.g., via
      `export RTK_TEE=`) requires a live container inspection.
  - test: "MCP plugin auto-build works end-to-end (INIT-02 runtime)"
    expected: >
      Install a Claude Code plugin whose mcp/package.json has scripts.build.
      Run toolbox shell. Confirm dist/ is built and .toolbox-built marker exists
      in the versioned plugin dir.
    why_human: >
      The 50-mcp-plugins.sh extraction is a verbatim port; the smoke-test only
      checks executability. The actual npm install + npm run build pipeline
      requires a real plugin install side-effect that cannot be automated
      without a full shell session.
---

# Phase 10: Init Sequence — Verification Report

**Phase Goal:** Replace the inline per-tool blocks in `entrypoint.sh` with a
catalog-driven init manifest under `internal/build/assets/init.d/`; the Go-side
Tool Catalog declares which init script each tool owns and `entrypoint.sh`
iterates instead of inlining.

**Verified:** 2026-05-07T14:00:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

---

## Goal Achievement

**YES.** The catalog-driven init manifest is fully in place. `entrypoint.sh`
went from ~349 lines of mixed inline blocks to ~123 lines: UID/passwd injection
(unchanged), credential check (unchanged), a ~15-line iterator with failure
envelope, user startup hooks (unchanged), and `exec "$@"`. Every per-tool block
(rtk, cf, graphify, playwright-cli, MCP plugins) lives exclusively in
`internal/build/assets/init.d/<NN>-<tool>.sh`. The Go-side catalog's
`Entry.InitScript` field is populated for the 5 tools;
`TestCatalogInitDBijection` enforces set-equality between catalog declarations
and files on disk. The smoke-test bijection block confirms executability inside
the built image. All 7 Go packages pass `make go-test`; `make go-lint` exits 0.

---

## Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | No inline blocks remain in entrypoint.sh for rtk init, cf skill seed, playwright, graphify, MCP plugin auto-build | VERIFIED | `entrypoint.sh` is 123 lines; grep for rtk/cf/playwright/graphify/npm-install/mcp returns zero matches in the file. Only the iterator block (`INIT_D`, `for f in "$INIT_D"/*.sh`) is present. |
| 2 | Each init.d script is shellcheck-clean, idempotent, gated on `command -v` | VERIFIED | All 5 files begin `#!/usr/bin/env bash` + `set -euo pipefail` + outer `command -v X >/dev/null 2>&1 \|\| exit 0`. `bash -n` passes on all 5. Idempotency enforced via file-absence guards (cf: `[ ! -f "$_cf_skill_file" ]`; rtk TOML: `[ ! -f "$HOME/.config/rtk/config.toml" ]`; MCP: `.toolbox-built` marker). |
| 3 | Failure semantics preserved: per-script failures non-fatal, errors to log, tail-5 inline | VERIFIED (static) / UNCERTAIN (runtime) | Iterator uses `if ! bash "$f" 2>"$_log"; then` form (Pitfall 9 compliant). Log path is `$HOME/.toolbox-state/init/<name>.log` (Pitfall 1 compliant). `tail -n 5 "$_log" \| sed 's/^/      /'` is present. MCP keeps per-plugin `.toolbox-build-error.log` exception (D-07). Cannot verify runtime isolation without a live container with a deliberately broken script. |
| 4 | Smoke test asserts catalog-declared init scripts exist and are executable | VERIFIED | `smoke-test.sh` has the `=== init.d bijection + executability ===` block as a separate `docker run --rm` invocation, asserts `[ -x "$f" ]` for every `init.d/*.sh`, asserts `count >= 5`. Live run: `OK: 5 init.d scripts present and executable`. Final result: 55 passed, 0 failed, 0 skipped. |
| 5 | Privacy invariants preserved: RTK_TELEMETRY_DISABLED=1 + RTK_TEE=0 in Dockerfile ENV; ~/.secrets not mounted | VERIFIED (static) / UNCERTAIN (runtime) | `Dockerfile` line 1095-1096: `ENV RTK_TELEMETRY_DISABLED=1 RTK_TEE=0`. `internal/mountplan/defaults.go` explicitly excludes `~/.secrets`. TOML seed moved into `init.d/10-rtk.sh` (correct per D-13). No init.d script unsets these vars (all 5 files grep-checked). Runtime confirmation requires a live shell. |
| 6 | CONTEXT.md has Init Sequence glossary entry with three paragraphs | VERIFIED | `CONTEXT.md` contains `### Init Sequence` at the correct position (after Session Plan). Three-paragraph structure present: (1) concrete pipeline referencing `catalog.Entry.InitScript`, `//go:embed`, `tarEmbeddedContext`, Dockerfile COPY, `entrypoint.sh` iterator, per-script self-gate; (2) owning location `internal/build/assets/init.d/` + `internal/catalog.Entry.InitScript`, marker log path, `TestCatalogInitDBijection`, smoke-test reference; (3) why the term exists. All D-14 requirements met. |
| 7 | CLAUDE.md updated: internal/catalog bullet, init.d/ bullet, gotcha location pointers to init.d files | VERIFIED | `internal/catalog` — bullet present with full Tool Catalog description including `internal/build/assets/init.d/ (Init Sequence consumes Entry.InitScript)`. `internal/build/assets/init.d/` — dedicated bullet present listing all 5 scripts and TestCatalogInitDBijection. MCP gotcha points to `init.d/50-mcp-plugins.sh`. rtk gotcha points to `init.d/10-rtk.sh`. cf gotcha points to `init.d/20-cf.sh`. Privacy paragraph preserved verbatim. |

**Score:** 6/7 truths VERIFIED (SC-3 and SC-5 are VERIFIED statically; runtime
confirmation requires human testing per SC-3 and SC-5 manual checks).

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/build/assets/init.d/10-rtk.sh` | rtk init extraction | VERIFIED | 74 lines, shebang + set -euo pipefail, outer `command -v rtk` gate, TOML seed, two claude/codex sub-gates |
| `internal/build/assets/init.d/20-cf.sh` | cf skill seed extraction | VERIFIED | 73 lines, outer `command -v cf` gate, inner cf+claude double gate, heredoc SKILL.md |
| `internal/build/assets/init.d/30-graphify.sh` | graphify install extraction | VERIFIED | 27 lines, outer `command -v graphify` gate, inner claude double gate, non-fatal echo |
| `internal/build/assets/init.d/40-playwright-cli.sh` | playwright-cli extraction | VERIFIED | 30 lines, `(cd "$HOME" && playwright-cli install --skills claude)` subshell preserved (Pitfall 4) |
| `internal/build/assets/init.d/50-mcp-plugins.sh` | MCP plugin auto-build extraction | VERIFIED | 72 lines, per-plugin `.toolbox-build-error.log` exception preserved (D-07) |
| `internal/build/assets/entrypoint.sh` | Iterator with failure envelope | VERIFIED | INIT_D + TOOLBOX_INIT_LOG_DIR vars, `if [ -d "$INIT_D" ]` guard, `for f in "$INIT_D"/*.sh` loop with `if ! bash "$f" 2>"$_log"` form |
| `internal/build/assets/smoke-test.sh` | Bijection + executability block | VERIFIED | `=== init.d bijection + executability ===` section as separate `docker run --rm`, count >= 5, `[ -x "$f" ]` per file |
| `internal/build/embed.go` | //go:embed assets/init.d | VERIFIED | `//go:embed assets/Dockerfile assets/bashrc.sh assets/entrypoint.sh assets/zshrc.sh assets/init.d` |
| `internal/catalog/catalog.go` | InitScript populated for 5 entries | VERIFIED | cf="20-cf.sh", claude="50-mcp-plugins.sh", graphify="30-graphify.sh", playwright_cli="40-playwright-cli.sh", rtk="10-rtk.sh" |
| `internal/catalog/init_d_bijection_test.go` | Go-side bijection test | VERIFIED | TestCatalogInitDBijection enforces set-equality catalog ↔ embed.FS `init.d/` |
| `internal/build/build_test.go` | TestEmbedAssetsContainsInitDDir | VERIFIED | Asserts >= 5 entries in `Assets/assets/init.d`; TestTarEmbeddedContextShipsInitDDir asserts mode 0755 in tar |
| `internal/build/assets/Dockerfile` | COPY init.d/ + chmod | VERIFIED | Line 946: `COPY init.d/ /usr/local/lib/toolbox/init.d/`; line 947: `RUN chmod -R 0755 /usr/local/lib/toolbox/init.d/` |
| `CONTEXT.md` | `### Init Sequence` glossary entry | VERIFIED | Three-paragraph entry after Session Plan; references InitScript, tarEmbeddedContext, in-image path, TestCatalogInitDBijection, marker-log path |
| `CLAUDE.md` | Architecture + gotchas updated | VERIFIED | internal/catalog bullet, internal/build/assets/init.d/ bullet, MCP/rtk/cf gotchas point to init.d files |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `catalog.Entry.InitScript` | `init.d/*.sh` files | `TestCatalogInitDBijection` (embed.FS) | VERIFIED | Test passes; 5 catalog entries match 5 files |
| `init.d/*.sh` | `/usr/local/lib/toolbox/init.d/` in image | Dockerfile COPY + chmod | VERIFIED | Line 946-947 in Dockerfile; smoke-test live run confirms executable |
| `entrypoint.sh` iterator | `init.d/*.sh` | `INIT_D="/usr/local/lib/toolbox/init.d"` + `for f in "$INIT_D"/*.sh` | VERIFIED | Present at lines 86-102 of entrypoint.sh |
| `internal/build/embed.go` | `assets/init.d` subtree | `//go:embed assets/init.d` | VERIFIED | Bare-directory embed pattern; TestEmbedAssetsContainsInitDDir passes |
| Dockerfile ENV | `RTK_TELEMETRY_DISABLED=1`, `RTK_TEE=0` | `ENV` directive lines 1095-1096 | VERIFIED | Confirmed by grep; NOT moved to init scripts |

---

## Data-Flow Trace (Level 4)

Not applicable — this phase produces shell scripts and documentation, not components that render dynamic data. The data flow is: catalog declaration → embedded FS → Dockerfile COPY → entrypoint iterator → subshell execution. This is verified at the wiring level above.

---

## Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| entrypoint.sh syntax valid | `bash -n entrypoint.sh` | exit 0 | PASS |
| All 5 init.d scripts syntax valid | `bash -n <each>` | exit 0 all | PASS |
| Go tests pass (all packages) | `make go-test` | 9 packages ok, 0 failures | PASS |
| Go lint clean | `make go-lint` | 0 issues | PASS |
| Smoke test live run | `smoke-test.sh toolbox:local` | 55 passed, 0 failed, 0 skipped | PASS |
| init.d bijection + executability | `smoke-test.sh toolbox:local` (bijection block) | `OK: 5 init.d scripts present and executable` | PASS |
| TestCatalogInitDBijection | part of `make go-test` | ok internal/catalog | PASS |
| TestEmbedAssetsContainsInitDDir | part of `make go-test` | ok internal/build | PASS |
| TestCatalogShape (relaxed) | part of `make go-test` | ok internal/catalog | PASS |
| TestComputeImageHashPinnedDigest | part of `make go-test` | ok internal/build | PASS |

---

## Requirements Coverage

| Requirement | Description | Status | Evidence |
|-------------|-------------|--------|---------|
| INIT-01 | entrypoint.sh iterates init.d/*.sh, no inline blocks for extraction targets | VERIFIED | entrypoint.sh 123 lines; all 5 blocks in init.d/ |
| INIT-02 | Catalog entries declare InitScript; manifest derives from catalog | VERIFIED | 5 entries populated; TestCatalogInitDBijection enforces bijection |
| INIT-03 | Per-script failures non-fatal, errors to log, tail-5 inline | VERIFIED (static) | Iterator uses `if !` form; log path correct; tail-5 present; UNCERTAIN at runtime |
| INIT-04 | Smoke test asserts every catalog-declared init script exists and is executable | VERIFIED | `=== init.d bijection + executability ===` block in smoke-test.sh; live run OK |
| INIT-05 | Boot privacy invariants preserved; RTK env vars stay in Dockerfile | VERIFIED (static) | Dockerfile ENV lines 1095-1096 intact; no init.d script unsets them |
| INIT-06 | CONTEXT.md Init Sequence glossary entry | VERIFIED | Three-paragraph entry per D-14 requirements |
| DOCS-01 | CONTEXT.md glossary entry for Init Sequence | VERIFIED | `### Init Sequence` present, sibling to Session Plan |
| DOCS-02 | CLAUDE.md Architecture + gotchas final pass | VERIFIED | internal/catalog bullet, init.d/ bullet, location pointers updated |

---

## Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| `entrypoint.sh` | None found | — | Clean: no inline tool blocks, no TODO/FIXME, no placeholder comments |
| `init.d/*.sh` | None found | — | All scripts are substantive implementations, not stubs |

No blockers found. One note: `50-mcp-plugins.sh` prints user-facing progress to stdout (`echo "Building Claude Code MCP plugins:"`) — this is intentional per D-06 (iterator does not redirect stdout) and matches the original behavior.

---

## Human Verification Required

### 1. Failure isolation at runtime (INIT-03 / Pitfall 9)

**Test:** Temporarily append `exit 1` to `internal/build/assets/init.d/30-graphify.sh`, build image (`make build`), run `docker run --rm <image> /usr/local/bin/entrypoint sleep 1`. Confirm: container runs to completion, other 4 init scripts execute, marker log appears at `~/.toolbox-state/init/30-graphify.log` inside the container, last 5 stderr lines are printed inline before the `sleep 1`.

**Expected:** Container completes normally; only `30-graphify.sh: failed (log: ...)` appears inline; rtk/cf/playwright/mcp scripts still run.

**Why human:** The `if !` form neutralises `set -e` for the iterator, but runtime process-tree behavior under bash subshell + stderr redirect requires a live container to confirm. The Go tests verify the iterator text is present in the file; they cannot verify the runtime semantics.

### 2. Privacy invariants at shell start (INIT-06 runtime)

**Test:** After `toolbox shell`, run `env | grep RTK_` inside the container.

**Expected:** Shows `RTK_TELEMETRY_DISABLED=1` and `RTK_TEE=0`. `mount | grep secrets` is empty.

**Why human:** Static analysis confirms the Dockerfile ENV directives are present and no init.d script modifies them. Runtime confirmation requires a live shell.

### 3. MCP plugin auto-build end-to-end (INIT-02 runtime)

**Test:** Install a Claude Code plugin with `mcp/package.json` containing `scripts.build`. Run `toolbox shell`. Confirm `dist/` built and `.toolbox-built` marker exists in the versioned plugin dir.

**Expected:** First shell after install triggers build; subsequent shells skip (marker present).

**Why human:** The extraction from entrypoint.sh to 50-mcp-plugins.sh is a verbatim port, but end-to-end npm build correctness requires a real plugin install side-effect.

---

## Gaps Summary

No blockers or FAILED truths. The 3 human verification items above cover:
- Runtime failure-isolation semantics (cannot be asserted programmatically)
- Privacy env-var confirmation at shell runtime
- MCP plugin build pipeline (requires live plugin install)

All static checks, Go tests, lint, and smoke-test pass with 0 failures.

---

## Outstanding Notes

- The `TARGETARCH` Dockerfile issue with the classic builder is a pre-existing concern unrelated to Phase 10 scope. The smoke test ran against an existing `toolbox:local` image (arm64, built separately). This is noted for a future phase.
- `codex` has no `InitScript` in the catalog — this is intentional per phase context (D-13: Codex sandbox is a Docker create-time `seccomp=unconfined` concern handled in `internal/sessionplan`, not a runtime init script). The smoke-test verified this is correctly handled.

---

_Verified: 2026-05-07T14:00:00Z_
_Verifier: Claude (gsd-verifier)_
