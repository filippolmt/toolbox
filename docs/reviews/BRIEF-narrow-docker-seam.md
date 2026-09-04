# Brief — Narrow the Docker seam (candidate 01)

Decided in a grilling session, 2026-09-04. Every recommendation accepted as a block.
Full report: [`architecture-review-2026-09-04.html`](architecture-review-2026-09-04.html), candidate 01.

## The problem

Every module under `internal/` declares `client.APIClient` (~90 methods) and calls 1–5 of them.
Eleven hand-rolled test adapters exist because there is no narrow interface a shared fake could
satisfy: `ImageInspect` is implemented 5 times, `ImagePull` 4, `ContainerInspect`/`Stop`/`Remove`
3 each. The only two interfaces declared outside tests in all of `internal/` are `bridge.Agent`
and `worktree.Git`.

The seam is real (a live client on one side, a fake on the other); it is declared at the wrong
width.

## Decisions

| # | Decision | Choice |
|---|---|---|
| Q1 | Where the interface lives | The consumer declares it, **unexported**, in its own package. No shared-interface hub package. |
| Q2 | Does `internal/container` narrow? | **No.** It stays on `client.APIClient`. It is the Docker edge; its union would be ~20 methods out of 90 and would need re-checking on every new call into a leaf. |
| Q3 | Scope | **The image family only**: `imageplan`, `imagepull`, `imageprefetch`, `imagereclaim`, `localimage`. `teardown` (5 methods), `worktree` (1) and `build` as a package are a second pass. |
| Q4 | Test adapters | **Narrow and rewrite the adapters of the in-scope leaves only**, onto a shared fake in `internal/dockertest`. Out of scope: untouched. |

## Dependency closure

Go constraint: an interface→interface assignment requires the target's method set to be a subset.
So every caller must declare at least the union of its own methods and those of the callees it
passes the value to.

Forced consequence: `build.LocalRepoDigest` and `build.BuildOverlay` **must narrow in this slice**,
because `imageprefetch` and `localimage` call them passing their own client. This is not scope
creep: without it the leaves are forced to keep the fat client.

Resulting sets (at most 3 methods):

| Module | Own methods | Via callee | Total |
|---|---|---|---|
| `imagepull` | `ImagePull` | — | 1 |
| `localimage` | `ImageInspect` | `build.BuildOverlay` → `ImageBuild` | 2 |
| `imagereclaim` | `ImageList`, `ImageRemove` | — | 2 |
| `imageprefetch` | `ImagePull`, `DistributionInspect` | `build.LocalRepoDigest` → `ImageInspect` | 3 |
| `imageplan` | `ImageInspect` | `imagepull` → `ImagePull`; `imageprefetch` → `ImagePull`, `DistributionInspect` | 3 |
| `build.LocalRepoDigest` | `ImageInspect` | — | 1 |
| `build.BuildOverlay` | `ImageBuild` | — | 1 |

`internal/container` keeps passing its `client.APIClient`: it satisfies every one of these
unexported interfaces, because its method set contains them all.

## Shape of the shared fake

It lives in `internal/dockertest` (already present, today holding only three leaf helpers).

- One struct with **one function field per method** the in-scope leaves use.
- A nil field **panics with a message naming the method**.
- The struct **must not** embed `client.APIClient` — otherwise the narrowing buys nothing in the
  tests, which is the whole point.

This preserves the invariant today's tests express as a panic on a nil embed — "this method was
not called" — and improves the error message. The fake's zero value panics on any call, exactly
as today.

Assertions not to lose (they are assertions of *absence*):

- `TestSweepNeverForcesAndNeverPrunesChildren` — `ImageRemove` takes neither `force` nor `PruneChildren`
- `TestPollAsksTheRegistryNothingWhileStampIsFresh` — the registry is asked nothing while the stamp is fresh

## Naming

Unexported interfaces, named for what they do, not `dockerClient`. E.g. in `imagereclaim`:
`type imageStore interface { ImageList(...); ImageRemove(...) }`.

An exported function may take an unexported interface type: the caller passes a value that
satisfies it and never needs to name the type.

To clean up on the way out: the naming drift already present in the mocks (`distDigst` vs
`distDigest`, `imgInspFn` vs `inspectFn`) dies with the adapters that carry it.

## Verification

- `make go-check` (test + lint) — mandatory, `internal/imagereclaim/**` is in scope
- `go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out` — the total floor the repo enforces is 80%
- The `internal/imagereclaim` gate (reclamation refusal) also runs locally with the socket and `IMAGE_TAG` in hand

Note: the suite is **red to begin with** inside a toolbox shell, for an unrelated reason —
`TestLaunchProximo_MissingBinary` (`internal/bridge/proximo_test.go`), because
`proximoFallbackCandidates()` hardcodes `/usr/local/bin/proximo`, which exists in the toolbox
image itself. That is candidate 03. Do not mistake it for a regression from this work.

## To do at the end

Add a glossary entry to `CONTEXT.md` for the concept this shape introduces — the per-module
declaration of the daemon methods that module calls — with its meaning, its owning package and
why it was named. The project's `CLAUDE.md` requires it whenever a design conversation names a
new concept.

Do not write version numbers or current-state figures in any repo text.
