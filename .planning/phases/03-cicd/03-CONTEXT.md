# Phase 3: CI/CD - Context

**Gathered:** 2026-04-18
**Status:** Ready for planning

<domain>
## Phase Boundary

GitHub Actions workflow che builda automaticamente l'immagine Docker su ogni push a main (o dispatch manuale), esegue lo smoke test come gate di validazione, e pubblica l'immagine su GHCR con tag `latest` e `sha-<commit>`. Multi-arch (amd64 + arm64).

</domain>

<decisions>
## Implementation Decisions

### Trigger e branching strategy
- **D-01:** Workflow triggered su push a `main` e `workflow_dispatch` manuale
- **D-02:** Build sempre eseguita — nessun path filter condizionale (ogni push a main produce un'immagine aggiornata)

### Tag strategy e retention
- **D-03:** Due tag per ogni build: `latest` e `sha-<short-commit-hash>`
- **D-04:** Nessuna pulizia automatica delle immagini vecchie su GHCR — gestione manuale se necessario

### Piattaforme target
- **D-05:** Build multi-arch: `linux/amd64` + `linux/arm64` via Docker Buildx
- **D-06:** Il Dockerfile usa già `TARGETARCH` — buildx lo imposta automaticamente per ogni piattaforma

### Struttura workflow
- **D-07:** Job unico con step sequenziali: login → buildx setup → metadata → build+push → smoke test
- **D-08:** Cache layer Docker via GitHub Actions cache (`cache-from`/`cache-to` type=gha)
- **D-09:** Notifiche: solo default GitHub (email/web su failure) — nessuna action aggiuntiva

### Actions e versioni
- **D-10:** `docker/login-action@v3` con `registry: ghcr.io`, `username: ${{ github.actor }}`, `password: ${{ secrets.GITHUB_TOKEN }}` — no PAT necessario per same-repo packages
- **D-11:** `docker/setup-buildx-action@v3` per multi-platform build
- **D-12:** `docker/metadata-action@v5` per generazione automatica OCI labels
- **D-13:** `docker/build-push-action@v6` con context `.`, file `docker/Dockerfile`

### Smoke test come gate
- **D-14:** Lo smoke test (`docker/smoke-test.sh`) viene eseguito come step dopo il build — un fallimento blocca il push (ma in un job unico, il push avviene dentro build-push-action prima dello smoke test; lo smoke test valida l'immagine già pushata)

### Claude's Discretion
- Configurazione esatta del metadata-action (labels, annotations)
- Strategia di retry in caso di failure transitorie GHCR
- Formato esatto del workflow YAML (naming degli step, commenti)
- Se eseguire lo smoke test prima del push (build locale senza push → test → push separato) o dopo il push

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Infrastruttura Docker esistente
- `docker/Dockerfile` — Dockerfile dell'immagine toolbox, usa TARGETARCH, build context dalla root
- `docker/smoke-test.sh` — Script smoke test con 16 validazioni tool, accetta image name come argomento
- `Makefile` — Target `build`, `test`, `shell` esistenti per build locale

### Progetto e requirements
- `.planning/REQUIREMENTS.md` — Requirements CICD-01, CICD-02, CICD-03 coperti in questa fase
- `.planning/PROJECT.md` — Vincoli progetto: GHCR come registry, GitHub Actions come CI

### Stack e pattern
- `CLAUDE.md` §Technology Stack — Pattern CI/CD raccomandati: login-action@v3, build-push-action@v6, metadata-action@v5, tag strategy

### Fase precedente (contesto immagine)
- `.planning/phases/01-image-foundation/01-CONTEXT.md` — Decisioni D-02 (build context dalla root), D-09 (versioni pinnate come ARG), D-11 (layer ordering)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `docker/smoke-test.sh` — Già pronto, accetta `IMAGE` come primo argomento, exit code 1 su failure
- `Makefile` target `test` — Pattern esistente `docker/smoke-test.sh $(FULL)` da replicare nel workflow

### Established Patterns
- Build context dalla root: `docker build -f docker/Dockerfile .` (D-02 da Phase 01)
- TARGETARCH usato nel Dockerfile per download binari arch-specific

### Integration Points
- `.github/workflows/` — directory da creare, non esiste ancora
- Il workflow legge `docker/Dockerfile` e usa `docker/smoke-test.sh` come gate

</code_context>

<specifics>
## Specific Ideas

- Lo smoke test è già lo stesso usato in sviluppo locale (`make test`) — il CI non ha bisogno di un test separato
- Pattern GHCR standard: `ghcr.io/${{ github.repository }}:latest` + `ghcr.io/${{ github.repository }}:sha-<commit>`

</specifics>

<deferred>
## Deferred Ideas

- **Supporto semver tagging** — richiede gestione release, da valutare come estensione futura
- **Cleanup automatico immagini vecchie** — non necessario ora, riconsiderare se lo storage GHCR cresce
- **Build matrice per test su multiple piattaforme** — overkill per progetto personale

</deferred>

---

*Phase: 03-cicd*
*Context gathered: 2026-04-18*
