---
gsd_state_version: 1.0
milestone: v1.3
milestone_name: Architecture Deepening
status: "shipped — milestone archived"
last_updated: "2026-05-07T22:30:00Z"
last_activity: "2026-05-07 -- v1.3 milestone shipped and archived"
progress:
  total_phases: 6
  completed_phases: 6
  total_plans: 28
  completed_plans: 28
  percent: 100
---

# State: Toolbox

## Current Position

Milestone: v1.3 — SHIPPED 2026-05-07
Status: Milestone archived. PR #160 merged (2026-05-07T21:40:26Z, all checks green). Tag `v1.3` to be pushed.
Last activity: 2026-05-07 -- v1.3 milestone shipped and archived

Next: `/gsd-new-milestone` to start next cycle.

## Project Reference

See: .planning/REQUIREMENTS.md (updated 2026-04-20 for v1.1)

**Core value:** Un singolo comando (`toolbox shell`) ti mette dentro un ambiente completo, isolato e riproducibile.
**Current focus:** Phase 10 — init-sequence

## Performance Metrics

| Metric | Value |
|--------|-------|
| v1.0 Phases complete | 3/3 |
| v1.0 Requirements complete | 20/20 |
| v1.1 Phases complete | 1/1 |
| v1.1 Plans complete | 4/4 |
| v1.1 UAT | 13/13 pass |
| 02-01 duration | 4m 23s |
| 03-01 duration | 76s |
| 04 plan duration | ~18h (iterative, spanning human-verify cycle) |
| Phase 09 P02 | 10m 49s | 2 tasks | 7 files |

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

### Phase 04 context (zsh-shell-bundle) — VERIFIED

- Origine: issue [#37](https://github.com/filippolmt/toolbox/issues/37), con deviazione deliberata: **Oh-My-Zsh in scope**, **zsh come default** (non opt-in)
- Requisiti completati: ZSH-01..05, SHELL-01..04 (9/9)
- Plan 01 (Go config): `Shell` field + `SupportedShells` + `ValidateShell` + `zsh` in `KnownTools`
- Plan 02 (Go container): `ResolveShellCmd` + `ShellMismatchError`, `/bin/bash` hardcodes rimossi
- Plan 03 (Dockerfile): zsh + OMZ + plugin custom + fzf + zoxide + `/etc/zsh/zshrc` + Renovate customManagers
- Plan 04 (smoke-test + human-verify): `check_zsh` block 14 assertions; bundle shipato con 17 plugin (fzf + direnv OMZ plugin dropped — rumorosi); CMD zsh; `make shell-bash` aggiunto; human-verify approved 2026-04-21
- Breaking change v1.1: default shell ora zsh (user deve settare `shell: bash` esplicitamente per il vecchio comportamento)

### Phase 05 context (go-toolchain-lsp) — PLANNED

- Origine: Claude Code LSP fallisce con `Executable not found in $PATH: "gopls"` dentro `toolbox shell`.
- Requisiti: GO-01..GO-07 (7 req, locked via SPEC)
- Plan 01 (Dockerfile): Layer 14a `go` + `gopls` + `goimports`, tarball SHA256-verified, `-ldflags="-s -w"`, single RUN gated da `INSTALL_GO=true`, ENV PATH aggiornato
- Plan 02 (Go CLI): `"go"` in `KnownTools` + `ToolBuildArg` → `"INSTALL_GO"`, mount `~/.toolbox/go → /home/toolbox/go` (incondizionato, pattern playwright-cache / npm-global), test extends `config_test.go` esistente
- Plan 03 (CI plumbing): 3 Renovate customManagers (`golang-version` per GO, `go` datasource per gopls/goimports), 3 `check_optional` in smoke-test (goimports usa `sh -c 'echo "" | goimports'` per bypassare exit 2 di `-h`)
- Plan 04 (docs): traceability REQUIREMENTS/ROADMAP/STATE v1.2
- Plan 05 (UAT): build + `make test` verde + human-verify LSP operativo in file `.go` reale
- Default on (`tools.go: true`): image size +~150 MB (Go ~100 + gopls/goimports ~50 stripped). Opt-out invoca auto-build `toolbox:local-<hash>`
- Coesistenza: host `Makefile` continua a usare `golang:1.26` container per build CLI; il Go bundled nel container serve i progetti dell'utente dentro `toolbox shell` (isolamento intenzionale host-Go vs container-Go)

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

- [Phase ?]: Cycle-breaker shellcmd: extract shell-resolution helpers so sessionplan composes them without depending on container
- [Phase ?]: Test reshape at Seam tier: shell-mismatch + bad-port + publish-mismatch tests moved/collapsed since failures now surface at sessionplan.Plan

### Roadmap Evolution

- Phase 5 added: go-toolchain-lsp — install Go + gopls in Dockerfile so LSP works inside toolbox container (fixes `Executable not found in $PATH: "gopls"`). Guarded by `ARG INSTALL_GO=true`, `tools.go` in `.toolbox.yaml`, Renovate customManagers for `GO_VERSION` + `GOPLS_VERSION`, smoke-test assertions for `go version` + `gopls version`.

## Blockers

(None)

## Todos

(None)

## Deferred Items

Items acknowledged and deferred at milestone close on 2026-04-21:

| Category | Item | Status |
|----------|------|--------|
| uat_gap | Phase 03: 03-HUMAN-UAT.md | partial (3 pending scenarios) |
| verification_gap | Phase 03: 03-VERIFICATION.md | human_needed |

Both items belong to v1.0 milestone (already shipped). Carry forward as legacy tech debt.

---

*Last updated: 2026-04-21 — Phase 04 verified (13/13 UAT), milestone v1.1 ready for ship*

**Planned Phase:** 05 (go-toolchain-lsp) — 5 plans — 2026-04-21T08:35:43.792Z
