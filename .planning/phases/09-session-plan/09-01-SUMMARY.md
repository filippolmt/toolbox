---
phase: 09-session-plan
plan: 01
subsystem: infra
tags: [sessionplan, mountplan, container, docker-sdk, plan-merge-twin-seam, tdd]

# Dependency graph
requires:
  - phase: 06-container-collapse
    provides: single-sectioned-file discipline + test-via-public-Seam rule
  - phase: 07-tool-catalog
    provides: typed-accessors-only discipline + DOCS-01 glossary parity pattern
  - phase: 08-config-plan
    provides: Plan + Merge twin-Seam pattern + workspace normalization once at top
provides:
  - "internal/sessionplan package with public Seams Plan / Merge / MissingPublishPorts / ContainerNameFor"
  - "SessionPlan / MergedSessionPlan / Image typed structs encoding the full Docker session shape"
  - "Verbatim helper migration (containerNameFor, shellEnv, parsePublishSpecs, missingPublishPorts) from internal/container/lifecycle.go to sessionplan"
  - "Wave-0 contract tests pinning SESS-05: pure-data + fs-tier coverage without Docker SDK"
affects:
  - 09-02 (Plan 02 wires cmd/shell.go and lifecycle.Shell to consume *SessionPlan, deletes the now-duplicate helpers from lifecycle.go)
  - 09-03 (Plan 03 ships DOCS-01 glossary entry + CLAUDE.md architecture line)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Plan + Merge twin Seam (Mount Plan / Config Plan precedent carried forward)"
    - "Asymmetric Binds field type per Seam: SessionPlan.Binds []mountplan.Bind, MergedSessionPlan.Binds []config.Mount"
    - "Verbatim helper migration with intentional duplication across phases (Phase 06/07/08 discipline)"
    - "Single sectioned file with Public Seams / Port Parsing / Workspace Env banners"

key-files:
  created:
    - internal/sessionplan/plan.go
    - internal/sessionplan/plan_test.go
  modified: []

key-decisions:
  - "Two distinct result types (SessionPlan + MergedSessionPlan) instead of one struct with sum-typed Binds — call-site readability + Phase 06 single-typed-struct precedent"
  - "ContainerNameFor exported (capitalised) with ContainerNamePrefix const exported alongside, so Plan 02's Stop / StopAll thinning can call sessionplan without taking a *SessionPlan input"
  - "MergedSessionPlan.WorkingDir defaults to mountplan.WorkspaceTarget — pure-merge tier cannot consult the fs to evaluate the mirror predicate; Plan tier sees the resolved mirror"
  - "formatPublishMismatch stays in internal/container (UI-formatting concern, D-13 split criterion); sessionplan exports the missing-list only"
  - "Cmd / SecurityOpt NOT yet on SessionPlan — Plan 02 will land them once the lifecycle thinning consumes the Seam"

patterns-established:
  - "Workspace Normalization Once at Plan Top (Pitfall 8): filepath.Abs + filepath.Clean at Plan/Merge entry; lifecycle no longer normalizes"
  - "No Docker SDK in sessionplan tests: container.InspectResponse constructed as struct literal — daemon never invoked"
  - "Test-via-public-Seam discipline: tests cross Plan / Merge / MissingPublishPorts / ContainerNameFor only; never reach into parsePublishSpecs / shellEnv"

requirements-completed: [SESS-01, SESS-03, SESS-04, SESS-05]

# Metrics
duration: 8m 24s
completed: 2026-05-07
---

# Phase 09 Plan 01: Session Plan Bootstrap Summary

**Bootstrap del package `internal/sessionplan` con il twin Seam Plan + Merge, i tipi tipizzati `SessionPlan` / `MergedSessionPlan` / `Image`, e i Wave-0 test che pinnano la composizione di `build.ResolveImage` + `mountplan.Plan` / `mountplan.Merge` senza invocare il Docker SDK.**

## Performance

