# State: Toolbox

## Current Position

Phase: 03-cicd (complete — human verification pending)
Plan: —
Status: Phase 03 verified (automated) — 3 human UAT items pending
Last activity: 2026-04-18 — Phase 03 execution and verification complete

```
[##########] Phase 03/03 complete (100%)
```

## Project Reference

See: .planning/PROJECT.md (updated 2026-04-17)

**Core value:** Un singolo comando (`toolbox shell`) ti mette dentro un ambiente completo, isolato e riproducibile.
**Current focus:** CI/CD workflow per build automatizzata e push su GHCR (Phase 03)

## Performance Metrics

| Metric | Value |
|--------|-------|
| Phases complete | 2/3 |
| Requirements complete | 16/20 |
| Plans complete | 1/1 (phase 03) |
| 02-01 duration | 4m 23s |
| 03-01 duration | 76s |

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
| Test-before-push pattern | Smoke test prima del push su GHCR, evita immagini rotte con tag latest | Confirmed |
| cache-to solo sul primo build | Evita sovrascrittura scope cache tra i due build-push-action | Confirmed |

## Blockers

(None)

## Todos

(None)

---

*Last updated: 2026-04-18 — plan 03-01 auto tasks complete*
