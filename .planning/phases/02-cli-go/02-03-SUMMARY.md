---
phase: 02-cli-go
plan: 03
subsystem: cli-commands
tags: [docker-build, streaming, completion, cobra, tar-context]
dependency_graph:
  requires: [02-01]
  provides: [build-command, stop-command, completion-command, build-image-function]
  affects: [cmd/root.go]
tech_stack:
  added: [github.com/docker/docker@v28.5.2+incompatible]
  patterns: [tar-context-streaming, dockerignore-filtering, cobra-completion]
key_files:
  created:
    - internal/build/build.go
    - internal/build/build_test.go
    - internal/container/lifecycle.go
    - cmd/build.go
    - cmd/stop.go
    - cmd/completion.go
  modified:
    - go.mod
    - go.sum
decisions:
  - "Docker SDK v28.5.2+incompatible usato (coerente con Plan 01)"
  - "lifecycle.go creato come stub per compilazione — Plan 02-02 crea la versione completa in parallelo"
  - "shouldIgnore() aggiunta come helper testabile per pattern .dockerignore"
metrics:
  duration: "166s"
  completed: "2026-04-18T07:39:51Z"
  tasks_completed: 2
  tasks_total: 2
  files_created: 6
  files_modified: 2
---

# Phase 02 Plan 03: Build, Stop, Completion Commands Summary

Docker image build con tar context streaming, stop+remove container, e shell completion bash/zsh/fish via Cobra native generators.

## Task Results

### Task 1: Image build con tar context e streaming output
**Commit:** 859cf71
**Files:** internal/build/build.go, internal/build/build_test.go, internal/container/lifecycle.go

- `BuildImage()` crea tar context in goroutine con `io.Pipe`, rispetta `.dockerignore` (T-02-07)
- `streamBuildOutput()` parsa JSON streaming linea per linea con `bufio.Scanner` (D-12)
- `readDockerignore()` parsa il file ignorando commenti e righe vuote
- `shouldIgnore()` gestisce pattern directory (trailing `/`), glob (`*.md`), e match esatti
- Stub `lifecycle.go` con `NewClient()`, `Stop()`, `ContainerName` per compilazione cross-plan
- 5 test unitari: stream output, stream error, dockerignore parsing, missing dockerignore, pattern matching

### Task 2: Cobra commands build, stop, completion
**Commit:** 8d56175
**Files:** cmd/build.go, cmd/stop.go, cmd/completion.go

- `buildCmd`: carica config, crea Docker client, chiama `build.BuildImage(ctx, cli, cfg)`
- `stopCmd`: crea Docker client, chiama `container.Stop(ctx, cli)` (D-02: stop + remove)
- `completionCmd`: `ValidArgs: ["bash", "zsh", "fish"]`, `ExactArgs(1)`, `OnlyValidArgs`
  - Bash: `GenBashCompletionV2` con descriptions (CLI-05)
  - Zsh: `GenZshCompletion` — produce `#compdef toolbox` valido
  - Fish: `GenFishCompletion` con descriptions

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Docker SDK non presente in go.mod**
- **Found during:** Task 1
- **Issue:** `github.com/docker/docker` non era tra le dipendenze del progetto
- **Fix:** `go get github.com/docker/docker@v28.5.2+incompatible` + `go mod tidy`
- **Files modified:** go.mod, go.sum
- **Commit:** 859cf71

**2. [Rule 3 - Blocking] lifecycle.go non esiste nel worktree (creato da Plan 02-02 in parallelo)**
- **Found during:** Task 1
- **Issue:** `internal/container/lifecycle.go` necessario per compilazione ma creato da piano parallelo
- **Fix:** Creato stub con `NewClient()`, `Stop()`, `ContainerName` — il merge del orchestrator risolve eventuali conflitti
- **Files modified:** internal/container/lifecycle.go
- **Commit:** 859cf71

## Verification Results

```
go build ./...                          -- OK
go test ./internal/build/ -v -count=1   -- 5/5 PASS
./toolbox --help                        -- build, stop, completion listed
./toolbox completion zsh | head -5      -- #compdef toolbox (valid)
./toolbox build --help                  -- "Builda l'immagine Docker toolbox"
./toolbox stop --help                   -- "Ferma e rimuove il container toolbox"
```

## Known Stubs

| File | Line | Description | Resolution |
|------|------|-------------|------------|
| internal/container/lifecycle.go | 1-42 | Stub completo di lifecycle — Plan 02-02 crea la versione definitiva in parallelo | Merge orchestrator risolve conflitto; funzionalita' equivalente |
