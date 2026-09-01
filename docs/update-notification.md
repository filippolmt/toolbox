# Update notifications

How a running `toolbox shell` tells you a newer runtime image or host CLI is
available, where it caches that result, and how to turn it off. Detection and
download run host-side, on the `toolbox shell` process you already started;
the banner itself is advisory — nothing swaps the container underneath you.

## What you see

When a newer version is available, the prompt prints a one-shot yellow line
the next time it redraws:

- **Newer runtime image downloaded** (the local image store has moved past the
  image your container was created from — the bytes are already here): *exit
  the shell and run `toolbox stop`, then reopen it to run on it.*
- **Newer runtime image that could not be downloaded** (the registry has moved
  and the pull keeps failing — an expired registry credential looks exactly
  like this): *check registry access.* It fires only after the failure has
  outlasted a full probe cadence, so a dropped Wi-Fi connection never accuses
  the registry.
- **Newer CLI** (a GitHub release tag newer than the host CLI you launched
  with): *run `brew upgrade` on the host.*

The banner shows once per distinct result. It will not reappear on every
prompt; it only shows again when the detected result changes. The two image
lines are exclusive — a download you can already use outranks one that failed.

## How it works

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
The transfer itself is not new: under `pull: auto` those bytes already arrive
synchronously at shell start. What moves is **who is present when they move** —
from a developer who just opened a shell to one already sitting in it. The
download is also strictly rarer than the ticker, because a pull only follows a
probe that found a new digest, so its rate is the repo's merge rate. If that
is not acceptable on a given connection, `pull: never` is the answer, and
`TOOLBOX_PULL=never` is its per-session form.
