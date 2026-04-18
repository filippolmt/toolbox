---
phase: 02-cli-go
plan: 02
subsystem: container-lifecycle
tags: [go, docker-sdk, tty, container, shell, cobra]
dependency_graph:
  requires: [go-module, root-command, config-system, mount-resolver, ui-helpers]
  provides: [shell-command, container-lifecycle, tty-attach, container-stop]
  affects: [cmd/shell.go, internal/container]
tech_stack:
  added: [docker-sdk-v28.5.2, golang.org/x/term-v0.42.0, errdefs, ocispec]
  patterns: [state-machine-lifecycle, tty-raw-mode, signal-forwarding, package-level-fn-mock]
key_files:
  created:
    - internal/container/lifecycle.go
    - internal/container/attach.go
    - cmd/shell.go
    - internal/container/lifecycle_test.go
  modified:
    - go.mod
    - go.sum
decisions:
  - "errdefs.IsNotFound al posto di client.IsErrNotFound (API v28.5.2 usa errdefs)"
  - "ImageInspect al posto di ImageInspectWithRaw (API cambiata in v28.5.2)"
  - "Package-level var execShellFn per permettere mock nei test senza interfacce aggiuntive"
  - "notFoundError custom nel test con metodo NotFound() per soddisfare errdefs.IsNotFound"
metrics:
  duration: 4m11s
  completed: 2026-04-18T07:41:20Z
  tasks_completed: 2
  tasks_total: 2
  files_created: 4
  files_modified: 2
---

# Phase 02 Plan 02: Container Lifecycle e Shell Command Summary

Implementazione completa del comando `toolbox shell` con state machine per il ciclo di vita container (create/start/exec), TTY attach con raw mode e signal forwarding, e test unitari con mock Docker client.

## Task Completion

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Container lifecycle e TTY attach | 1e783df | lifecycle.go, attach.go, go.mod, go.sum |
| 2 | Cobra shell command e test lifecycle | 7a37a74 | cmd/shell.go, lifecycle_test.go |

## Implementation Details

### Container Lifecycle (lifecycle.go)

State machine a 3 rami:
- **Running**: exec diretto nel container esistente (nessuna creazione)
- **Stopped**: ContainerStart + exec
- **Not found**: verifica immagine con ImageInspect, ContainerCreate + ContainerStart + exec

Costante `ContainerName = "toolbox"` (D-03, nome fisso non configurabile).

Stop con timeout 10s + Force remove per evitare container zombie (Pitfall 5).

### TTY Attach (attach.go)

- Raw mode via `term.MakeRaw` con `defer term.Restore` immediato (Pitfall 1)
- Signal handler per SIGINT/SIGTERM che fa restore del terminale prima di exit
- SIGWINCH forwarding per resize terminale + resize iniziale
- I/O bidirezionale con `io.Copy` in goroutine
- Graceful degradation se non in terminale (skip raw mode)

### Shell Command (cmd/shell.go)

Cobra command con `RunE` che orchestra: `config.Load()` -> `container.NewClient()` -> `container.Shell(ctx, cli, cfg)`

### Test Coverage (lifecycle_test.go)

8 test con mock Docker client:
- `TestContainerNameIsFixed` - costante D-03
- `TestShellExecInRunningContainer` - branch running
- `TestShellStartsStoppedContainer` - branch stopped
- `TestShellCreatesNewContainer` - branch not found con immagine presente
- `TestShellErrorOnMissingImage` - errore "toolbox build" per immagine mancante
- `TestStopAndRemove` - stop + remove con Force=true
- `TestStopContainerNotFound` - gestione graceful container assente
- `TestNotFoundErrorSatisfiesErrdefs` - validazione mock error type

Pattern: `var execShellFn = execShell` a livello package per sostituire la funzione di attach nei test senza richiedere Docker daemon.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fix API signature ContainerCreate nel mock**
- **Found during:** Task 2
- **Issue:** Il mock usava `interface{}` per networkConfig e platform, ma l'API v28.5.2 richiede `*network.NetworkingConfig` e `*ocispec.Platform`
- **Fix:** Aggiornata signature mock con tipi corretti + import network e ocispec
- **Files modified:** internal/container/lifecycle_test.go
- **Commit:** 7a37a74

**2. [Rule 3 - Blocking] API ImageInspect cambiata rispetto al piano**
- **Found during:** Task 1
- **Issue:** Il piano specifica `ImageInspectWithRaw` ma l'API v28.5.2 ha solo `ImageInspect` con variadic options
- **Fix:** Usato `ImageInspect` al posto di `ImageInspectWithRaw`
- **Files modified:** internal/container/lifecycle.go
- **Commit:** 1e783df

**3. [Rule 3 - Blocking] errdefs.IsNotFound al posto di client.IsErrNotFound**
- **Found during:** Task 1
- **Issue:** Il piano usa `client.IsErrNotFound` ma v28.5.2 usa `errdefs.IsNotFound` dal package `github.com/docker/docker/errdefs`
- **Fix:** Import errdefs e uso di `errdefs.IsNotFound`
- **Files modified:** internal/container/lifecycle.go
- **Commit:** 1e783df

## Verification Results

```
go build ./...                                     -- PASS
go test ./internal/container/ -v -count=1 -short   -- PASS (8/8 tests)
./toolbox shell --help                             -- Shows "Avvia una sessione shell nel container toolbox"
./toolbox --help | grep shell                      -- "shell       Avvia una sessione shell nel container toolbox"
```

## Threat Mitigations Applied

| Threat ID | Mitigation | Status |
|-----------|-----------|--------|
| T-02-05 | ImageInspect prima di ContainerCreate, ContainerName fisso "toolbox" | Applicata |
| T-02-06 | defer term.Restore + signal handler, timeout 10s su Stop, Force remove | Applicata |

## Self-Check: PASSED

- All 4 created files exist on disk
- Both task commits (1e783df, 7a37a74) found in git history
- go build ./... compiles without errors
- go test passes 8/8 tests
- toolbox shell --help shows expected output