- **Duration:** 8 min 24 s
- **Started:** 2026-05-07T16:14:04Z
- **Completed:** 2026-05-07T16:22:28Z
- **Tasks:** 2 (TDD: skeleton + RED + GREEN)
- **Files modified:** 2 (entrambi nuovi)

## Accomplishments

- Nuovo package `internal/sessionplan` (273 righe) che espone i Seam pubblici `Plan(cfg, workspace, ports, cliVersion) → (*SessionPlan, error)` e `Merge(cfg, workspace, ports, cliVersion) → (*MergedSessionPlan, error)`.
- Plan compone l'intera pipeline a cinque stadi: workspace normalize → port parse → image resolve → mount compose → container-name + env synth.
- Merge compone la versione pura: stessi cinque stadi ma `mountplan.Merge` al posto di `mountplan.Plan` — nessun side-effect filesystem, nessun setup HOME nei test.
- Migrazione verbatim degli helper da `internal/container/lifecycle.go`: `ContainerNameFor` (era `containerNameFor`), `ContainerNamePrefix`, `shellEnv`, `parsePublishSpecs`, `MissingPublishPorts` (era `missingPublishPorts`) — formato hash byte-per-byte preservato così che `Stop` / `StopAll` continueranno a funzionare quando Plan 02 li ricablerà.
- 13 funzioni di test (429 righe) — copertura per ogni decisione del Seam: image tag, port parse, mount delegation, container-name determinism, workspace normalization, env synthesis, port-mismatch table (incluso il caso nil-base e nil-HostConfig). Zero `client.APIClient` / `mockClient`.

## Task Commits

Ogni task committato atomicamente; Task 2 segue il flusso TDD RED → GREEN:

1. **Task 1: bootstrap skeleton** — `6ad2ac8` (feat) — package + tipi + Plan/Merge stubs + helper verbatim
2. **Task 2 RED: failing Wave-0 tests** — `1929ffd` (test) — 13 test funcs, 9 falliscono contro gli stub
3. **Task 2 GREEN: wire Plan/Merge bodies** — `82a4177` (feat) — composition completa, tutti i test verdi + lint pulito

_Note: Task 2 segue TDD plan-level. Refactor commit non necessario: il primo passaggio era già pulito post-fix lint._

## Files Created/Modified

- `internal/sessionplan/plan.go` — nuovo package: tipi `Image` / `SessionPlan` / `MergedSessionPlan`, Seam `Plan` / `Merge` / `MissingPublishPorts` / `ContainerNameFor`, helper migrati `parsePublishSpecs` / `shellEnv` / `sanitizeRe` / `ContainerNamePrefix`. Sezionato con i banner `Public Seams` / `Port Parsing` / `Workspace Env`.
- `internal/sessionplan/plan_test.go` — 13 test funcs in due tier: Plan tier (9 test, usano `t.TempDir` + `t.Setenv("HOME", ...)`), Merge tier (3 test, puramente data, nessun fs setup), `TestMissingPublishPortsTable` (5 sotto-casi tabulati assorbiti da `lifecycle_test.go::TestShellPublishMismatchWarning`).

## Decisions Made

- **Due tipi distinti (`SessionPlan` / `MergedSessionPlan`) anziché uno struct con campo `Binds` sum-typed.** L'asimmetria tra `[]mountplan.Bind` (Plan, sources risolti) e `[]config.Mount` (Merge, sources raw) è esplicita nel sistema di tipi; i call site non devono fare type assertion. Mirroring di Phase 06 single-typed-struct precedent.
- **`ContainerNameFor` + `ContainerNamePrefix` esportati.** `Stop` e `StopAll` non hanno un `*SessionPlan` in scope, quindi devono poter chiamare l'helper standalone. Esportare il prefix mantiene la simmetria con il filter naming usato da `StopAll`.
- **`MergedSessionPlan.WorkingDir` default a `mountplan.WorkspaceTarget`.** Il mirror predicate (`WorkspaceMirrorPath`) è fs-aware nel design futuro; il pure-merge tier non può consultare il fs senza rompere il contratto SESS-05. Documentato inline.
- **`formatPublishMismatch` resta in `internal/container`** (D-13 split criterion: UI-formatting). Sessionplan espone solo la lista mancante; il messaging vive nel layer che già conosce le convenzioni `ui.Warning`.
- **`Cmd` / `SecurityOpt` rimandati al Plan 02.** Questo plan crea la skeleton e wire la composizione del path felice; aggiungere campi che lifecycle non consuma ancora produrrebbe campi morti. Plan 02 li aggiunge insieme al ricablaggio del consumer.

