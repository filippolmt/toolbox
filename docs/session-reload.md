# Session reload

A newer runtime image is fetched in the background while you work. When *you*
decide, one command moves your session onto it — same working directory,
conversation resumed — without exiting and reopening by hand.

Two physical constraints shape all of it. Docker cannot swap the image of a
running container, and the command asking for the swap runs inside the very
container being replaced. So the reload is owned by the host-side `toolbox
shell` process — it tears down, recreates and re-attaches — while the
in-container command only signals it. **Process continuity is not preserved;
conversation continuity is.**

## What you see

When a newer version is available, the prompt prints a one-shot yellow line
the next time it redraws:

- **Newer runtime image downloaded** (the local image store has moved past the
  image your container was created from — the bytes are already here): *run
  `toolbox-reload` to move this session onto it (recreates the container).*
- **Newer CLI** (a GitHub release tag newer than the host CLI you launched
  with): *run `brew upgrade` on the host.*
- **Both**, on one line: *`brew upgrade` on the host, then `toolbox-reload`
  here.* One reload covers both axes, because it re-execs whatever `toolbox`
  is on disk before it recreates anything — so upgrading first is what makes
  the second command pick the new CLI up.
- **Newer runtime image that could not be downloaded** (the registry has moved
  and the pull keeps failing — an expired registry credential looks exactly
  like this): *check registry access.* It fires only after the failure has
  outlasted a full probe cadence, so a dropped Wi-Fi connection never accuses
  the registry.

The banner shows once per distinct result. It will not reappear on every
prompt; it only shows again when the detected result changes. A download you
can already use outranks one that failed.

