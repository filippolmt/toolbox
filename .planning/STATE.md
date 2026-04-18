# State: Toolbox

## Current Position

Phase: 03-cicd (next)
Plan: —
Status: Phase 02 complete — ready for Phase 03
Last activity: 2026-04-18 — Phase 02 verified and approved

```
[######----] Phase 02/03 complete (67%)
```

## Project Reference

See: .planning/PROJECT.md (updated 2026-04-17)

**Core value:** Un singolo comando (`toolbox shell`) ti mette dentro un ambiente completo, isolato e riproducibile.
**Current focus:** CLI Go per gestione container (Phase 02)

## Performance Metrics

| Metric | Value |
|--------|-------|
| Phases complete | 1/3 |
| Requirements complete | 13/20 |
| Plans complete | 1/3 (phase 02) |
| 02-01 duration | 4m 23s |

## Accumulated Context

### From Phase 01 (image-foundation) — COMPLETE
- Docker image completa con 10+ tool a versioni pinnate (kubectl, helm, tofu, gh, glab, docker CLI, claude)
- Base image: `node:22-bookworm-slim` (Debian Bookworm, glibc — compatibile con Claude Code npm install)
- Entrypoint con health check non-bloccanti per credenziali cloud (gh, glab, gcloud, az, oci)
- Bashrc con alias infra (`k`, `h`, `tf`, `d`), completions bash, starship prompt
- Smoke test con 16 validazioni (Makefile target `make smoke-test`)
- Build testata su arm64
- Layer ordering per cache efficiency: apt → binari → starship → npm → scripts → user

### Phase 02 context (CLI Go)
- Stack: Go + Cobra v1.10.2 + Viper v1.21.0 + Docker SDK (github.com/docker/docker v29.4.0)
- CLI binary gira sull'HOST, non dentro il container
- Configurazione: `~/.toolbox.yaml` via Viper
- Distribuzione: `go install` (non dentro l'immagine)
- Multi-stage Dockerfile per CLI: `golang:1.26-bookworm` builder → binary copiato, non necessario nell'immagine finale

## Decisions

| Decision | Rationale | Status |
|----------|-----------|--------|
| Debian slim over Alpine | musl libc rompe Claude Code npm install | Confirmed |
| Docker socket mount over DinD | Semplicita', no privileged mode | Confirmed |
| Cobra + Viper over kong/urfave | Viper YAML integration, industry standard | Confirmed |
| Moby client over shell exec | Typed SDK, piu' sicuro di string interpolation | Confirmed |
| Docker SDK v28.5.2 (not v29.4.0) | v29.4.0 non esiste come Go module; v28.5.2 ultima stabile | Confirmed |
| Multi-stage Dockerfile per CLI | Go toolchain fuori dalla final image | Pending impl |

## Blockers

(None)

## Todos

(None)

---

*Last updated: 2026-04-18 — plan 02-01 complete*
