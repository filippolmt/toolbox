# Phase 3: CI/CD - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-04-18
**Phase:** 03-cicd
**Areas discussed:** Trigger e branching strategy, Tag strategy e retention, Piattaforme target, Struttura workflow

---

## Trigger e branching strategy

### Quando buildare

| Option | Description | Selected |
|--------|-------------|----------|
| Push su main + dispatch | Build automatica su merge a main + workflow_dispatch manuale | ✓ |
| Push + PR check | Come sopra, più build senza push sulle PR | |
| Solo workflow_dispatch | Build solo manuale | |

**User's choice:** Push su main + dispatch (Raccomandato)
**Notes:** Pattern standard per progetti personali

### Build condizionale

| Option | Description | Selected |
|--------|-------------|----------|
| Sempre build | Ogni push su main builda, immagine latest sempre aggiornata | ✓ |
| Solo se docker/ cambia | Path filter su docker/, risparmia minuti ma possibile disallineamento | |

**User's choice:** Sempre build (Raccomandato)

---

## Tag strategy e retention

### Strategia tagging

| Option | Description | Selected |
|--------|-------------|----------|
| latest + sha-<commit> | Pattern da CLAUDE.md, semplice e sufficiente | ✓ |
| latest + sha + data | Aggiunge tag con data per visibilità età immagine | |
| latest + sha + semver | Aggiunge tag versione, richiede gestione release | |

**User's choice:** latest + sha-<commit> (Raccomandato)

### Pulizia immagini

| Option | Description | Selected |
|--------|-------------|----------|
| Nessuna pulizia | Layer condivisi, poco spazio, non vale automatizzare | ✓ |
| Pulizia manuale | Nota nel README per pulire a mano | |
| Cleanup automatico | Action dedicata, più complesso | |

**User's choice:** Nessuna pulizia (Raccomandato)

---

## Piattaforme target

| Option | Description | Selected |
|--------|-------------|----------|
| Solo linux/amd64 | Più semplice, Docker Desktop emula su Apple Silicon | |
| Multi-arch: amd64 + arm64 | Buildx nativo, arm64 ottimizzato per Apple Silicon, TARGETARCH già usato | ✓ |
| Solo linux/arm64 | Solo per Apple Silicon, nessuna compatibilità amd64 | |

**User's choice:** Multi-arch: amd64 + arm64 (Raccomandato)

---

## Struttura workflow

### Job structure

| Option | Description | Selected |
|--------|-------------|----------|
| Job unico sequenziale | Login → buildx → metadata → build+push → smoke test, un solo runner | ✓ |
| Multi-job: build → test → push | Separazione netta, serve passare immagine tra job | |

**User's choice:** Job unico con step sequenziali (Raccomandato)

### Caching

| Option | Description | Selected |
|--------|-------------|----------|
| GitHub Actions cache | cache-from/cache-to type=gha, integrato, 10GB limite | ✓ |
| Registry cache | Cache su GHCR, più persistente ma più complesso | |
| Nessun cache | Build da zero ogni volta | |

**User's choice:** GitHub Actions cache (Raccomandato)

### Notifiche

| Option | Description | Selected |
|--------|-------------|----------|
| Solo GitHub default | Email/web su failure, nessuna action extra | ✓ |
| Slack/Discord | Action dedicata, overkill per progetto personale | |

**User's choice:** Solo GitHub default (Raccomandato)

---

## Claude's Discretion

- Configurazione metadata-action (labels, annotations)
- Retry strategy per failure GHCR
- Naming e commenti nel workflow YAML
- Ordine smoke test vs push (prima o dopo)

## Deferred Ideas

- Semver tagging — richiede gestione release
- Cleanup automatico immagini vecchie
- Build matrice multi-piattaforma per test
