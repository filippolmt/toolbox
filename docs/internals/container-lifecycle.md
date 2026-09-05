# Container lifecycle internals

Maintainer notes on how the host CLI creates, secures, and tears down the container. User-facing image knobs (`image`, `registry_mirror`, `pull`) are documented in [configuration](../configuration.md#image-selection); port publishing and the loopback bridge under [`toolbox shell`](../commands.md#toolbox-shell).

## Image selection mechanics

`internal/build.ResolveImage(image, registryMirror)` owns the ref precedence (full `image` override > `registry_mirror` host swap > canonical default); `imageplan.Ensure` requires the resolved image locally, and `imageplan.Sync` implements the `pull` policy — one entry point, `never` → no registry round-trip, `always` → an unconditional pull, `auto` → probe, then ask ([the prompt](../configuration.md#the-start-up-refresh-prompt)). Which form it takes is the `imageplan.Reason` the caller passes: `ReasonCreate`/`ReasonStart` at shell start, and `ReasonReload` for a [session reload](../session-reload.md) running it before it destroys anything — the one reason that asks nothing and takes the TTL-cached refresh, since a reload adopts what the store holds and the prefetch is what advanced it. The user-visible semantics — canonical default, `toolbox build` escape hatch, the three config keys, and the caveat that a full `image` override doesn't see local `toolbox build` output while a `registry_mirror` does — live in [configuration](../configuration.md#image-selection).

## Codex nested sandbox

Codex is unconditionally installed, so `toolbox shell` creates the container with Docker `seccomp=unconfined` to allow Codex's built-in bubblewrap sandbox to create nested user namespaces. The flag lives in `sessionplan.NestedSandboxSecurityOpt`.

## Container teardown

The container is disposable: when the last attached shell exits it is destroyed (all persistent state lives on the `~/.toolbox/` bind mounts). The cost of *how* it is destroyed used to land on the user's prompt — `teardown.StopOne` ran a synchronous `ContainerStop` + `ContainerRemove`, and on macOS Docker Desktop the remove blocks on unmounting ~25 virtiofs binds inside the LinuxKit VM (~1–2s). The SIGTERM grace is not the cost (PID 1 is `sleep`, dies instantly) and neither is zsh (~90ms); the daemon-side unmount is.

Fix: containers are created with `HostConfig.AutoRemove: true` (`container.createAndStart`). The daemon's auto-remove worker performs the unmount + delete **after the container exits**, asynchronously from any client call. So the exit path only has to make the container *exit*:

- `teardown.OnShellExit` does a single `ContainerInspect` and branches on it:
  - a still-running sibling exec → leave the container running so the other terminal survives;
  - `HostConfig.AutoRemove` true → `ContainerKill` (SIGKILL — nothing to flush) and return immediately; the daemon reaps it off the prompt's critical path;
  - AutoRemove false (legacy container created before this change) → synchronous `StopOne` fallback, dead within one upgrade cycle since containers are recreated each shell.
- `teardown.StopOne` (the explicit `toolbox stop` / `--all` path) stays synchronous — a cleanup command should confirm removal — but now tolerates a `Conflict` ("removal already in progress") alongside `NotFound`, because on an AutoRemove container the stop may have already triggered the daemon's removal.

Consequence: a stopped container is auto-removed, so the `runplan.ActionStart` "reuse a stopped container" path effectively never fires for new containers — every `toolbox shell` recreates from the canonical image plus the mounted state. The latency moves off the blocking exit and onto a startup the user already expects to do work.

A container whose `ContainerStart` *failed* is no exception, even though `container.Shell` removes nothing on that path (it returns the wrapped error before the teardown defer is registered). The daemon force-removes an AutoRemove container when its start fails, so the record does not linger in state `created` — verified against both a failing entrypoint and a port conflict, the two shapes toolbox actually hits. `ActionStart` therefore survives only for containers created before AutoRemove was adopted, a daemon or host restart that outlived the reaping, a hand-typed `docker stop` (and a daemon-side auto-remove that itself fails). Don't "fix" the failed-start path into leaving a container behind for reuse: the daemon overrules it.

That rarity is load-bearing elsewhere: it is why the [start-up refresh prompt](../configuration.md#the-start-up-refresh-prompt) can afford to *discard* the container on that branch when the developer accepts a newer image ([ADR 0008](../adr/0008-refresh-prompt-on-a-stopped-container.md)) — the writable layer at risk belongs to a container that only an unusual event left behind, and every piece of persistent state lives on the `~/.toolbox/` mounts either way.

Rejected alternatives: a detached client-side `docker rm -f` (orphan process, no error feedback, races a fast re-`shell`); a single synchronous `ContainerRemove(Force)` (still blocks the client on the unmount). AutoRemove lets the daemon serialise the teardown correctly.

## Session reload teardown

A [session reload](../session-reload.md) is the one caller that must not use the policy documented directly above, and the reason is structural rather than a preference.

`teardown.OnShellExit` declines to destroy while a sibling exec is attached. That is right on an ordinary exit and wrong here: the container name is deterministic per workspace, so a spared old container blocks the `ContainerCreate` the reload performs next — and refusal would cost machinery the design already ruled out (a Docker call from inside the container to guard the marker write, or a re-attach loop in the host). A split-brain, half the panes on the old container and half on the new over the same workspace, is worse than the loss it would avoid. So `container.removeAndWait` is unconditional: **another attached terminal or a process left behind, the reload lists both and kills both.**

Two mechanics follow from that:

- **Force-remove, not kill-and-let-AutoRemove-reap.** The AutoRemove path returns as soon as the SIGKILL lands and leaves the delete to the daemon's worker, which races the new container's name. `removeAndWait` issues `ContainerRemove(Force)` and waits — subscribing `ContainerWait` with `WaitConditionRemoved` *before* the removal, because the daemon's own worker can finish in between and a wait started after that never fires. `NotFound` (already gone) and `Conflict` ("removal already in progress") are both success.
- **It runs in the new binary, after the verify.** `imageplan.Sync` (under `ReasonReload`) + `Ensure` gate it, so a reload that finds no usable image destroys nothing and the session stays exactly as it was. That ordering is also what makes the process list evidence rather than prediction: `container.reloadCasualties` enumerates immediately before the teardown and prints only once it succeeded.

One consequence belongs to the section below rather than this one: because the destroy precedes the create, the reloading session has stopped holding the [peer anchor](../../CONTEXT.md#peer-anchor) by the time `ensureAnchor` runs, so **the reload is the window in which a held stale anchor becomes replaceable** — the replacement documented under *Peer anchor reaping* is load-bearing for the reload, not merely tidy.

The enumeration is `ContainerTop`, which stays **cgroup-scoped even under a shared PID namespace** — `PidMode.IsContainer()` rewrites the OCI namespace path and leaves `cgroupsPath` per-container, so it lists this session's processes and no sibling's. An in-container `ps` could not: under peer messaging the anchor's namespace is the whole process table. Over it sits a static deny-list of the known baseline (`tini`, the idle main shell, `proximo-hosts --watch` and its `docker events` child, one `socat` per `-B` port). The list is informational, so the deny-list **does not need to be right, only honest**: a stale entry costs one noisy line and never a wrong decision. The session's own shell command is dropped exactly once, because the container's idle main process and a sibling attached pane run the identical command line and the pane is the loud loss worth showing.

## Image reclamation

`internal/imagereclaim` removes the [Superseded Images](../../CONTEXT.md#superseded-image) this CLI pulled: an image carrying a `RepoDigests` entry for the resolved toolbox repo and no `RepoTags` at all. `container.beginReclaim` starts it — behind the `reclaimImages` var, gated on the plan's resolved `image_reclaim`, cancelled with the session — and the placement in `Shell` is the design rather than an optimisation: **after `dispatchOp`, never before.** Only once this workspace's container exists and references the current image is every surviving reference to an older one somebody else's real reference; run earlier and the removal is guaranteed to be refused, because the session doing the reclaiming is itself the last holder.

Three things the predicate has to get right, and the reason each is not obvious:

- **The repo constraint is the perimeter.** An image this project never pulled carries no digest for this repo and is therefore never a candidate. The tempting shortcut — Docker's `dangling=true` filter — is wrong in both directions: it does not match these images (losing a tag leaves the repo digest behind, and an image is dangling only when it has neither), and it *does* match images belonging to every other project on the machine.
- **The running digest is excluded by name.** A config that pins `image:` to a digest instead of a tag produces a running image with no tags, so the predicate on its own would nominate the very image the shell just started from. `Input.KeepDigest` is the digest this session actually runs — read off the container on the connect path, off the re-stamped plan on create, the same `sessionDigest` the update prefetch is anchored to.
- **The ref tracked is the base, never the `:local` overlay tag.** The overlay is built rather than pulled, so it carries no repo digest at all; the base underneath it is what gains a generation per merge. `Input.Ref` is spelled like `imageprefetch.Input.Ref` deliberately — one value, one name, populated from the one `plan.Image.Ref`, which is the base on every line of `internal/container` ([Run Image](../../CONTEXT.md#run-image)) — and an empty one abstains before the store is even listed, since `build.RepoDigest` compares the bare registry path and the empty path matches a malformed `@sha256:…` entry.

The summary is the act's only output, and it goes through `ui.InfoAsyncf` rather than `ui.Infof`: by the time an unlink of several gigabytes finishes, the attached shell has put the tty in raw mode (`term.MakeRaw` clears `ONLCR`), where the bare LF the other `ui` writers end on drops a line without returning the carriage and staircases everything after it. Cancellation `break`s out of the loop instead of returning, so a session that exits mid-sweep still says what it freed — the developer's only sign that the space came back.

There is no in-use check. `ImageRemove` runs with neither `force` nor `PruneChildren`, and the daemon's refusal *is* the answer — atomically, on the far side of a race a `ContainerList` census would sit on the wrong side of. Two consequences follow and both will be reported as bugs: a stopped container pins its image indefinitely, and a `:local` overlay pins its base. Both flags would "fix" one of those by destroying something unreproducible, which is why neither is set. Full argument, and the real-daemon gate that pins the Docker behaviour it rests on: [ADR 0007](../adr/0007-daemon-refusal-as-in-use-check.md).

## Peer anchor reaping

The [Peer Anchor](../../CONTEXT.md#peer-anchor) holds the PID namespace every opted-in session joins, so **its** PID 1 is PID 1 for all of them — and reaping orphans is PID 1's job. `container.ensureAnchor` therefore overrides the image entrypoint's *payload* (a bare `sleep`, since none of the shell-start init belongs in a container that only holds a namespace) but not the init itself: the anchor runs `tini -g -- sleep infinity`.

Under a bare `sleep`, which never calls `wait()`, every process reparented after its parent exits stays a zombie for the anchor's lifetime — one PID slot each, accumulated across every shell that ever shared the anchor. Measured on a week-old anchor: 456 zombies, mostly `[atuin]`, `[sudo]`, `[herdr]` and `[zsh]`.

The session side cannot cover this. A container joining another's PID namespace is not PID 1 there, and the image's baked `ENTRYPOINT` carries no `-s`, so its tini never registers as a subreaper (`PR_SET_CHILD_SUBREAPER`) either.

### Replacing an anchor that predates it

An anchor created before this carries the old entrypoint, and the connect path used to reuse it forever. It now replaces it — but only when that breaks nothing, because Docker offers no help here: `docker rm -f` on an in-use anchor is **not** refused, it succeeds and leaves every session that held the namespace `exited` with 137 (measured, not inferred; the daemon tracks no dependency for `--pid container:<id>` the way it does for network mode).

`container.isCurrentAnchor` compares the running anchor's whole entrypoint against `container.anchorEntrypoint()` — the same slice `ContainerCreate` is handed, so the check and the spec cannot drift, and a later change to the anchor's PID 1 inherits the replacement for free. On a mismatch `container.replaceStaleAnchor` decides:

| Anchor state | Outcome |
|---|---|
| Stopped | Force-removed and recreated. Every session that held the namespace died with it, so nothing is left to break — and the holder scan is skipped, so a daemon that will not list containers cannot strand it. |
| Running, no holder | Force-removed and recreated. |
| Running, a session holds it | Left alone, with a warning. Exiting the other shells and starting one again is enough: the next start finds no holder and replaces it. |
| Removal itself fails | Warned about, and the stale anchor reused. |

Running-ness is read off the inspect record rather than taken as the `runplan.Action`: `runplan.Compute` derives that action from the same `State`, and the decision is about the container, not about which branch the caller arrived on.

That last row is why `replaceStaleAnchor` returns no error and `ensureAnchor` never fails on it. A stale anchor is a reaper-less PID 1 but a *working* namespace; failing the caller would send the session through `ensurePeerRuntime`'s degrade path to no peer messaging at all, which is worse than the bug being fixed. A self-heal that makes things worse when it cannot run is not one.

### Failing closed

`container.anchorHeld` is the guard between the replacement and a dead sibling shell, so it answers "held" for everything it cannot clear. It clears exactly three container states, each of which provably holds nothing:

| State | Cleared? |
|---|---|
| `created` | Yes — never started. |
| `exited`, `dead` | Yes — gone. |
| `running`, `paused`, `restarting` | No — a paused or restarting container keeps the namespace it joined. |
| `removing` | No — it cannot be shown to have let go yet. |

A `ContainerList` that errors, a `ContainerInspect` that errors, and an inspect that comes back with no `HostConfig` to read all answer "held" too. Guessing "free" from a daemon that would not answer costs a live session; guessing "held" costs one more shell start on the old anchor.

### One case the replacement cannot rescue

A **stopped** session container created against the old anchor still carries `PidMode: container:<old anchor id>`, fixed at `ContainerCreate`. Once the old anchor is gone that id resolves to nothing, so `warnReattachMismatch` warns and the `ContainerStart` behind it fails. In practice session containers are `AutoRemove: true` and a stopped one does not survive to be reattached (see [container teardown](#container-teardown)); a legacy container predating that can, and its recreate is the `toolbox stop <container>` the warning already prescribes. What changed is only who triggers it — this used to follow the user's own `docker rm -f`, and now follows toolbox's.

Pinned by `TestShellPeerReplacesUnusedStaleAnchor`, `TestShellPeerReplacesStoppedStaleAnchor`, `TestShellPeerKeepsHeldStaleAnchor`, `TestShellPeerKeepsStaleAnchorWhenHoldersUnknown`, `TestShellPeerKeepsStaleAnchorWhenHolderCannotBeRuledOut` and `TestShellPeerKeepsPeerMessagingWhenReplacementFails`; `TestShellPeerReusesRunningAnchor` holds the other direction — a current anchor is neither removed nor warned about.
