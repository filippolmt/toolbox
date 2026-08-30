# Orphaned Sibling cleanup: a `container` command group with a positive criterion

Status: accepted (implementation pending)

A toolbox shell has the host's Docker socket bind-mounted, so a `docker
compose up` in a project's test suite creates its containers on the *host*
daemon, as siblings of the shell rather than children of it. Nothing binds
their lifetime to the session: the shell exits, they stay. Plain Compose ships
no reaper (testcontainers' Ryuk would have covered this; Compose has no
equivalent), so the containers, plus one `<project>_default` network per run,
accumulate across test runs until someone notices — and the first symptom of
the network side is usually a `compose up` failing with *"all predefined
address pools have been fully subnetted"*, with no visible cause.

We add a `toolbox container` command group that acts on these
[Orphaned Siblings](../../CONTEXT.md) — and deliberately **only** on them:

- **Selection is positive, never by exclusion.** A target is recognised by the
  Compose project labels its creator wrote (`com.docker.compose.project`, with
  `com.docker.compose.project.working_dir` as the provenance and
  disambiguator), or, for a container carrying no Compose label, by being that
  single container. A container toolbox created is never a target: the group
  refuses one by name with an error pointing at `toolbox stop`, and never
  offers one in completion. The proximo stack is a Compose project like any
  other and is told apart by its own `proximo.role` label (`proximo.RoleLabel`,
  sibling of the `proximo.hosts` label that marks *routed* containers — the two
  mark disjoint populations): it is offered by name and excluded from every
  bulk form.
- **Three verbs, matching `toolbox worktree`.** `container stop <target…>`
  (plus `--all`) stops; `container rm <target…>` removes the targets named;
  `container prune` takes no arguments and removes every Orphaned Sibling.
  This mirrors `worktree rm <branch>` / `worktree prune`, where `prune` means
  "everything matching the criterion" and never takes a target.
- **`rm`/`prune` remove what is free to rebuild; nothing else.** Containers
  and the project's networks are removed (a network holds no data and Compose
  recreates it identically). Volumes are not, ever, unless `--volumes` is
  passed — the same escalation shape as `worktree prune --delete-remote`. Only
  volumes carrying the project label are eligible, so an `external: true`
  volume self-excludes.
- **`rm` stops before removing**, with the stop grace rather than Docker's
  `force` (which is an immediate SIGKILL, and a test stack usually has a
  database in it). `worktree rm` already carries this contract.
- **Discovery is the shell completion**, the repo's first dynamic one: a
  `ValidArgsFunction` listing live projects and standalone containers at TAB
  time, with typed values (`project:api`, `container:api`, and
  `project:api@<working_dir>` when two projects share a basename) so ambiguity
  is resolved before the parser sees it. A bare name is accepted when unique
  and errors with the alternatives when not. A completion function must return
  no suggestions — never an error — when the daemon is unreachable.

## Considered Options

**Stop everything except toolbox and proximo.** The original framing, and the
reason this ADR exists. Its blast radius is defined by the exceptions someone
remembered to write, so the first stack that is neither toolbox nor proximo
nor a test leftover dies silently. Rejected in favour of positive recognition:
when a destructive command can err in two directions, it should err towards
false negatives — a container left running is the state we are already in.

**`prune <target>` accepting both targets and `--all`.** Shorter to use, and
what was first proposed. Rejected because `prune` already means
"criterion-driven, no arguments" in `toolbox worktree`, and one word meaning
two things across sibling command groups is exactly the drift the glossary
exists to prevent.

**Sweep automatically when the shell exits**, in `teardown.OnShellExit`. It
would remove the need to remember anything, which is the actual complaint. But
it acts outside the toolbox-owned set without being asked, and two shells open
in parallel would have the first one's exit take the second one's test stack
down — the Compose labels record a working directory, not the session that
created it, so there is nothing to key the ownership check on
(`teardown.HasActiveExecs` answers a different question). Rejected; revisit
only if a per-session marker becomes available.

**A `toolbox container list`.** Rejected as a third inventory: `toolbox list`
already enumerates toolbox shells, TAB enumerates the targets, and
`container prune --dry-run` prints exactly the set the criterion selects.

## Consequences

- One criterion, three consumers: what completion offers, what `stop --all`
  stops, and what `prune` removes are the same set, and a test should pin that.
  The single deliberate asymmetry is proximo — offered by name (if you type it, you
  meant it) but never in the bulk set, and `--all` / `prune` announce the skip
  rather than staying silent about it.
- `--dry-run` is opt-in, as on `worktree prune`, and there is no interactive
  confirmation: a prompt on a cleanup command teaches people to press enter
  without reading. `--all` and `prune` print what they act on as they go.
- The removal path does **not** reuse `teardown.StopOne`. `Teardown` is the
  policy for a container toolbox owns — stop plus remove, lossless because the
  state lives on host bind mounts. This is a different population with a
  different policy, and sharing the function because both end in "stop and
  remove" would collapse the distinction the moment someone refactors.
- `teardown.DefaultStopGrace` (2s) is tuned for a toolbox shell and is likely
  too short for a database; the new path needs its own grace value.
- Linux users installing via `go install` must install completions by hand.
  The Homebrew cask already generates them at install time
  (`generate_completions_from_executable`, `.goreleaser.yaml`).
