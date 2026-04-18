# Requirements: Toolbox

**Defined:** 2026-04-16
**Reconstructed:** 2026-04-17
**Core Value:** Un singolo comando (`toolbox shell`) ti mette dentro un ambiente completo, isolato e riproducibile.

## v1.0 Requirements

### Image

- [x] **IMG-01**: Docker image basata su node:22-bookworm-slim con apt dependencies
- [x] **IMG-02**: Tool infra a versioni pinnate con verifica SHA256 (kubectl, helm, tofu, gh, glab, docker CLI)
- [x] **IMG-03**: Claude Code installato via npm globale
- [x] **IMG-04**: Utente non-root `toolbox` con UID/GID mapping dall'host
- [x] **IMG-05**: Entrypoint con health check non-bloccanti per credenziali cloud (gh, glab, gcloud, az, oci)
- [x] **IMG-06**: Bashrc con alias infra, completions bash e starship prompt

### Tools

- [x] **TOOL-01**: kubectl completions e alias `k`
- [x] **TOOL-02**: helm completions e alias `h`
- [x] **TOOL-03**: tofu completions e alias `tf`
- [x] **TOOL-04**: gh completions
- [x] **TOOL-05**: glab completions
- [x] **TOOL-06**: docker completions e alias `d`
- [x] **TOOL-07**: Smoke test che valida la presenza e versione di ogni tool installato

### CLI

- [ ] **CLI-01**: Comando `toolbox shell` che avvia il container e attacca stdin/stdout/stderr con TTY
- [ ] **CLI-02**: Comando `toolbox build` che builda l'immagine Docker localmente
- [ ] **CLI-03**: Comando `toolbox stop` che ferma e rimuove il container in esecuzione
- [x] **CLI-04**: File di configurazione YAML (`~/.toolbox.yaml`) con mount path, immagine, nome container
- [ ] **CLI-05**: Shell completion nativa per bash, zsh e fish via Cobra

### CI/CD

- [ ] **CICD-01**: GitHub Actions workflow che builda l'immagine su push a main e workflow_dispatch
- [ ] **CICD-02**: Push su GHCR con tag `latest` e `sha-<commit>`
- [ ] **CICD-03**: Smoke test eseguito come step di validazione nella pipeline prima del push

## Future Requirements

### Estensioni CLI

- **CLI-06**: Comando `toolbox status` per vedere stato del container
- **CLI-07**: Comando `toolbox update` per pull dell'ultima immagine da GHCR

### Estensioni Image

- **IMG-07**: Tool aggiuntivi (bat, fd, dust) come layer opzionale
- **IMG-08**: Supporto multi-arch (amd64 + arm64) nel CI

## Out of Scope

| Feature | Reason |
|---------|--------|
| Docker-in-Docker | Richiede privileged mode, complessità eccessiva — Docker socket mount è sufficiente |
| Alpine base image | musl libc rompe npm install di Claude Code (native bindings) |
| GUI/Desktop app | CLI-only tool, nessuna interfaccia grafica necessaria |
| Viper v2 | API non stabile — usare v1.21.0 |

## Traceability

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
| CLI-04 | Phase 02 | Complete |
| CLI-05 | Phase 02 | Pending |
| CICD-01 | Phase 03 | Pending |
| CICD-02 | Phase 03 | Pending |
| CICD-03 | Phase 03 | Pending |

**Coverage:**
- v1.0 requirements: 20 total
- Mapped to phases: 20
- Unmapped: 0 ✓

---
*Requirements defined: 2026-04-16*
*Last updated: 2026-04-17 after reconstruction*
