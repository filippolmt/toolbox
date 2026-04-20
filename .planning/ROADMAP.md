# Roadmap: Toolbox

**Current milestone:** v1.1 Shell & DX enhancements
**Goal:** Rendere zsh la shell interattiva di default nel container toolbox con Oh-My-Zsh, mantenendo bash selezionabile via config.
**Previous milestone:** v1.0 Toolbox (3 phases, complete 2026-04-18)
**Granularity:** Standard

---

## Phases

### v1.0 (complete)

- [x] **Phase 01: Image Foundation** - Docker image completa con tutti i tool a versioni pinnate e utente non-root
- [x] **Phase 02: CLI Go** - Binary Go (Cobra + Viper) per gestione del ciclo di vita del container dall'host
- [x] **Phase 03: CI/CD** - GitHub Actions per build automatizzata e push su GHCR

### v1.1 (in progress)

- [ ] **Phase 04: Zsh Shell Bundle** - zsh + Oh-My-Zsh + plugin come shell di default, selezionabile via `shell` in `~/.toolbox.yaml`

---

## Phase Details

### Phase 01: Image Foundation
**Goal**: L'immagine Docker e' disponibile localmente con tutti i tool pronti all'uso, accessibili come utente non-root con alias e completions configurati.
**Depends on**: Nothing (first phase)
**Requirements**: IMG-01, IMG-02, IMG-03, IMG-04, IMG-05, IMG-06, TOOL-01, TOOL-02, TOOL-03, TOOL-04, TOOL-05, TOOL-06, TOOL-07
**Success Criteria** (what must be TRUE):
  1. `docker build` produce un'immagine funzionante senza errori
  2. Il container si avvia come utente non-root con UID/GID mappati dall'host
  3. Ogni tool (kubectl, helm, tofu, gh, glab, docker, claude) e' disponibile nel PATH con la versione corretta
  4. Gli alias (`k`, `h`, `tf`, `d`) e le completions bash sono attivi nella sessione
  5. Lo smoke test (`make smoke-test`) passa tutte le 16 validazioni senza errori
**Plans**: TBD
**Status**: COMPLETE

### Phase 02: CLI Go
**Goal**: Il binary `toolbox` gira sull'host e gestisce il ciclo di vita del container con un singolo comando, leggendo la configurazione da `~/.toolbox.yaml`.
**Depends on**: Phase 01
**Requirements**: CLI-01, CLI-02, CLI-03, CLI-04, CLI-05
**Success Criteria** (what must be TRUE):
  1. `toolbox shell` avvia il container con i volumi configurati e attacca stdin/stdout/stderr con TTY interattivo
  2. `toolbox build` builda l'immagine Docker localmente senza richiedere comandi Docker separati
  3. `toolbox stop` ferma e rimuove il container in esecuzione
  4. `~/.toolbox.yaml` controlla mount path, nome immagine e nome container, e le modifiche hanno effetto senza ricompilare
  5. `toolbox completion zsh` (e bash/fish) genera gli script di completion installabili nello shell
**Plans:** 3 plans
Plans:
- [x] 02-01-PLAN.md — Fondazione Go: modulo, root command, config YAML, mount resolver, UI helpers
- [x] 02-02-PLAN.md — Comando shell: container lifecycle, TTY attach, signal forwarding
- [x] 02-03-PLAN.md — Comandi build, stop, completion
**UI hint**: no

### Phase 03: CI/CD
**Goal**: Ogni push su `main` produce automaticamente una nuova immagine validata e pubblicata su GHCR, disponibile per il pull.
**Depends on**: Phase 01
**Requirements**: CICD-01, CICD-02, CICD-03
**Success Criteria** (what must be TRUE):
  1. Un push su `main` (o `workflow_dispatch` manuale) avvia il workflow e pubblica l'immagine su GHCR senza intervento manuale
  2. L'immagine pubblicata e' accessibile con entrambi i tag `latest` e `sha-<commit>` su `ghcr.io`
  3. Lo smoke test viene eseguito come step di validazione nella pipeline e un fallimento blocca il push
