---
phase: 02-cli-go
plan: 01
subsystem: cli-foundation
tags: [go, cobra, viper, config, mount, ui]
dependency_graph:
  requires: []
  provides: [go-module, root-command, config-system, mount-resolver, ui-helpers]
  affects: [cmd/root.go, internal/config, internal/mount, internal/ui]
tech_stack:
  added: [cobra-v1.10.2, viper-v1.21.0, docker-sdk-v28.5.2, huh-v2.0.3, lipgloss-v2.0.3, x/term]
  patterns: [viper-multi-path-config, mapstructure-unmarshal, filepath-clean-sanitization]
key_files:
  created:
    - main.go
    - cmd/root.go
    - internal/config/config.go
    - internal/config/config_test.go
    - internal/mount/resolve.go
    - internal/mount/resolve_test.go
    - internal/ui/output.go
    - internal/ui/spinner.go
  modified:
    - go.mod
    - go.sum
    - Makefile
    - .gitignore
decisions:
  - "Docker SDK v28.5.2 al posto di v29.4.0 (non esiste come Go module)"
  - "os.UserHomeDir() al posto di go-homedir (stdlib sufficiente)"
metrics:
  duration: 4m 23s
  completed: 2026-04-18T07:33:06Z
---

# Phase 02 Plan 01: Fondazione Go Summary

Modulo Go inizializzato con Cobra root command, Viper multi-path config (progetto > globale > env > defaults), sistema di configurazione con structs mapstructure, mount resolver con skip per path mancanti e filepath.Clean, UI helpers colorati con lipgloss v2 e spinner huh v2.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Go module, main.go, root command con Viper multi-path config | c73c3e4 | main.go, cmd/root.go, go.mod, go.sum, .gitignore |
| 2 | Config struct, mount resolver, UI helpers e test | bf26753 | internal/config/config.go, internal/config/config_test.go, internal/mount/resolve.go, internal/mount/resolve_test.go, internal/ui/output.go, internal/ui/spinner.go, Makefile |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Docker SDK v29.4.0 non esiste come Go module**
- **Found during:** Task 1
- **Issue:** `go get github.com/docker/docker@v29.4.0` fallisce — il modulo usa versioning `+incompatible` e l'ultima release e' v28.5.2
- **Fix:** Usato `github.com/docker/docker@v28.5.2+incompatible` (ultima stabile disponibile)
- **Files modified:** go.mod, go.sum
- **Commit:** c73c3e4

## Verification Results

```
go build ./...          -> OK
go test ./... -count=1  -> 6/6 test passano
./toolbox --help        -> Output corretto
```

## Test Coverage

| Package | Tests | Status |
|---------|-------|--------|
| internal/config | TestDefaultMounts, TestImageRef, TestLoadWithoutConfig | PASS |
| internal/mount | TestExpandHome, TestResolveMountsSkipsMissing, TestResolveMountsFormat | PASS |

## Known Stubs

Nessuno. Tutti i componenti sono funzionali con dati reali (defaults built-in, path resolution dal filesystem).

## Self-Check: PASSED

- All 8 created files exist on disk
- Both task commits (c73c3e4, bf26753) verified in git log
