# State: Toolbox

## Current Position

Milestone: v1.1 — Shell & DX enhancements
Phase: 04-zsh-shell-bundle (not started)
Plan: —
Status: Requirements defined — ready for /gsd-spec-phase 4
Last activity: 2026-04-20 — v1.1 milestone opened, Phase 04 added

```
[##########] v1.0: 3/3 complete (100%)
[          ] v1.1: 0/1 (Phase 04 pending spec)
```

## Project Reference

See: .planning/REQUIREMENTS.md (updated 2026-04-20 for v1.1)

**Core value:** Un singolo comando (`toolbox shell`) ti mette dentro un ambiente completo, isolato e riproducibile.
**Current focus:** v1.1 Phase 04 — bundle zsh + Oh-My-Zsh + plugin come shell di default, config-driven via `shell` in `~/.toolbox.yaml`.

## Performance Metrics

| Metric | Value |
|--------|-------|
| v1.0 Phases complete | 3/3 |
| v1.0 Requirements complete | 20/20 |
| v1.1 Phases complete | 0/1 |
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

### Phase 02 context (CLI Go) — COMPLETE
- Stack: Go + Cobra v1.10.2 + Viper v1.21.0 + Docker SDK (github.com/docker/docker v28.5.2)
- CLI binary gira sull'HOST, non dentro il container
- Configurazione: `~/.toolbox.yaml` via Viper
- Distribuzione: `go install` (non dentro l'immagine)
- Multi-stage Dockerfile per CLI: `golang:1.26-bookworm` builder → binary copiato, non necessario nell'immagine finale

### Phase 03 context (CI/CD) — COMPLETE
- GitHub Actions workflow builda e pubblica su GHCR (`ghcr.io/filippolmt/toolbox:{latest,sha-<short>}`)
- Smoke test come gate prima del push
- Multi-arch (amd64 + arm64) con test locale su amd64

### Phase 04 context (zsh-shell-bundle) — SPEC PENDING
- Origine: issue [#37](https://github.com/filippolmt/toolbox/issues/37), ma con deviazione deliberata: **Oh-My-Zsh in scope**, **zsh come default** (non opt-in)
- Reference materiale fornito dall'utente: `.planning/phases/04-zsh-shell-bundle/REFERENCE-zshrc.md` (zshrc macOS personale — subset applicabile al container)
- Requisiti: ZSH-01..05, SHELL-01..04 (9 req)
- Punti aperti da decidere in SPEC.md: lista finale plugin OMZ, HISTFILE zsh separato, completions cachate vs inline, se includere fzf/fzf-tab

## Decisions

| Decision | Rationale | Status |
|----------|-----------|--------|
| Debian slim over Alpine | musl libc rompe Claude Code npm install | Confirmed |
| Docker socket mount over DinD | Semplicita', no privileged mode | Confirmed |
| Cobra + Viper over kong/urfave | Viper YAML integration, industry standard | Confirmed |
| Moby client over shell exec | Typed SDK, piu' sicuro di string interpolation | Confirmed |
| Docker SDK v28.5.2 (not v29.4.0) | v29.4.0 non esiste come Go module; v28.5.2 ultima stabile | Confirmed |
| Multi-stage Dockerfile per CLI | Go toolchain fuori dalla final image | Confirmed |
| Test-before-push pattern | Smoke test prima del push su GHCR, evita immagini rotte con tag latest | Confirmed |
| cache-to solo sul primo build | Evita sovrascrittura scope cache tra i due build-push-action | Confirmed |
| v1.1: Oh-My-Zsh in scope (override issue #37) | Utente vuole esplicitamente OMZ + plugin ecosystem | Confirmed (2026-04-20) |
| v1.1: zsh default shell (override issue #37) | Utente vuole zsh come default, non opt-in | Confirmed (2026-04-20) |
| v1.1: fish fuori scope | Rimandato a SHELL-05 (richiede refactor alias POSIX) | Confirmed (2026-04-20) |

## Blockers

(None)

## Todos

(None)

---

*Last updated: 2026-04-20 — v1.1 opened, Phase 04 added*
