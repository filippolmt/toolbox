---
phase: 02-cli-go
verified: 2026-04-18T09:46:00Z
status: human_needed
score: 5/5 must-haves verified
overrides_applied: 0
human_verification:
  - test: "Eseguire `toolbox shell` con Docker daemon attivo e immagine buildata"
    expected: "Si apre una sessione bash interattiva nel container con TTY funzionante, resize terminale, e ctrl+d esce ripristinando il terminale"
    why_human: "Richiede Docker daemon, immagine buildata, e verifica interattiva TTY/raw mode/signal forwarding"
  - test: "Eseguire `toolbox shell` due volte in terminali diversi"
    expected: "La seconda invocazione apre un exec nello stesso container (nessun secondo container creato)"
    why_human: "Richiede Docker daemon attivo e due terminali"
  - test: "Eseguire `toolbox build` nella root del progetto"
    expected: "Build Docker con output streaming in tempo reale, immagine taggata toolbox:local"
    why_human: "Richiede Docker daemon e verifica visiva dello streaming output"
  - test: "Eseguire `toolbox stop` dopo shell"
    expected: "Container fermato e rimosso, nessun container residuo (verificare con docker ps -a)"
    why_human: "Richiede Docker daemon attivo con container running"
  - test: "Modificare ~/.toolbox.yaml con mount custom e rilanciare toolbox shell"
    expected: "I nuovi mount sono applicati senza ricompilare il binary"
    why_human: "Richiede Docker daemon e verifica che il mount personalizzato e' presente nel container"
---

# Phase 02: CLI Go Verification Report

**Phase Goal:** Il binary `toolbox` gira sull'host e gestisce il ciclo di vita del container con un singolo comando, leggendo la configurazione da `~/.toolbox.yaml`.
**Verified:** 2026-04-18T09:46:00Z
**Status:** human_needed
**Re-verification:** No -- initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `toolbox shell` avvia il container con i volumi configurati e attacca stdin/stdout/stderr con TTY interattivo | VERIFIED (code) | `cmd/shell.go` chiama `container.Shell()` che crea/exec container con binds da `mount.ResolveMounts()`. `attach.go` implementa TTY raw mode, SIGWINCH resize, I/O bidirezionale. Richiede test manuale con Docker daemon. |
| 2 | `toolbox build` builda l'immagine Docker localmente senza richiedere comandi Docker separati | VERIFIED (code) | `cmd/build.go` -> `build.BuildImage()` crea tar context con `.dockerignore`, chiama `cli.ImageBuild()`, streaming JSON output. |
| 3 | `toolbox stop` ferma e rimuove il container in esecuzione | VERIFIED (code) | `cmd/stop.go` -> `container.Stop()` con timeout 10s + `RemoveOptions{Force: true}`. Test `TestStopAndRemove` verifica il flusso. |
| 4 | `~/.toolbox.yaml` controlla mount path, nome immagine e nome container, e le modifiche hanno effetto senza ricompilare | VERIFIED (code) | Viper multi-path in `cmd/root.go`: `ReadInConfig` (home) + `MergeInConfig` (progetto). `config.Load()` fa `Unmarshal` a runtime. Nessun valore hardcoded nel binary oltre i defaults. |
| 5 | `toolbox completion zsh` (e bash/fish) genera gli script di completion installabili nello shell | VERIFIED (runtime) | Testato: `toolbox completion zsh` produce `#compdef toolbox`, `bash` produce `# bash completion V2`, `fish` produce script fish valido. `ValidArgs: ["bash", "zsh", "fish"]` con `ExactArgs(1)`. |

