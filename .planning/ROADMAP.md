# Roadmap: Toolbox v1.0

**Milestone:** v1.0 Toolbox
**Goal:** Ambiente di sviluppo containerizzato completo con CLI Go per gestione ciclo di vita e CI/CD per build automatizzate su GHCR.
**Created:** 2026-04-17 (reconstructed)
**Granularity:** Standard

---

## Phases

- [x] **Phase 01: Image Foundation** - Docker image completa con tutti i tool a versioni pinnate e utente non-root
- [x] **Phase 02: CLI Go** - Binary Go (Cobra + Viper) per gestione del ciclo di vita del container dall'host
- [ ] **Phase 03: CI/CD** - GitHub Actions per build automatizzata e push su GHCR

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

---

## Progress

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 01. Image Foundation | — | Complete | 2026-04-17 |
| 02. CLI Go | 3/3 | Complete | 2026-04-18 |
| 03. CI/CD | 1/1 | Complete (human-verify pending) | 2026-04-18 |

---

## Coverage

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
| CLI-01 | Phase 02 | Pending |
| CLI-02 | Phase 02 | Pending |
| CLI-03 | Phase 02 | Pending |
| CLI-04 | Phase 02 | Pending |
| CLI-05 | Phase 02 | Pending |
| CICD-01 | Phase 03 | Complete |
| CICD-02 | Phase 03 | Complete |
| CICD-03 | Phase 03 | Complete |

**v1.0 coverage: 20/20 requirements mapped. No orphans.**

---

*Created: 2026-04-17 (reconstruction after Phase 01 completion)*
