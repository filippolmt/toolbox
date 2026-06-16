# Update notifications

How a running `toolbox shell` tells you a newer runtime image or host CLI is
available, where it caches that result, and how to turn it off. This is an
advisory banner only — toolbox never auto-updates or rebuilds anything.

## What you see

When a newer version is available, the prompt prints a one-shot yellow line
the next time it redraws:

- **Newer runtime image** (GHCR `:latest` moved past the image your container
  was created from): *exit the shell and run `toolbox stop`, then reopen it*
  — or `toolbox build` to rebuild the image locally.
- **Newer CLI** (a GitHub release tag newer than the host CLI you launched
  with): *run `brew upgrade` on the host.*

The banner shows once per distinct result. It will not reappear on every
prompt; it only shows again when the detected result changes.

## How it works

Two checks run, both independent — either can fire without the other:

1. **Identity injection (host).** At container creation the host CLI injects
   `TOOLBOX_IMAGE_DIGEST` (the running image's resolved repo digest) and
   `TOOLBOX_CLI_VERSION` (the host CLI build version) into the container
   environment. A locally built or untagged image has no repo digest, so
   `TOOLBOX_IMAGE_DIGEST` is omitted and the image check is skipped (no false
   "update available"). A `dev` CLI build has no release to compare against and
   skips the CLI check.
2. **Background poller (`toolbox-update-check`).** A baked helper queries GHCR
   for the canonical `ghcr.io/filippolmt/toolbox:latest` digest (anonymous
   bearer token → manifest `HEAD` → `Docker-Content-Digest`) and the GitHub
   `releases/latest` `tag_name`, compares them to the injected identity, and
   writes the result to a cache file. GHCR is queried for the canonical repo
   even behind a `registry_mirror`: a mirror serves identical content, and
   GHCR `:latest` stays the authoritative "is there something newer" signal.
3. **Prompt banner (zsh `precmd`).** The prompt reads *only* the cache file
   (fast, local) and renders the banner. When the cache is stale it fires the
   poller in a disowned background job, so the prompt never blocks on the
   network. If both checks fail to reach their registry (offline), the poller
   exits without touching a previously valid cache.

## Cache and TTL

State lives under `$HOME/.toolbox-state/` (the same disposable state location
as shell history):

| File | Role |
|------|------|
| `update-check` | Latest comparison result the prompt renders. |
| `update-check.stamp` | Records each poll attempt (success *or* failure) so the TTL throttles retries even while offline. |
| `update-check.shown` | The last result the banner displayed, so a stable result doesn't re-nag. |

The poller only contacts the network when the stamp is older than the TTL —
**6 hours by default**. Override with `TOOLBOX_UPDATE_CHECK_TTL` (seconds).
Delete the cache files to force a check on the next prompt.

## Opt out

Set `TOOLBOX_NO_UPDATE_CHECK` to a truthy value (`1`, `true`, `yes`, `on`) to
disable the feature entirely: no banner, and the poller never runs (no network,
no cache writes). Add it to your `env:` passthrough (see
[`env:` passthrough](configuration.md#env-passthrough)) to make it permanent
across shells.