**Plans:** 1 plan
Plans:
- [x] 03-01-PLAN.md — Workflow GitHub Actions: build, smoke test, push multi-arch su GHCR (Task 2: human-verify pending)
**UI hint**: no

### Phase 04: Zsh Shell Bundle
**Goal**: `toolbox shell` avvia zsh (con Oh-My-Zsh e plugin di produttività) come default, mantenendo bash come opzione configurabile da `~/.toolbox.yaml` e degradando con errore chiaro se lo stato di config è incoerente.
**Depends on**: Phase 02 (CLI Go config pipeline), Phase 01 (image build pipeline)
**Requirements**: ZSH-01, ZSH-02, ZSH-03, ZSH-04, ZSH-05, SHELL-01, SHELL-02, SHELL-03, SHELL-04
**Success Criteria** (what must be TRUE):
  1. Con config di default, `toolbox shell` entra in zsh con Oh-My-Zsh, starship prompt, autosuggestions e syntax highlighting attivi
  2. Impostando `shell: bash` in `~/.toolbox.yaml`, `toolbox shell` entra in bash senza toccare zsh
  3. Impostando `shell: zsh` e `tools.zsh: false`, `toolbox shell` esce con errore non-zero e messaggio chiaro su stderr prima di avviare il container
  4. Un valore `shell` non supportato fa fallire la CLI con lista dei valori accettati
  5. Smoke test verde con `tools.zsh: true`: zsh presente, `~/.oh-my-zsh` popolata, i tre plugin caricati
  6. Alias infra (`k`, `h`, `tf`, `d`) e completions per kubectl/helm/gh/glab/yq/docker/git/gcloud funzionano in zsh come in bash
**Plans**: TBD (da creare via `/gsd-plan-phase 4`)
**Status**: Not started

---

## Progress

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 01. Image Foundation | — | Complete | 2026-04-17 |
| 02. CLI Go | 3/3 | Complete | 2026-04-18 |
| 03. CI/CD | 1/1 | Complete (human-verify pending) | 2026-04-18 |
| 04. Zsh Shell Bundle | 0/? | Not started | — |

---

## Coverage

### v1.0

| Requirement | Phase | Status |
|-------------|-------|--------|
| IMG-01 | Phase 01 | Complete |
| IMG-02 | Phase 01 | Complete |
| IMG-03 | Phase 01 | Complete |
| IMG-04 | Phase 01 | Complete |
| IMG-05 | Phase 01 | Complete |
| IMG-06 | Phase 01 | Complete |
| TOOL-01 | Phase 01 | Complete |
| TOOL-02 | Phase 01 | Complete |
| TOOL-03 | Phase 01 | Complete |
| TOOL-04 | Phase 01 | Complete |
| TOOL-05 | Phase 01 | Complete |
| TOOL-06 | Phase 01 | Complete |
| TOOL-07 | Phase 01 | Complete |
| CLI-01 | Phase 02 | Complete |
| CLI-02 | Phase 02 | Complete |
| CLI-03 | Phase 02 | Complete |
| CLI-04 | Phase 02 | Complete |
| CLI-05 | Phase 02 | Complete |
| CICD-01 | Phase 03 | Complete |
| CICD-02 | Phase 03 | Complete |
| CICD-03 | Phase 03 | Complete |

**v1.0 coverage: 20/20 requirements mapped. No orphans.**

### v1.1

| Requirement | Phase | Status |
|-------------|-------|--------|
| ZSH-01 | Phase 04 | Pending |
| ZSH-02 | Phase 04 | Pending |
| ZSH-03 | Phase 04 | Pending |
| ZSH-04 | Phase 04 | Pending |
| ZSH-05 | Phase 04 | Pending |
| SHELL-01 | Phase 04 | Pending |
| SHELL-02 | Phase 04 | Pending |
| SHELL-03 | Phase 04 | Pending |
| SHELL-04 | Phase 04 | Pending |

**v1.1 coverage: 9/9 requirements mapped. No orphans.**

---

*Created: 2026-04-17 (reconstruction after Phase 01 completion)*
*Updated: 2026-04-20 — v1.1 milestone opened, Phase 04 added*
