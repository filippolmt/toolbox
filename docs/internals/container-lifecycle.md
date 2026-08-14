# Container lifecycle internals

Maintainer notes on how the host CLI creates, secures, and tears down the container. User-facing image knobs (`image`, `registry_mirror`, `pull`) are documented in [configuration](../configuration.md#image-selection); port publishing and the loopback bridge under [`toolbox shell`](../commands.md#toolbox-shell).

## Image selection mechanics

`internal/build.ResolveImage(image, registryMirror)` owns the ref precedence (full `image` override > `registry_mirror` host swap > canonical default); `imageplan.Ensure` requires the resolved image locally and `imageplan.Refresh` implements the `pull` policy (`never` → no registry round-trip, `always` → `imagepull.ForcePull`, `auto` → `imagepull.RefreshIfStale`, 1 h TTL cache). The user-visible semantics — canonical default, `toolbox build` escape hatch, the three config keys, and the caveat that a full `image` override doesn't see local `toolbox build` output while a `registry_mirror` does — live in [configuration](../configuration.md#image-selection).

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

A container whose `ContainerStart` *failed* is no exception, even though `container.Shell` removes nothing on that path (it returns the wrapped error before the teardown defer is registered). The daemon force-removes an AutoRemove container when its start fails, so the record does not linger in state `created` — verified against both a failing entrypoint and a port conflict, the two shapes toolbox actually hits. `ActionStart` therefore survives only for containers created before AutoRemove was adopted (and for a daemon-side auto-remove that itself fails). Don't "fix" the failed-start path into leaving a container behind for reuse: the daemon overrules it.

Rejected alternatives: a detached client-side `docker rm -f` (orphan process, no error feedback, races a fast re-`shell`); a single synchronous `ContainerRemove(Force)` (still blocks the client on the unmount). AutoRemove lets the daemon serialise the teardown correctly.
