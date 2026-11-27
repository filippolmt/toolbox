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

Running `toolbox herdr` again is safe. On a running Shell whose machine herdr-core already knows on the Shell's own `--via` line, it checks that herdr-core's binary is still in the container (`$HOME/.local/bin/herdr-core`) and otherwise changes nothing. When it creates the container (the first start after a stop, an image update, a recreate), or finds a running one without that binary, and the machine is already known, it runs `hc machine update toolbox`, which installs `hc` in the container again. A machine called `toolbox` that herdr-core reaches some other way (your own ssh machine, say) is not this Shell's: the command leaves it alone and fails, naming its current target; rename that machine (`hc machine rename toolbox <name>`) or run `toolbox herdr --reregister` to replace it. A Shell the command just created is removed in that case. When herdr-core cannot register the machine on a Shell the command just created, the Shell is removed and herdr-core's reason is the error.

To repair a machine entry, force a fresh registration: `toolbox herdr --reregister` removes the machine (`hc machine rm`) and adds it again with `--install`.

### Without hc on the host

Without `hc` on your `PATH`, `toolbox herdr` still starts the Shell and prints the exact `hc machine add` command for it, with the machine name and `--via` line of the Shell it planned. Run it once herdr-core is installed, or run `toolbox herdr` again.

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

removes the Herdr Core Shell. With none running it exits 0 without a word. herdr-core's state (terminal records, saved screens) stays on the volume `toolbox-herdr-data`, so the next start restores the spaces. The machine stays registered on the host; the next `toolbox herdr` creates a new container and updates the machine, which installs `hc` in it again.

## Profiles

`--profile work` runs a separate Herdr Core Shell, `toolbox-herdr-work`, with that profile's credentials, its own state volume `toolbox-herdr-data-work`, registered as the machine `toolbox-work`. A profile whose name, once sanitized, looks like a workspace hash (eight hex characters) is refused: `toolbox-herdr-<8hex>` is the container of a directory named `herdr`.

## Agents on the host

An agent in a herdr.app space on the `toolbox` machine and an agent in a space on the host can talk to each other: one types a message into the other's prompt and reads its screen. It runs on herdr-core and the toolbox bridge, locally — nothing leaves your machine. Claude Code's own `SendMessage` cannot do this: its session registry and sockets do not cross Docker Desktop's VM.

It works between **agents in herdr.app spaces** (Claude Code, Codex, pi — any agent herdr-core recognises), not with a session open in Terminal.app or iTerm, and not from an ordinary `toolbox shell`.

### Before you start

1. herdr-core on the host, with `hc` on your `PATH` — the same requirement as [Start](#start).
2. The bridge installed by this toolbox version. After updating the toolbox CLI, run `toolbox bridge install` again so the daemon knows the relay:

   ```sh
   toolbox bridge install
   ```

3. A Herdr Core Shell started by this toolbox version:

   ```sh
   toolbox herdr ~/projects
   ```

   If a Shell was already running from an older version, `toolbox herdr` says it *predates hc-host*. Recreate it once:

   ```sh
   toolbox herdr stop
   toolbox herdr ~/projects
   ```

4. In herdr.app, one space on the `toolbox` machine and one on the host, each running an agent.

### From the host to the toolbox

In a terminal (or an agent) on the host, the Shell's terminals are `toolbox/<terminal>`:

```sh
hc ls                                   # every terminal: toolbox/t2, toolbox/t4, …
hc read toolbox/t2 --lines 40           # its screen and the 40 lines above it
hc send toolbox/t2 "run the test suite and tell me what fails"
```

`hc send` waits for a person's unfinished draft in that terminal before it types. Under a [profile](#profiles) the machine is `toolbox-<profile>`.

### From the toolbox to the host

In a terminal of a space on the `toolbox` machine, `hc-host` reaches the host's terminals, addressed without a machine prefix:

```sh
hc-host ls                              # the host's terminals: address, agent, status
hc-host read t2 --lines 40
hc-host send t2 "review the diff in ~/src/app"
```

The host agent receives `[from toolbox/t3] review the diff in ~/src/app`: the prefix is where to answer, so it replies with `hc send toolbox/t3 "…"` and the answer is typed into your terminal. There is no waiting command: the answer arrives as a message.

`hc-host send` types only into an agent waiting for input (`idle` or `done` in `hc-host ls`). A shell, a working agent or one showing a question or approval dialog is refused, and nothing is typed. Retry once it is free.

### Ask your agents to do it

The agents need only the address and the command. For example, to the agent on the host:

> Ask the agent in `toolbox/t2` to run the tests: `hc send toolbox/t2 "…"`. Its reply will arrive in this terminal.

And to the agent in the toolbox:

> Ask the host agent in `t2` (see `hc-host ls`) to review your diff with `hc-host send t2 "…"`. It replies in this terminal.

### Turn it off

The relay is on wherever the bridge is installed, and any toolbox container can use it. To turn it off for every container, set in `~/.toolbox.yaml` on the host:

```yaml
host_terminal_relay: false
```

It applies to the next request, with no restart. `toolbox config ui` sets it too, in the Global scope. Only `~/.toolbox.yaml` counts: in a project's `.toolbox.yaml` the key changes nothing, and `toolbox config doctor` says so. Remove the key, or set it to `true`, to turn the relay back on. The host-to-toolbox direction is herdr-core's own and is not affected.

When something refuses or does not answer, see [`hc-host` refuses or cannot reach the host](troubleshooting.md#hc-host-refuses-or-cannot-reach-the-host). How the bridge gates it: [Host terminals](bridge.md#host-terminals).

## Trust boundary

herdr-core is not a sandbox: whoever reaches its socket controls its terminals. Here that is anyone allowed to `docker exec` into the Shell, the same Docker daemon boundary that already covers every toolbox container. Nothing is published on the network: there is no sshd and no port. The reverse direction, container to host, is narrower on purpose: the host is never registered as a machine in the Shell's herdr-core, which would hand the container a shell on the host; `hc-host` reaches only waiting agents ([ADR 0025](adr/0025-host-terminal-relay.md)).

## Recreate

An image update or a [Session Reload](session-reload.md) recreates the container. Running programs end; the spaces come back from the state volume with fresh shells. The `hc` herdr-core installed lives in the container's writable layer and is gone too. When `toolbox herdr` itself created the new container (an image update taken at its start), it updates the machine, which installs `hc` again. A Session Reload (or a refresh taken by `toolbox herdr shell`) recreates the Shell outside `toolbox herdr`: until the next `toolbox herdr`, which sees the binary missing and updates the machine, herdr.app cannot reach it. Run `toolbox herdr` once after such a recreate.

## Remove the machine

```sh
toolbox herdr stop
hc machine rm toolbox
docker volume rm toolbox-herdr-data
```

`hc machine rm` makes the host's herdr-core forget the machine (`toolbox-<profile>` under a profile). Removing the volume drops the saved spaces; skip it to keep them for a later start.
