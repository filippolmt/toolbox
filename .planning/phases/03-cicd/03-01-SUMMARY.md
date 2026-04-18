---
phase: 03-cicd
plan: 01
subsystem: ci-cd
tags: [github-actions, docker, ghcr, multi-arch, ci-cd]
dependency_graph:
  requires: [docker/Dockerfile, docker/smoke-test.sh]
  provides: [.github/workflows/docker-publish.yml]
  affects: [ghcr.io/filippolmt/tools]
tech_stack:
  added: [github-actions, docker/login-action@v3, docker/setup-qemu-action@v3, docker/setup-buildx-action@v3, docker/metadata-action@v5, docker/build-push-action@v6]
  patterns: [test-before-push, gha-cache, multi-arch-qemu]
key_files:
  created:
    - .github/workflows/docker-publish.yml
  modified: []
decisions:
  - "Test-before-push pattern: build+load locale per smoke test, poi push multi-arch — evita pubblicare immagini rotte su GHCR"
  - "cache-to solo sul primo build step — evita sovrascrittura scope cache tra i due build-push-action"
  - "QEMU setup prima di Buildx — arm64 emulation deve essere disponibile quando il builder viene creato"
metrics:
  duration: 76s
  completed: 2026-04-18
  tasks_completed: 1
  tasks_total: 2
  task2_status: checkpoint-human-verify
---

# Phase 03 Plan 01: Workflow GitHub Actions - Docker Build, Test, Push su GHCR

Workflow CI/CD completo con pattern test-before-push: build locale per smoke test validation, poi push multi-arch (amd64+arm64) su GHCR con tag latest e sha-<short>.

## Task Summary

| Task | Name | Type | Status | Commit | Files |
|------|------|------|--------|--------|-------|
| 1 | Create GitHub Actions workflow file | auto | Done | ec8b476 | .github/workflows/docker-publish.yml |
| 2 | Verify workflow by pushing to main | checkpoint:human-verify | Pending | -- | -- |

## What Was Built

Un singolo workflow file `.github/workflows/docker-publish.yml` che implementa:

1. **Trigger**: push su `main` + `workflow_dispatch` manuale (D-01)
2. **Login GHCR**: `docker/login-action@v3` con `GITHUB_TOKEN` (D-10)
3. **QEMU + Buildx**: setup per build multi-arch arm64 su runner amd64 (D-05, D-11)
4. **Metadata**: `docker/metadata-action@v5` genera tag `latest` + `sha-<short>` e OCI labels (D-03, D-12)
5. **Build + Load**: primo `build-push-action@v6` con `load: true` per test locale — nessun `platforms` (Pitfall 2), `cache-to: type=gha,mode=max` solo qui (Pitfall 4) (D-13)
6. **Smoke Test**: invoca `docker/smoke-test.sh` sull'immagine caricata localmente — se fallisce, il push non avviene (D-14)
7. **Build + Push Multi-arch**: secondo `build-push-action@v6` con `push: true`, `platforms: linux/amd64,linux/arm64`, riusa cache dal primo build (D-05, D-13)

**Tutte le 14 decisioni (D-01 - D-14) implementate. Tutti i 4 pitfall mitigati.**

## Decisions Made

1. **Test-before-push risolve ambiguita D-14**: lo smoke test gira PRIMA del push su GHCR, non dopo. Evita che immagini rotte vengano pubblicate con tag `latest`.
2. **cache-to solo sul primo build**: il secondo build usa solo `cache-from` per evitare sovrascrittura dello scope cache (Pitfall 4 da RESEARCH.md).
3. **Nessun path filter**: ogni push su main triggera la build (D-02) — semplice e coerente per progetto personale.

## Deviations from Plan

None - plan executed exactly as written.

## Checkpoint: Task 2 - Human Verification Required

Task 2 e' un checkpoint `human-verify`. Per completarlo:

1. Push del commit su `main` (il commit ec8b476 triggera il workflow)
2. Verificare su https://github.com/filippolmt/tools/actions che "Docker Publish" si avvia
3. Attendere completamento (10-20 min per build arm64 via QEMU)
4. Verificare tutti gli step: Checkout, Login, QEMU, Buildx, Metadata, Build+Load, Smoke Test, Build+Push
5. Verificare su https://github.com/filippolmt/tools/pkgs/container/tools i tag `latest` e `sha-<short>`

## Known Stubs

None - no stubs in the workflow file.

## Self-Check: PASSED

- FOUND: .github/workflows/docker-publish.yml
- FOUND: .planning/phases/03-cicd/03-01-SUMMARY.md
- FOUND: commit ec8b476
