---
phase: 07-tool-catalog
plan: 01
subsystem: tool-catalog
tags: [catalog, build-args, image-hash, canonical-encoding, mutation-test]

requires:
  - phase: 06-container-collapse
    provides: "Module/Adapter package shape (internal/mountplan) cited as the deepening reference for internal/catalog"
provides:
  - "internal/catalog package with the full Phase 07-10 Entry schema (Key, Default, BuildArg + reserved Description / InitScript / SmokeTest)"
  - "Entries: 30-row alphabetical declaration folding the legacy KnownTools slice and ToolBuildArg map into one source of truth"
  - "Typed accessors: Keys, BuildArg, Defaults, IsDefault"
  - "Canonical encoder: WriteCanonicalEntries (parameterised) + WriteCanonical (thin wrapper over Entries)"
  - "D-10 mutation test (TestCanonicalEncodingIsNeutralToOptionalFieldPopulation) that locks optional fields out of the encoding"
affects: [07-02, 07-03, 07-04, 07-05, 07-06, 10-init-manifest]

tech-stack:
  added: []
  patterns:
    - "Module-package shape (mirrors internal/mountplan): minimal exported surface, package doc spelling out pipeline + invariants"
    - "Parameterised encoder + thin wrapper (WriteCanonicalEntries / WriteCanonical) so production callers consume the package-level table while tests can inject custom slices"
    - "Mutation test (byte-equality under field population) instead of string-grep, to lock structural invariants against future format drift"

key-files:
  created:
    - internal/catalog/catalog.go
    - internal/catalog/catalog_test.go
  modified: []

key-decisions:
  - "D-05 enforced — no back-compat re-exports of KnownTools / ToolBuildArg; subsequent waves migrate consumers to typed accessors"
  - "D-09 / D-10 enforced — canonical encoding is (Key, Default, BuildArg) only; optional fields are excluded so future Phase 10 population is hash-neutral"
  - "Encoder split into parameterised (WriteCanonicalEntries) + wrapper (WriteCanonical) specifically to enable D-10 mutation testing on test-local []Entry slices without touching package-level state"

patterns-established:
  - "Pattern: Single-declaration tool catalog with public typed accessors replaces parallel slice + map declarations"
  - "Pattern: Mutation test for encoder field-exclusion invariants — populate the would-be-leaked fields on an in-test fixture and assert byte-equality vs a bare fixture"

metrics:
  duration: "~10min"
  completed: "2026-05-06"
  tasks_completed: 2
  files_created: 2
  files_modified: 0
---

# Phase 07 Plan 01: Tool Catalog Foundation Summary

Introduced `internal/catalog/` as the canonical declaration of every bundled tool, replacing the parallel `KnownTools` slice + `ToolBuildArg` map with a single 30-row `Entries` table that ships the full Phase 07-10 schema (Key/Default/BuildArg + reserved Description/InitScript/SmokeTest), exposing typed accessors and a parameterised canonical encoder whose optional-field neutrality is locked by a mutation test.

## What Shipped

### Entry Schema (D-06, D-07, D-08)

```go
type Entry struct {
    Key         string // tool key in .toolbox.yaml `tools:` map
    Default     bool   // default-on/off
    BuildArg    string // Dockerfile ARG name, e.g. "INSTALL_GH"
    Description string // Phase 10: zero-valued in Phase 07
    InitScript  string // Phase 10: zero-valued in Phase 07
    SmokeTest   string // Phase 09/10: zero-valued in Phase 07
}
```

Six fields total — the three load-bearing fields (Key, Default, BuildArg) drive build args + image hash today; the three reserved fields are declared up front so the schema does not churn when Phase 10 lands the init manifest. Phase 07 leaves all three reserved fields zero-valued; the canonical encoder explicitly excludes them so Phase 10 population stays hash-neutral.

### Public Surface

| Symbol | Purpose |
| --- | --- |
| `var Entries []Entry` | Alphabetical-by-Key 30-row declaration |
| `func Keys() []string` | One key per Entry, catalog order |
| `func BuildArg(key string) string` | Key→BuildArg lookup, "" on miss |
| `func Defaults() map[string]bool` | Fresh map, every entry true (Phase 07 default-on) |
| `func IsDefault(map[string]bool) bool` | Missing key = enabled (legacy semantics) |
| `func WriteCanonicalEntries(io.Writer, []Entry, map[string]bool) error` | Parameterised deterministic encoder |
| `func WriteCanonical(io.Writer, map[string]bool) error` | Thin wrapper: `WriteCanonicalEntries(w, Entries, enabled)` |

Eight exported names total (`Entry`, `Entries`, `Keys`, `BuildArg`, `Defaults`, `IsDefault`, `WriteCanonical`, `WriteCanonicalEntries`). **No back-compat re-exports of `KnownTools` / `ToolBuildArg` (D-05)** — Plans 07-02 and 07-03 migrate consumers to the typed accessors.

### Canonical Encoding (D-09)

```
tool:<key>|<resolved-bool>|<build-arg>\n
```

`<resolved-bool>` is `enabled[Key]` if present, else `Default`. Iteration in slice order — production callers use the alphabetical package-level `Entries`. Description / InitScript / SmokeTest are NOT part of the format and the function body does not reference those fields.

## Tests (10 total — all passing)