## Deviations from Plan

None — il plan è stato eseguito esattamente come scritto.

L'unico micro-aggiustamento è stato un fix lint in fase GREEN: la riga `var _ []config.Mount = merged.Binds` (type-assertion al Merge tier) ha fatto trip il rule `staticcheck QF1011` ("could omit type"). Sostituita con `_ = []config.Mount(merged.Binds)` — slice cast equivalente, lint pulito. Stesso commit (Task 2 GREEN), nessun nuovo commit dedicato.

---

**Total deviations:** 0
**Impact on plan:** Zero — tutti i 13 test passano via `make go-test`, `make go-lint` clean, contratto SESS-05 pinned.

## Issues Encountered

- **Worktree cwd-drift mid-execution.** I Make target hanno `HOST_SRC := $(if $(TOOLBOX_HOST_WORKSPACE),$(TOOLBOX_HOST_WORKSPACE),$(CURDIR))`: senza `TOOLBOX_HOST_WORKSPACE`, Make passa `$(CURDIR)` che il setup del processo aveva risolto al main repo, non al worktree dell'agente. Sintomo: il primo `Write` con path assoluto è atterrato in `/Users/filippo/project/github/toolbox/internal/sessionplan/` (main repo) invece che nel worktree. Risoluzione: file spostato nel worktree con `mv`, `make go-test` invocato sempre con `TOOLBOX_HOST_WORKSPACE="$(git rev-parse --show-toplevel)"`. Nessuna riga di codice persa, nessun commit zombie.

## User Setup Required

None — nessuna configurazione esterna richiesta.

## Next Phase Readiness

- **Plan 02 unblocked.** Il package `internal/sessionplan` compila pulito con `Plan` e `Merge` completamente cablati. Plan 02 ora può:
  1. Cambiare la firma di `cmd/shell.go::runShell` per costruire `*sessionplan.SessionPlan` e passarlo a `container.Shell(ctx, cli, plan)`.
  2. Cambiare la firma di `lifecycle.Shell` da `(ctx, cli, cfg, workspace, publish)` a `(ctx, cli, plan)`.
  3. Cancellare gli helper duplicati da `lifecycle.go` (`containerNameFor`, `shellEnv`, `parsePublishSpecs`, `missingPublishPorts`) — la duplicazione è intenzionale nel Plan 01 per mantenere `cmd/shell.go` e `lifecycle.Shell` compilabili senza modifiche.
  4. Far chiamare a `Stop` / `StopAll` `sessionplan.ContainerNameFor` e `sessionplan.ContainerNamePrefix` invece dei nomi locali (deprecabili).
  5. Migrare i casi tabulati di `TestShellPublishMismatchWarning` (già coperti da `TestMissingPublishPortsTable` qui) e ridurre il test di lifecycle a "warning emesso", non al contenuto della lista.

- **Plan 03 unblocked per DOCS-01.** Il glossario `Session Plan` può puntare al package esistente con la formulazione "before this concept was named, cmd/shell.go::runShell e lifecycle.Shell facevano il sequencing inline" — già allineata con il package doc del file.

---
*Phase: 09-session-plan*
*Completed: 2026-05-07*

## Self-Check: PASSED

- File `internal/sessionplan/plan.go` exists.
- File `internal/sessionplan/plan_test.go` exists.
- File `.planning/phases/09-session-plan/09-01-SUMMARY.md` exists.
- Commits `6ad2ac8`, `1929ffd`, `82a4177` present in git log.
