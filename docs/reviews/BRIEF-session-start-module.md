# Brief — candidate 08: one module for "start a session"

Source: [`architecture-review-2026-09-04.html`](architecture-review-2026-09-04.html), candidate 08
(`#c8`). **Read the candidate itself, not only this brief.**

## Repo state when this brief was written

`docs/reviews/` is **untracked** — three new files, nothing staged. The review
was moved here out of `dist/`, which is gitignored and overwritten by
GoReleaser, so it had never been committed at all. `git add docs/reviews/` as
the first commit of this work; do not discard it.

## The problem, verified symbol by symbol

Two composition roots for "open a session": `cmd/shell.go:runShell` and
`cmd/worktree.go:openSession`. **Find them by symbol, not by line number** —
the ones printed in the review are already stale.

Duplicated in both:

- `takeReloadHandover`
- `build.LocalRepoDigest` + `build.ResolveImage`

Present in `runShell` only:

| Behaviour | `openSession` |
|---|---|
| `MigrateLegacyToolboxState` | absent |
| `printBridgeTipIfNeeded` | absent |
| `--profile` / `--share` (`mountplan.Profile`, `NewProfile`) | absent |
| `resolvePeer` | absent — passes the raw config instead |

## The trap — decide this before writing code

Unifying the assembly is **not a pure refactor**. It grants worktree sessions
behaviours they do not have today, so each one needs a decision: bug, or
deliberate absence?

`--profile` and `--share` are the sharp case: worktree does not expose those
flags at all, so "make the divergence impossible" may mean *adding flags*
rather than sharing code. Do not assume all of them should be extended — ask.

Open question nobody has checked yet: whether `--profile` even makes sense for
a worktree session under the mount model.

## The seam, for `/tdd`

What blocks testability is that `runShell` reads eight package-level flag vars
(`shellPublish`, `shellCreate`, `shellPath`, `shellBridgeLoopback`,
`shellOAuth`, `shellProfile`, `shellShare`, `shellPeer`), so no test can call
it without mutating globals.

The new entry point taking a typed intent **is** the seam: the flags stay in
cobra, and a test builds the intent directly. That is where this candidate
becomes verifiable.

## Two cautions

- **Do not copy coverage figures from the review.** It reports 0.0% for both
  functions and that is no longer true. Re-measure if you need the number, and
  remember that writing a current-state figure into the repo is forbidden —
  see [CLAUDE.md](../../CLAUDE.md).
- **The deletion test is inverted here**, and the review says so: there is no
  module to delete, the complexity is already spread across two call sites.
  The bar is not "deleting it is invisible" but "a third entry point would not
  add a third copy".

## Gate

`make go-check`. Nothing under `cmd/**` carries a CI gate beyond `ci.yml`.
