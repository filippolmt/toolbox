# Herdr desktop app

Run the agents the Herdr desktop app (herdr.app) starts inside toolbox, with the bundled CLIs, the isolated `~/.toolbox` credentials and the container boundary. herdr.app talks to **herdr-core** (`hc`), not to the Herdr TUI the image bundles; the container that hosts herdr-core is the [Herdr Core Shell](../CONTEXT.md#herdr-core-shell), a [detached shell](../CONTEXT.md#detached-shell), one per host and [profile](commands.md#profiles). Why it works this way: [ADR 0024](adr/0024-herdr-core-runs-in-a-detached-shell-reached-by-docker-exec.md).

## Start

Install herdr-core on the host first (herdr.app ships it, or see [herdr.dev/core](https://herdr.dev/core/)), so `hc` is on your `PATH`. Then:

```sh
toolbox herdr ~/src
```

The directory is the workspace the app's spaces can reach. To skip it on every start, set it once:

```yaml
# ~/.toolbox.yaml
herdr_workspace: ~/src
```

A `dir` argument overrides `herdr_workspace` when it starts a Herdr Core Shell. With neither, the command joins a running Herdr Core Shell on its own workspace ([run it again](#run-it-again)); with none running, it fails and names both. Either way the directory must exist and must not be your home itself (`~`, `~/`, or its absolute path): the Shell would otherwise mount the whole home.

The command starts the Herdr Core Shell (`toolbox-herdr`) and adds it to the host's herdr-core as the machine `toolbox`:

```sh
hc machine add --name toolbox --install --via 'docker exec -i toolbox-herdr sh -c'
```

herdr-core installs its own `hc` in the container, at the host's version, checked against the release's SHA-256. Open herdr.app: the `toolbox` machine shows as connected, and a space on it runs its terminals and agents in the container. Claude Code, Codex and pi report their status to the app without any hook.

When herdr-core cannot add the machine on a Shell the command just created, the Shell is removed and herdr-core's reason is the error.

The Herdr TUI (`herdr` in a toolbox shell) is unaffected: the two runtimes coexist and do not see each other's terminals.

## Open a terminal in it

```sh
toolbox herdr shell
```

It attaches a terminal to the running Herdr Core Shell, on its workspace; `exit` leaves herdr-core running. With no Herdr Core Shell running it fails and tells you to start one with `toolbox herdr`. A [session reload](session-reload.md) from that terminal re-enters `toolbox herdr shell`.

## Run it again

A second `toolbox herdr` on a running Herdr Core Shell creates nothing: it says the Shell is already running and on which workspace. With no `dir` argument and no `herdr_workspace`, that workspace stands in. A `dir` argument or `herdr_workspace` that differs from it is not applied: the Shell keeps its own, and the report says so; `toolbox herdr stop` first to mount another.

## Stop

```sh
toolbox herdr stop
```

removes the Herdr Core Shell. With none running it exits 0 without a word. herdr-core's state (terminal records, saved screens) stays on the volume `toolbox-herdr-data`, so the next start restores the spaces. The machine stays registered on the host; a new container has no `hc` yet, and `hc machine update toolbox` installs it again.

## Profiles

`--profile work` runs a separate Herdr Core Shell, `toolbox-herdr-work`, with that profile's credentials, its own state volume `toolbox-herdr-data-work`, registered as the machine `toolbox-work`. A profile whose name, once sanitized, looks like a workspace hash (eight hex characters) is refused: `toolbox-herdr-<8hex>` is the container of a directory named `herdr`.

## Trust boundary

herdr-core is not a sandbox: whoever reaches its socket controls its terminals. Here that is anyone allowed to `docker exec` into the Shell, the same Docker daemon boundary that already covers every toolbox container. Nothing is published on the network: there is no sshd and no port.

## Recreate

An image update or a [Session Reload](session-reload.md) recreates the container. Running programs end; the spaces come back from the state volume with fresh shells. The `hc` herdr-core installed lives in the container's writable layer and is gone too: `hc machine update toolbox` installs it again.

## Remove the machine

```sh
toolbox herdr stop
hc machine rm toolbox
docker volume rm toolbox-herdr-data
```

`hc machine rm` makes the host's herdr-core forget the machine (`toolbox-<profile>` under a profile). Removing the volume drops the saved spaces; skip it to keep them for a later start.