The line names the cost — *recreates the container* — because the reload asks
for no confirmation, so this is the only place the price is stated before the
act. It deliberately promises nothing about your agent; see
[what survives](#what-survives-and-what-dies) for why that promise would be
conditional.

If your host CLI predates the reload, the banner falls back to the old
*exit the shell and run `toolbox stop`* wording instead of advising a command
that would refuse. See [version skew](#version-skew).

## toolbox-reload

Type it at the zsh prompt:

```
toolbox-reload
```

It **always** reloads, including when nothing newer exists. That is deliberate:
it also re-execs the host CLI and recreates the container from scratch, which
makes it the natural way out of a dirty session. A command that refused when
there was nothing to adopt would be unreliable exactly when you reach for it to
put things back in order.

It is a zsh **function**, not a program: a child process cannot make its parent
shell exit, and the exit is the whole mechanism. Two consequences:

- It works from the prompt, not from inside an agent's shell-out. Asking for a
  reload from inside Claude Code or Codex would be asking the agent to kill
  itself — and the reload restarts it anyway.
- It works whether or not the [bridge](bridge.md) is installed, and it survives
  `pull: never`, which is what keeps `never` + `toolbox build` + reload alive.

There is no confirmation. What you get instead is a short summary once the swap
completes: image digest before/after, CLI version before/after, with an
explicit `unchanged` when they match — the only evidence distinguishing a
successful-but-pointless reload from one that failed silently.

### What happens, in order

```
toolbox-reload → marker written, shell exits
                 → host re-execs the toolbox on disk   (a `brew upgrade` takes effect here)
                 → refresh + verify the image
                 → destroy the old container
                 → create, attach
```

The order is the safety argument. The riskiest step is the first, when your old
session is still alive, and the image is proven present **before** anything is
destroyed: **a reload that finds no usable image is not a failed reload, it is
a no-op that leaves you where you are.** If something fails *after* the
teardown — a port conflict, a `~/.toolbox/Dockerfile` overlay that will not
build — the error names the exact command that gets you back, because the
shell that would normally tell you has already exited.

### What survives, and what dies

**Survives**, because it already lives on a bind mount rather than in the
container:

| What | Where |
|---|---|
| zsh history | `~/.toolbox-state/zsh_history` |
| atuin history and its sync key | the `atuin` mount |
| Agent conversation (Claude / Codex) | the `claude` / `codex` mounts |
| Your shell customisation | `~/.toolbox-state/zshrc.d/*.zsh` |
| Every credential and CLI auth directory | the auth mounts (`gh`, `glab`, `gcloud`, `docker`, …) |
| Workspace files | the workspace bind |

**Carried deliberately:** your working directory, when it is still under the
workspace. Anything else — `cd /home/toolbox`, a path inside a mount the new
session may not have, a directory since deleted — falls back silently to the
top of the workspace.

**Re-derived, not carried:** the whole `TOOLBOX_*` identity. The new session
computes it from the same workspace and the same config, so it is right for the
*new* image instead of replaying the old one's `PATH` and CA variables into it.
Snapshotting the old environment and restoring it is not merely expensive, it is
the half-updated state this whole mechanism exists to avoid.

**Dies:** running processes, background jobs, servers and watchers you started
by hand, and environment you exported into the live shell. For environment that
*should* persist, `~/.toolbox-state/zshrc.d/` already exists.

**Also dies: another terminal attached to the same container.** An ordinary
exit spares a container while a sibling pane is attached; a reload cannot. The
container name is fixed per workspace, so a spared old container would block
the new one from being created, and half your panes on the old image and half
on the new is worse than the loss. The rule is one sentence: *another attached
terminal or a process left behind, the reload lists both and kills both.*

The list is printed, not prompted on. A dev server or a watcher is the normal
state of a working shell, so a confirmation would fire on nearly every reload.
It exists for the one thing you have forgotten — a `Ctrl+Z`-suspended agent, a
detached job — invisible at the prompt where you typed the command.

### Your agent

A `toolbox worktree` session auto-launches an agent, so a reload relaunches it
and **resumes** the most recent conversation (`claude --continue`,
`codex resume --last`). A plain `toolbox shell` reloads into a plain shell: the
reload reproduces how the session was started, it does not guess what you were
doing.

"How the session was started" includes the flags you typed. A session opened
with `--profile work -p 7171 --peer=false` reloads with all three: `--profile`
and `--peer` decide the container's name, `--profile` also decides which
credential root is mounted, and `-p` bindings are fixed at creation — so
dropping any of them would land you in a *different* container from the one
the reload just destroyed. Two things are deliberately not replayed: `--create`
and `--path`, whose work is already done (the named shell is in your config by
then), and `worktree create`, which comes back as `worktree open <branch>` so a
branch that now exists is not re-created and a prompt your agent has already
completed is not re-sent. The agent is pinned as *resolved*, so the session
comes back on the agent it actually ran even if you change the default
meanwhile.

The resume is conditional on your working directory having been carried:
`claude --continue` is keyed on the directory, and the workspace is mounted
twice. When the directory falls back, the agent launches bare — resuming the
wrong conversation in silence is worse than not resuming.

### Peer sessions and the anchor

A reload replaces only the session container that asked for it. The
[peer anchor](../CONTEXT.md#peer-anchor) is shared and stays on whatever image
it was created from — the reload does nothing about that, deliberately, since
forcing the issue would either break sibling sessions or make you wait for
other terminals to close. The anchor is functionally inert, so the mixed-image
window is a documentation matter and not a correctness one.

The reload is, however, the window in which a *stale* anchor becomes
replaceable: it stops holding the anchor before the next session asks for one,
so a single-session developer gets for free the replacement the existing
warning otherwise asks them to trigger by closing every shell. That warning now
fires after the old container is gone, so it cannot be acted on beforehand.

### Version skew

The image is pushed when a change merges; the CLI is released on a tag and
reaches you through `brew upgrade`, which is your act. So the two can disagree,
in two directions that are not symmetric:

- **Newer image, older CLI.** `toolbox-reload` exists but the CLI cannot hear
  it. Left unguarded this is the worst failure here — the marker is written,
  the old CLI does not recognise it, and the session is torn down for nothing.
  So the command **refuses at the prompt without exiting**, naming
  `brew upgrade toolbox` and leaving your session intact. The banner falls back
  to the pre-reload wording for the same reason.
- **Newer CLI, older image.** `toolbox-reload` does not exist yet:
  `command not found`, loud and lossless, and it heals in one exit-and-reopen.
  There is a chicken-and-egg worth stating plainly — to *get* the command you
  must exit the old way once — but it happens once per machine.

## How the prefetch works

One detector, host-side, running for as long as the shell is attached
(`internal/imageprefetch`). Three steps, all silent — the host process's
stdout *is* your terminal:

1. **Probe.** `DistributionInspect` asks the Docker daemon for the digest the
   registry currently serves for your resolved image ref. It needs no
   credentials against the public GHCR package, and it goes through the daemon
   — so a [`registry_mirror`](configuration.md#image-selection) is what gets
   asked, and is therefore authoritative for how fast a new image is noticed.
2. **Prefetch.** When that digest differs from the one the local store holds,
   the image is pulled in the background. By the time the banner appears the
   bytes are already on disk; the download does not wait for you and does not
   block the prompt.
3. **Publish.** The comparison the banner renders is *the local store against
   the digest your container was created from* — read back off the running
   container (`TOOLBOX_IMAGE_DIGEST`, injected at creation), so it stays right
   when you attach to a container another terminal created. The result is
   written to a cache file; the in-container zsh `precmd` hook reads only that
   file and renders the line.

The CLI axis rides along: the host compares its own build version against the
GitHub `releases/latest` tag. Nothing in the container polls anything.

**The prefetch stands down on a locally built image.** `toolbox build` tags the
canonical ref, and an image that was built rather than pulled has no repo
digest. That absence is the signal: while it holds, the probe and the pull are
skipped entirely, so an automatic download never overwrites a build you asked
for. An explicit `docker pull ghcr.io/filippolmt/toolbox:latest` restores the
digest and the prefetch resumes.

## Cache and TTL

State lives under `$HOME/.toolbox-state/` in the container — the same
directory the host knows as `~/.toolbox/toolbox/state/` (mounts_root- and
profile-aware), which is how the two ends meet:

| File | Role |
|------|------|
| `update-check` | Latest comparison result the prompt renders. Written host-side, atomically. |
| `update-check.stamp` | Records each poll attempt (success *or* failure) so the cadence throttles retries even while offline. |
| `update-check.unavailable-since` | When the download *first* started failing. Removed as soon as the bytes land. |
| `update-check.shown` | The last result the banner displayed, so a stable result doesn't re-nag. |

The result file is `key=value`, one per line:

| Field | Meaning |
|---|---|
| `image_update` | `1` when this session is behind the local store. |
| `image_latest` | The digest the registry currently serves. |
| `image_state` | `none` / `ready` / `unavailable` — added *alongside* `image_update`, never replacing it, so an older image still renders its own (still true) sentence. |
| `cli_update`, `cli_latest` | The CLI axis. |

The poller's ticker is only an alarm: every tick re-reads the stamp, and the
registry is contacted only when it is older than **30 minutes**. Because the
stamp lives on the shared state mount, that is one probe per half hour *for
you* rather than one per open shell — best effort, since two sessions whose
ticks coincide can both find the stamp stale and both probe; the daemon
deduplicates the pull that follows, so the cost is a second manifest lookup.
The first poll runs as soon as a shell attaches, subject to the same gate.
Delete the cache files to force a check on the next tick.

If the registry cannot be reached, the previous (still valid) result is left
alone rather than blanked, and the stamp still advances — an offline machine
costs one failed probe per cadence, not one per tick.

## Opt out

Set `TOOLBOX_NO_UPDATE_CHECK` to any non-empty value to silence the banner. Put
it in your `env:` passthrough (see
[`env:` passthrough](configuration.md#env-passthrough)) to make it permanent
across shells — set there it also stops the host-side probe, so there is no
network traffic and no cache write either. Exported inside a live shell it
stops the rendering only; the network side is governed by
[`pull`](configuration.md#image-selection) from that point on.

`pull: never` turns the whole act off: no probe, no prefetch, no banner. It is
a statement about the network, and a probe talks to the network. For a single
run without touching your config: `TOOLBOX_PULL=never toolbox shell`.

`bridge: false` is **irrelevant** here, before and after: the bridge is
host-application access (browser, editor, proximo), not network egress, and
detection has never gone through it.

## What the prefetch changes about metered connections

There is no metered-network detection and no size threshold — deliberately.
The transfer itself is not new: under `pull: auto` those bytes arrive at shell
start too, and there you are [asked
first](configuration.md#the-start-up-refresh-prompt). What moves is **who is
present when they move** — from a developer who just opened a shell to one
already sitting in it. The
download is also strictly rarer than the ticker, because a pull only follows a
probe that found a new digest, so its rate is the repo's merge rate. If that
is not acceptable on a given connection, `pull: never` is the answer, and
`TOOLBOX_PULL=never` is its per-session form.
