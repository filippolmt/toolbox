# Phase 2: CLI Go - Context

**Gathered:** 2026-04-18
**Status:** Ready for planning

<domain>
## Phase Boundary

Binary Go `toolbox` che gira sull'host e gestisce il ciclo di vita del container Docker (create, exec, stop+remove, build) leggendo configurazione da `~/.toolbox.yaml` con override locale da `.toolbox.yaml` nel progetto corrente e env var `TOOLBOX_*`. Genera shell completions per bash/zsh/fish.

</domain>

<decisions>
## Implementation Decisions

### Ciclo di vita container
- **D-01:** `toolbox shell` — se il container è già running, esegue `exec` nel container esistente (nuova sessione shell). Se non esiste, lo crea e avvia. Un solo container attivo alla volta.
- **D-02:** `toolbox stop` — ferma E rimuove il container (stop + remove). Nessun container residuo. Tutti i dati persistono sui volumi host.
- **D-03:** Nome container fisso `toolbox` — non configurabile. Semplice e prevedibile.

### Schema YAML config
- **D-04:** Config multi-path: `.toolbox.yaml` (progetto corrente, priorità alta) → `~/.toolbox.yaml` (globale) → env var `TOOLBOX_*` → defaults built-in. Merge: progetto vince su globale.
- **D-05:** Config opzionale — zero-config out of the box. `toolbox shell` funziona subito con defaults sensati. Il YAML serve solo per personalizzare.
- **D-06:** Schema YAML raggruppato per area semantica: `image:` (name, tag), `mounts:` (source, target, readonly), `build:` (context, dockerfile). Scalabile per future opzioni.

### Volume mount di default
- **D-07:** Mount di default (senza config file): `~/.claude` (rw), `~/.gitconfig` + `~/.gitconfig-dbm` (ro), `~/.ssh` (ro), `/var/run/docker.sock` (rw).
- **D-08:** `~/.secrets` NON montato di default — solo se configurato esplicitamente nel YAML.
- **D-09:** Path mancanti: warning e skip. Mostra avviso ma avvia comunque il container senza quel mount. Non blocca mai l'avvio.

### Output e UX della CLI
- **D-10:** Output colorato con simboli — ✔ verdi, ⚠ gialli, ✖ rossi. Coerente con l'entrypoint della Phase 1.
- **D-11:** Usare `charmbracelet/gum` per output TUI ricco e interattivo (spinner, confirm, choose).
- **D-12:** `toolbox build` — stream output Docker in tempo reale, come `docker build` diretto. Utile per debug.

### Claude's Discretion
- Struttura directory Go (cmd/, internal/, pkg/) — seguire convenzioni standard Cobra
- Dettagli implementativi del Docker SDK (ContainerCreate config, HostConfig)
- Gestione segnali (SIGINT, SIGTERM) durante exec
- Struttura interna del default config (valori specifici)

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Progetto
- `.planning/PROJECT.md` — Vincoli: Cobra v1.10.2, Viper v1.21.0, Docker SDK v29.4.0, CLI su host
- `.planning/REQUIREMENTS.md` — Requirements CLI-01 → CLI-05 coperti in questa fase
- `CLAUDE.md` §Technology Stack — Stack raccomandato, alternative scartate, version compatibility

### Phase 1 (dipendenza)
- `docker/Dockerfile` — Immagine da buildare con `toolbox build`
- `docker/smoke-test.sh` — Test da integrare nel build workflow
- `.planning/phases/01-image-foundation/01-CONTEXT.md` — Decisioni Phase 1 (struttura flat docker/, build context da root)

### Librerie esterne
- `https://github.com/charmbracelet/gum` — TUI tool Go per output ricco (spinner, confirm, choose). Da usare per l'output della CLI.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `docker/Dockerfile` — Immagine da buildare con `toolbox build` (build context dalla root)
- `docker/smoke-test.sh` — Validazione post-build

### Established Patterns
- Phase 1: struttura flat in `docker/` — la CLI Go andrà nella root o in `cmd/`
- Phase 1: alias e convenzioni shell (✅/⚠️ pattern output) — la CLI segue lo stesso stile

### Integration Points
- `toolbox build` invoca `docker build -f docker/Dockerfile .` (build context root)
- `toolbox shell` monta i volumi e avvia il container dall'immagine buildata in Phase 1
- Shell completions: Cobra genera nativamente per bash/zsh/fish

</code_context>

<specifics>
## Specific Ideas

- Output CLI con simboli ✔/⚠/✖ — stesso linguaggio visivo dell'entrypoint Phase 1
- `charmbracelet/gum` per spinner durante operazioni lunghe e output interattivo
- Zero-config experience: `toolbox shell` funziona al primo lancio senza nessun file YAML
- Mount skip con warning: mai bloccare l'avvio per un path mancante

</specifics>

<deferred>
## Deferred Ideas

- `toolbox status` — comando per vedere stato del container (CLI-06, future requirement)
- `toolbox update` — pull ultima immagine da GHCR (CLI-07, future requirement)
- Nome container configurabile — per ora fisso, in futuro se servono ambienti paralleli

</deferred>

---

*Phase: 02-cli-go*
*Context gathered: 2026-04-18*