**Score:** 5/5 truths verified (code-level; runtime con Docker richiede human)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `go.mod` | Go module con dipendenze | VERIFIED | Module `github.com/filippolmt/toolbox`, cobra v1.10.2, viper v1.21.0, docker SDK v28.5.2 |
| `main.go` | Entry point | VERIFIED | 7 righe, chiama `cmd.Execute()` |
| `cmd/root.go` | Root command con Viper multi-path | VERIFIED | `Execute()`, `initConfig()` con `ReadInConfig` + `MergeInConfig`, `SetEnvPrefix("TOOLBOX")`, `setDefaults()` |
| `cmd/shell.go` | Cobra command shell | VERIFIED | `shellCmd` con RunE, chiama `config.Load()` -> `container.Shell(ctx, cli, cfg)` |
| `cmd/build.go` | Cobra command build | VERIFIED | `buildCmd` con RunE, chiama `build.BuildImage(ctx, cli, cfg)` |
| `cmd/stop.go` | Cobra command stop | VERIFIED | `stopCmd` con RunE, chiama `container.Stop(ctx, cli)` |
| `cmd/completion.go` | Cobra command completion | VERIFIED | `GenBashCompletionV2`, `GenZshCompletion`, `GenFishCompletion`, `ValidArgs` |
| `internal/config/config.go` | Config struct + defaults | VERIFIED | `Config`, `Load()`, `DefaultMounts()` con 5 mount, `ImageRef()`. No `~/.secrets` nei defaults (D-08). |
| `internal/config/config_test.go` | Test config | VERIFIED | 3 test: `TestDefaultMounts`, `TestImageRef`, `TestLoadWithoutConfig` |
| `internal/mount/resolve.go` | Path expansion e validazione | VERIFIED | `ResolveMounts()` con `expandHome()`, `os.Stat()`, `filepath.Clean()` (T-02-01). Skip mancanti con warning (D-09). |
| `internal/mount/resolve_test.go` | Test mount | VERIFIED | 3 test: `TestExpandHome`, `TestResolveMountsSkipsMissing`, `TestResolveMountsFormat` |
| `internal/container/lifecycle.go` | Container create/exec/stop | VERIFIED | `Shell()` con state machine 3 branch (running/stopped/not found), `Stop()` con Force remove, `ContainerName = "toolbox"` (D-03). |
| `internal/container/attach.go` | TTY attach con signal forwarding | VERIFIED | `execShell()` con `term.MakeRaw`, `defer term.Restore`, SIGINT/SIGTERM handler, SIGWINCH resize, I/O bidirezionale. |
| `internal/container/lifecycle_test.go` | Test lifecycle con mock | VERIFIED | 8 test con mock Docker client che coprono tutti i branch. |
| `internal/build/build.go` | Docker image build con streaming | VERIFIED | `BuildImage()`, `createTarContext()` con `.dockerignore`, `streamBuildOutput()` con JSON parsing. |
| `internal/build/build_test.go` | Test build | VERIFIED | 5 test: stream output, stream error, dockerignore, missing dockerignore, shouldIgnore. |
| `internal/ui/output.go` | Output colorato | VERIFIED | `Success()`, `Warning()`, `Error()`, `Info()` con lipgloss v2 (D-10). |
| `internal/ui/spinner.go` | Spinner huh v2 | VERIFIED | `WithSpinner()` con `spinner.New().Title().Action().Run()` (D-11). |
| `Makefile` | Target Go | VERIFIED | `go-build:` e `go-test:` presenti. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `cmd/root.go` | `internal/config/config.go` | `initConfig` chiama Viper, `config.Load()` usato nei cmd | WIRED | `config.Load()` presente in `cmd/shell.go` e `cmd/build.go` |
| `cmd/shell.go` | `internal/container/lifecycle.go` | `container.Shell(ctx, cli, cfg)` | WIRED | Import + chiamata diretta a linea 34 |
| `cmd/build.go` | `internal/build/build.go` | `build.BuildImage(ctx, cli, cfg)` | WIRED | Import + chiamata diretta a linea 33 |
| `cmd/stop.go` | `internal/container/lifecycle.go` | `container.Stop(ctx, cli)` | WIRED | Import + chiamata diretta a linea 30 |
| `cmd/completion.go` | cobra | `GenBashCompletionV2/GenZshCompletion/GenFishCompletion` | WIRED | Chiamate dirette nel switch a linee 30-34 |
| `internal/container/lifecycle.go` | `internal/container/attach.go` | `execShellFn` (var = `execShell`) | WIRED | Variabile package-level a linea 22, chiamata in Shell() a linee 47, 55, 87 |
| `internal/container/lifecycle.go` | `internal/config/config.go` | `cfg.ImageRef()`, `mount.ResolveMounts(cfg.Mounts)` | WIRED | Import config + mount, chiamate a linee 36 e 59 |
| `internal/build/build.go` | `internal/config/config.go` | `cfg.ImageRef()`, `cfg.Build.*` | WIRED | Import config, chiamate a linee 29, 31, 36 |

### Data-Flow Trace (Level 4)