| # | Test | Asserts |
| --- | --- | --- |
| 1 | TestCatalogShape | All entries have non-empty Key + BuildArg; optional fields zero (D-08) |
| 2 | TestCatalogAlphabeticalByKey | Entries sorted ascending by Key |
| 3 | TestCatalogContainsLegacyKnownTools | Key set equals `config.KnownTools` exactly (bridging — REMOVED in 07-03) |
| 4 | TestCatalogBuildArgMatchesLegacyMap | BuildArg matches `config.ToolBuildArg` (bridging — REMOVED in 07-03) |
| 5 | TestKeysReturnsAllEntries | `Keys()` returns one string per Entry, in catalog order |
| 6 | TestBuildArgLookup | `BuildArg("rtk")=="INSTALL_RTK"`, `BuildArg("unknown")==""` |
| 7 | TestDefaultsAllEnabled | `Defaults()` map all-true, length matches Entries |
| 8 | TestIsDefaultMatchesLegacy | Defaults / empty / `{rtk:false}` semantics match legacy |
| 9 | TestCanonicalEncodingDeterministic | `WriteCanonical` byte-stable across calls; lines sorted by Key |
| 10 | TestCanonicalEncodingIsNeutralToOptionalFieldPopulation | **D-10 mutation test** — `bytes.Equal` of WriteCanonicalEntries output for bare vs populated `[]Entry` slices |

Test 10 is a **mutation test, not a string-grep**: it constructs two test-local `[]Entry` fixtures (`bareEntries` zero-valued optional fields, `populatedEntries` with non-empty Description/InitScript/SmokeTest) and asserts `bytes.Equal` of `WriteCanonicalEntries` output. This catches any encoder-format change that would serialise the optional fields, regardless of whether the field names appear as literals in the output (the previous string-grep formulation could not catch a new encoder format like `desc:%s` that omits the field name).

Tests 3 and 4 reference `config.KnownTools` / `config.ToolBuildArg` — they are intentionally bridging the legacy and catalog representations during the migration window. **Plan 07-03 removes them BEFORE deleting the legacy literals**, so every intermediate commit compiles.

Tests live in `package catalog_test` (external test package) so they exercise only the exported public surface, satisfying the test-via-public-API discipline carried over from the Phase 06 lesson. The plan's prose mentioned `package catalog`, but its own code samples used `catalog.X` qualified references that compile only in `package catalog_test`; choosing the external form resolves the inconsistency in favour of the public-API discipline (#critical-invariant 4) and the plan's acceptance criterion that tests must not reach unexported names.

## What Did Not Ship (Intentional)

- **Legacy literals** — `internal/config/tools.go` is untouched. `KnownTools`, `ToolBuildArg`, `DefaultTools`, `IsDefaultTools` all still exist and still work; **Plan 07-03** deletes them after Plan 07-02 migrates the build-side consumer.
- **Consumer wiring** — `internal/build/tag.go:103-111` (the canonical encoding loop the catalog will replace) is untouched; **Plan 07-02** migrates that consumer.
- **Description / InitScript / SmokeTest population** — fields are declared, all zero-valued; **Phase 10** populates them when the init manifest lands.

## Deviations from Plan

### Test package choice

The plan's prose stated `package catalog` (internal test package) but the plan's own code samples used `catalog.Entries`, `catalog.Keys()`, etc. — qualified references that compile only in `package catalog_test`. I chose the external test package to:
1. Match the executable code samples in the plan body
2. Satisfy the plan's own acceptance criterion that tests must not reference unexported names
3. Honour `<critical_invariants>` item 4 ("Test-via-public-API discipline") in the spawning prompt

This is a documentation-vs-code consistency call, not a substantive deviation — the test surface, assertions, and behaviour are exactly as specified.

### Otherwise

None — plan executed exactly as written. All 10 tests pass, both `make go-lint` and `make go-test` green.

## Verification

| Check | Result |
| --- | --- |
| `make go-test` | exit 0; all packages green; `internal/catalog` tests all PASS |
| `make go-lint` | exit 0; 0 issues |
| `grep -c "var Entries"` in catalog.go | 1 |
| `grep -c "^func "` in catalog.go | 6 |
| `grep -c "func WriteCanonicalEntries"` | 1 |
| `grep -c "return WriteCanonicalEntries(w, Entries"` | 1 |
| `grep -c '^func Test'` in catalog_test.go | 10 |
| `git diff --stat HEAD -- internal/config internal/build` | empty (untouched) |

## Threat Flags

None. Plan 07-01 is a pure-internal Go package with no I/O, no untrusted input, and no new trust boundaries. The two STRIDE-register threats (T-07-01 catalog drift vs legacy literals; T-07-02 optional-field leakage in canonical encoding) are mitigated by Tests 3+4 and Test 10 respectively — all four passing.

## Commits

- `e9f455d` feat(07-01): add internal/catalog package with Entry schema and accessors
- `d676891` test(07-01): add internal/catalog tests covering shape, accessors, and D-10 mutation invariant

## Self-Check: PASSED

- `internal/catalog/catalog.go` exists — FOUND
- `internal/catalog/catalog_test.go` exists — FOUND
- Commit `e9f455d` exists — FOUND
- Commit `d676891` exists — FOUND
- All 10 tests run and PASS in `go test -v ./internal/catalog/...`
- `make go-lint` exits 0
- `internal/config/tools.go` and `internal/build/tag.go` unchanged (CAT-02 deletion + Wave 2 migration intentionally deferred)
