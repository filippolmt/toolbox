# Troubleshooting

Symptoms, causes, and fixes for the failure modes users actually hit. Bridge-specific issues live in [bridge troubleshooting](bridge.md#troubleshooting).

## Config or port edits don't take effect

**Symptom:** you edited `.toolbox.yaml` (mounts, env, proximo) or re-ran `toolbox shell -p <port>` / `-B`, but the running container behaves as before.

**Cause:** mounts, [port bindings](commands.md#publishing-ports), env vars, and proximo `ExtraHosts` are all fixed at `ContainerCreate` — Docker accepts no post-hoc changes, and `toolbox shell` reattaches to an existing container instead of recreating it.

**Fix:** `toolbox stop`, then re-run `toolbox shell …`. The container is disposable; all persistent state lives on the `~/.toolbox/` binds, so nothing is lost.

## Port already in use on the host

**Symptom:** `toolbox shell -p <port>` fails at container start with a Docker "address already in use" error.

**Cause:** another host process (or another toolbox container) already binds that host port.

**Fix:** find and stop the listener (`lsof -i :<port>` on macOS/Linux) or publish a different host port (`-p <other>:<port>`). If the conflict is another workspace's toolbox container, `toolbox stop <name|dir>` it first.

## "unknown mount name" at startup

**Symptom:** `toolbox shell` fails immediately with an error about a `mounts:` entry referencing an unknown name.

**Cause:** a patch entry (no `target:`) names a mount that isn't a default and isn't a user entry — usually a typo. Failing loud at `Plan()` is deliberate, so typos surface immediately instead of silently appending a broken mount.

**Fix:** check the spelling against the default names in [mounts](mounts.md#mounts-merge-semantics) (`toolbox mounts list` shows the effective set; the CLI suggests close matches). To add a genuinely new mount, set `target:` too (append form).

## Nerd Font placeholders in the prompt

**Symptom:** the starship prompt shows `?` / `▢` replacement glyphs instead of icons (git branch, kubernetes, language logos).

**Cause:** the host terminal has no Nerd Font configured — glyphs are rendered by the host terminal, not the container.

**Fix:** install a [Nerd Font](https://www.nerdfonts.com/) on the host and select it in your terminal profile, e.g. `brew install --cask font-jetbrains-mono-nerd-font`. (Background on why the bundled prompt pins specific glyphs: [shell-start internals](internals/shell-start.md#prompt-glyph-width).)

## xdg-open, code, or OAuth login does nothing

See [bridge troubleshooting](bridge.md#troubleshooting) — the usual causes are the bridge daemon not installed (`toolbox bridge install`), not running (`toolbox bridge status`), or a version-skewed host CLI. For OAuth callbacks that never reach the in-container CLI, see the [loopback bridge](commands.md#loopback-bridge).

## A new `.test` app is unreachable from the container

**Symptom:** `https://<name>.test` works in the host browser but fails to resolve/connect inside a shell that was already open.

**Cause:** proximo-routed hosts are discovered and pinned at container create time; apps started afterwards are invisible to the running container.

**Fix:** start the proximo stack (or the new app) first, then `toolbox stop` + `toolbox shell` to re-discover. Details: [proximo boundaries](proximo.md#boundaries-and-caveats).