Non applicabile -- la CLI non renderizza dati dinamici in UI. I dati fluiscono da config YAML -> Viper -> Config struct -> Docker SDK. Nessun componente di rendering UI con data source da verificare.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Progetto compila | `go build ./...` | Successo, nessun errore | PASS |
| Test passano | `go test ./... -count=1 -short` | 19/19 test PASS (build:5, config:3, container:8, mount:3) | PASS |
| Help mostra subcommand | `./toolbox --help` | Elenca build, completion, shell, stop | PASS |
| Shell help corretto | `./toolbox shell --help` | "Avvia una sessione shell nel container toolbox" | PASS |
| Build help corretto | `./toolbox build --help` | "Builda l'immagine Docker toolbox" | PASS |
| Stop help corretto | `./toolbox stop --help` | "Ferma e rimuove il container toolbox" | PASS |
| Zsh completion valida | `./toolbox completion zsh \| head -2` | `#compdef toolbox` | PASS |
| Bash completion valida | `./toolbox completion bash \| head -1` | `# bash completion V2 for toolbox` | PASS |
| Fish completion valida | `./toolbox completion fish \| head -1` | `# fish completion for toolbox` | PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| CLI-01 | 02-02 | `toolbox shell` con TTY | SATISFIED | `cmd/shell.go` -> `container.Shell()` -> `execShell()` con raw mode, signal forwarding, resize. 8 test unitari. |
| CLI-02 | 02-03 | `toolbox build` locale | SATISFIED | `cmd/build.go` -> `build.BuildImage()` con tar context, .dockerignore, streaming JSON. 5 test unitari. |
| CLI-03 | 02-03 | `toolbox stop` + remove | SATISFIED | `cmd/stop.go` -> `container.Stop()` con timeout 10s + Force remove. Test `TestStopAndRemove`, `TestStopContainerNotFound`. |
| CLI-04 | 02-01 | Config YAML | SATISFIED | Viper multi-path (`~/.toolbox.yaml` + `.toolbox.yaml`), `SetEnvPrefix("TOOLBOX")`, `config.Load()` con `Unmarshal`. DefaultMounts 5 mount. Test `TestLoadWithoutConfig`. |
| CLI-05 | 02-03 | Shell completion bash/zsh/fish | SATISFIED | `cmd/completion.go` con `GenBashCompletionV2`, `GenZshCompletion`, `GenFishCompletion`. Testato runtime: output valido per tutti e 3. |

Nessun requisito orfano: tutti e 5 i requisiti CLI mappati a Phase 02 in REQUIREMENTS.md sono coperti dai piani eseguiti.

### Decision Verification

| Decision | Description | Status | Evidence |
|----------|-------------|--------|----------|
| D-01 | Shell exec in container esistente | HONORED | `lifecycle.go` linea 44-47: branch `Running` -> `execShellFn` |
| D-02 | Stop + remove | HONORED | `lifecycle.go` linea 96-115: `ContainerStop` + `ContainerRemove{Force: true}` |
| D-03 | Nome container fisso "toolbox" | HONORED | `lifecycle.go` linea 18: `const ContainerName = "toolbox"` |
| D-04 | Config multi-path, progetto vince | HONORED | `root.go` linea 42-47: `ReadInConfig` (home) + `MergeInConfig` (progetto) |
| D-05 | Zero-config | HONORED | `root.go` linea 43: `_ = viper.ReadInConfig()` ignora errore. Test `TestLoadWithoutConfig` conferma. |
| D-06 | Schema YAML raggruppato | HONORED | `config.go`: `Config{Image, Mounts, Build}` con mapstructure tags |
| D-07 | 5 mount di default | HONORED | `config.go` linea 40-47: `DefaultMounts()` con 5 mount. Test `TestDefaultMounts`. |
| D-08 | ~/.secrets NON di default | HONORED | `config.go` linea 39: commento esplicito. Test `TestDefaultMounts` verifica assenza. |
| D-09 | Path mancanti: warning + skip | HONORED | `resolve.go` linea 25-27: `os.Stat` + warning + continue. Test `TestResolveMountsSkipsMissing`. |
| D-10 | Output colorato | HONORED | `output.go`: lipgloss v2 con colori e prefissi OK/WARN/FAIL/Info |
| D-11 | huh v2 per spinner | HONORED | `spinner.go`: `charm.land/huh/v2/spinner` con `WithSpinner()` |
| D-12 | Build streaming output | HONORED | `build.go` linea 55-71: `streamBuildOutput()` con `bufio.Scanner` + JSON parsing |

