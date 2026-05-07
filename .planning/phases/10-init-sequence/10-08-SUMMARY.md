---
phase: 10-init-sequence
plan: 08
type: summary
wave: 8
status: complete
---

# Plan 10-08 SUMMARY — Init Sequence glossary entry

## Objective

Append `### Init Sequence` glossary entry to root `CONTEXT.md` (DOCS-01, D-14). Mirror the depth and three-paragraph shape of the existing Mount Plan / Tool Catalog / Config Plan / Session Plan entries.

## What was built

One docs-only edit on `CONTEXT.md`: a new `### Init Sequence` block appended immediately after the Session Plan entry's last paragraph. Three paragraphs:

1. **Concrete pipeline:** `catalog.Entry.InitScript → //go:embed assets/init.d → tarEmbeddedContext walks subtree → Dockerfile COPY+chmod → entrypoint.sh iterator with tail-5-on-failure envelope → per-script self-gate`. Marker-log path documented (`$HOME/.toolbox-state/init/<name>.log` in-container, `~/.toolbox/state/init/` host bind-mount source). MCP-plugins per-plugin `.toolbox-build-error.log` exception called out. Bijection enforcement (Go-side `TestCatalogInitDBijection` + shell-side smoke-test block) referenced.
2. **Owning location:** `internal/build/assets/init.d/` (the scripts) + `internal/catalog.Entry.InitScript` (the manifest).
3. **Why the term exists:** before naming, per-tool boot logic accumulated as inline blocks with heterogeneous failure handling; the term turns the catalog into the single discoverable list, the iterator into the single failure-envelope owner, and `<NN>-` prefix into the ordering signal.

No existing entry modified. Diff is additions-only.

## Files modified

- `CONTEXT.md` (+38 righe)

## Acceptance criteria

All grep checks pass:

- `grep -c '^### Init Sequence$' CONTEXT.md` → 1 ✓
- `grep -c '^### Mount Plan$' / Tool Catalog / Config Plan / Session Plan` → 1 each (existing entries untouched) ✓
- `grep -c 'InitScript' CONTEXT.md` → 4 (multiple references in the entry) ✓
- `grep -c 'tarEmbeddedContext' CONTEXT.md` → 1 ✓
- `grep -c '/usr/local/lib/toolbox/init.d' CONTEXT.md` → 1 ✓
- `grep -c 'TestCatalogInitDBijection' CONTEXT.md` → 1 ✓
- `grep -c '.toolbox-state/init' CONTEXT.md` → 1 ✓
- Order check (`awk` ensures `### Init Sequence` appears AFTER `### Session Plan`) → exit 0 ✓

## Deviations

None. Inline orchestrator edit (no subagent) since this is a pure docs append with no Go/build/runtime side-effects — keeps wave 8 cheap.

## Follow-up

Plan 10-09 (DOCS-02 final pass on CLAUDE.md §Architecture + §Non-obvious gotchas).