### Threat Mitigation Verification

| Threat ID | Mitigation | Status |
|-----------|-----------|--------|
| T-02-01 | `filepath.Clean()` su path dopo espansione ~ | APPLIED (`resolve.go` linea 23) |
| T-02-02 | `~/.secrets` non montato di default | APPLIED (`config.go` DefaultMounts) |
| T-02-03 | Viper parsing sicuro, file locali | ACCEPTED |
| T-02-04 | Docker socket access documentato | ACCEPTED |
| T-02-05 | ImageInspect prima di Create, nome fisso | APPLIED (`lifecycle.go` linee 59, 18) |
| T-02-06 | defer Restore + signal handler, timeout, Force remove | APPLIED (`attach.go` linee 44, 52-59; `lifecycle.go` linea 108) |
| T-02-07 | .dockerignore rispettato nel tar context | APPLIED (`build.go` linea 75, 97) |
| T-02-08 | Build context grande se no .dockerignore | ACCEPTED |
| T-02-09 | Completion scripts generati da Cobra | ACCEPTED |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| (nessuno) | - | - | - | Nessun TODO/FIXME/PLACEHOLDER trovato. Nessun return vuoto sospetto (tutti i `return nil` sono flussi legittimi post-operazione). |

### Human Verification Required

### 1. Shell interattiva con Docker

**Test:** Eseguire `toolbox build` poi `toolbox shell` con Docker daemon attivo
**Expected:** Build con streaming output, poi sessione bash interattiva con TTY funzionante, resize terminale al ridimensionamento della finestra, ctrl+d esce ripristinando il terminale correttamente
**Why human:** Richiede Docker daemon, immagine buildata, e verifica interattiva del TTY/raw mode/signal forwarding

### 2. Multi-sessione nello stesso container

**Test:** Eseguire `toolbox shell` in due terminali diversi contemporaneamente
**Expected:** Seconda invocazione apre un exec nello stesso container (nessun secondo container creato, verificare con `docker ps`)
**Why human:** Richiede Docker daemon attivo e due sessioni terminale

### 3. Stop e rimozione completa

**Test:** Con container running, eseguire `toolbox stop` poi `docker ps -a | grep toolbox`
**Expected:** Container fermato, rimosso, nessun residuo in `docker ps -a`
**Why human:** Richiede Docker daemon con container running

### 4. Config YAML runtime

**Test:** Creare `~/.toolbox.yaml` con mount custom (es. `~/Projects` -> `/workspace`), eseguire `toolbox shell`, verificare che il mount e' presente nel container con `mount | grep workspace`
**Expected:** Mount personalizzato applicato senza ricompilare il binary
**Why human:** Richiede Docker daemon e verifica dei mount all'interno del container

### 5. Warning per path mancanti

**Test:** Configurare in `.toolbox.yaml` un mount con source inesistente (es. `/tmp/nonexistent`), eseguire `toolbox shell`
**Expected:** Warning "mount skipped" stampato in output, container si avvia comunque senza quel mount
**Why human:** Richiede Docker daemon e verifica visiva del warning output

### Gaps Summary

Nessun gap trovato. Tutti e 5 i success criteria della roadmap sono soddisfatti a livello di codice:

- **Compilazione:** `go build ./...` compila senza errori
- **Test:** 19/19 test passano (config: 3, mount: 3, container: 8, build: 5)
- **Comandi:** `toolbox --help` elenca tutti e 4 i subcommand (shell, build, stop, completion)
- **Completion:** Output valido per bash, zsh, fish
- **Decisioni:** Tutte le 12 decisioni (D-01 a D-12) rispettate
- **Threat mitigations:** Tutte le 9 mitigazioni applicate o accettate
- **Requisiti:** CLI-01 a CLI-05 tutti soddisfatti

Lo status e' `human_needed` perche' la verifica runtime con Docker daemon (TTY interattivo, container lifecycle reale, mount effettivi) non puo' essere fatta programmaticamente. Il codice e' completo, ben strutturato, e testato -- serve solo la conferma end-to-end con Docker.

---

_Verified: 2026-04-18T09:46:00Z_
_Verifier: Claude (gsd-verifier)_
