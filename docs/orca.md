# Orca

Run [Orca](https://github.com/stablyai/orca)'s runtime inside toolbox, so the agents the Orca desktop app starts use the bundled CLIs, the isolated `~/.toolbox` credentials and the container boundary. The container that hosts it is the [Orca Shell](../CONTEXT.md#orca-shell): a [detached shell](../CONTEXT.md#detached-shell), one per host and [profile](commands.md#profiles). Why it is a command and not a recipe: [ADR 0023](adr/0023-orca-is-a-first-class-toolbox-command.md).

## Start and pair

Tell toolbox once which directory holds your repositories:

```yaml
# ~/.toolbox.yaml
orca_workspace: ~/src
```

Then start the runtime:

```sh
toolbox orca
```

The first start downloads `orca-server-linux-<arch>.tar.gz` from the [server version](#server-version)'s release, checks it against the release's `orca-server-sha256sums.txt`, and installs it. The command returns once the server is ready and the desktop app lists it (see [pairing](#pairing)). The runtime keeps running with no terminal open until you stop it.

`toolbox orca <abs-dir>` serves another directory for this start, overriding `orca_workspace`. With neither, the command fails and names both. Either way the directory must exist and must not be your home itself (`~`, `~/`, or its absolute path): the Orca Shell would otherwise mount the whole home.

### Server version

The server runs the same version as the Orca desktop app on your Mac, so the two always speak the same protocol. toolbox reads the app's `CFBundleShortVersionString` from `/Applications/Orca.app`; when the app is elsewhere, it finds it through Spotlight by its bundle identifier, `com.stablyai.orca`. After the desktop app updates itself, `toolbox orca stop` and `toolbox orca` bring the server to the new version.

`--version 1.4.262` overrides the app's version. It is required when there is no desktop app on this machine (on Linux toolbox does not look for one), or when the app is a release candidate: RC releases publish no server. With neither an app nor `--version`, the command fails before any container work and names both.

### Pairing

`toolbox orca` registers the runtime with the desktop app as the environment `toolbox` (`toolbox-<profile>` under a [profile](#profiles)). When the app already lists that environment, it does nothing; otherwise it asks the server in the Orca Shell for a pairing offer (`orca-ide pair --address 127.0.0.1`) and adds it with Orca's CLI:

```sh
orca environment add --name toolbox --pairing-code 'orca://pair?code=…'
```

The pairing code sits on that command line, visible in the process list for the few seconds the call runs: that is how Orca's CLI takes it.

toolbox uses the CLI bundled inside the app (`Orca.app/Contents/Resources/bin/orca`), else an `orca` on your `PATH`. With neither, it opens the `orca://pair` link (the app registers the scheme) and prints it; when the link does not open, it only prints it. Paste a printed link in Orca under **Settings → Remote Orca Servers → Add Server**. Without a CLI toolbox cannot tell whether the app already lists the runtime, so every start offers the link again.

`toolbox orca pair` forces a fresh pairing: it removes the environment and adds it again from a new offer. Use it after the server's identity changed, for example after deleting Orca's data. With a profile: `toolbox orca pair --profile work`.

If an open desktop app does not show the new environment, restart it.

## Open a terminal in it

```sh
toolbox orca shell
```

It attaches a terminal to the running Orca Shell, the agents' own workspace and tools; `exit` leaves Orca running. With no Orca Shell running it fails and tells you to start one with `toolbox orca`. A [session reload](session-reload.md) from that terminal re-enters `toolbox orca shell`, and the recreated Orca Shell starts the server again; if the desktop app loses it, `toolbox orca pair`. Ordinary `toolbox shell` sessions run alongside it.

## Run it again

A second `toolbox orca` on a running Orca Shell recreates nothing. It says the Orca Shell is already running and at which version, prints the endpoint, and [pairs](#pairing) as a fresh start does. It needs neither `--version`, the desktop app, nor a `dir`: the running Orca Shell's own version and workspace stand in. When that server has stopped, it fails and names the log instead of reporting a dead server as ready.

## Stop

```sh
toolbox orca stop
```

It removes the Orca Shell, and succeeds quietly when there is none, so scripts can call it unconditionally. `toolbox stop --all` removes it too, and `toolbox list` shows it marked `(detached)`.

To change the server version, stop first: a second `toolbox orca` joins the running Orca Shell, leaves its version as it is, and says so.

## Profiles

`toolbox orca --profile work` starts a separate Orca Shell, `toolbox-orca-work`, with the profile's own credentials and its own Orca data (pairing identity, paired devices). `toolbox orca stop --profile work` stops it. A profile name shaped like eight hex characters is refused, because it would collide with a workspace container's name.

All Orca Shells publish the same host port, so only one runs at a time.

## Port and log

The runtime listens on host `127.0.0.1:6768` only; nothing outside your machine reaches it. When another container already publishes the port, the start fails before any container work and names the holder; when a process outside Docker holds it, the start fails naming the port.

Orca's data lives on the state mount, `~/.toolbox/toolbox/state/orca` on the host (under `~/.toolbox/profiles/<name>/` with a profile, or your `mounts_root`). Inside it:

- `server/<version>/` — the cached server, and `server/current`, the one that runs. Installing a version removes the cached versions older than it, so upgrades leave one server; after a downgrade the newer version stays cached for the next upgrade. A checksum mismatch leaves `current` where it was.
- `serve.log` — what the installer and the server printed, with a `toolbox: orca: server stopped with status <n>` line when a start ends. When `toolbox orca` times out waiting for the server, the error names this file; the Orca Shell keeps running so you can read it. A crashed server is not restarted: `toolbox orca stop`, then start again.

## Offline

Each start downloads the requested version unless it is already cached. When the download fails:

- **A version is cached**: the newest cached version starts, and `toolbox orca` prints a warning naming the version you asked for and the one that runs. A desktop app at another version may refuse to pair or misbehave; once online, `toolbox orca stop` and start again to install the matching version.
- **Nothing is cached**: `toolbox orca` fails with the installer's reason, and removes the Orca Shell, so no container is left running without a server. Connect and start again.

A checksum mismatch never falls back: it fails the same way as an empty cache.

## What Orca changes in your toolbox config

Orca's runtime writes into the agent config every toolbox shell shares under `~/.toolbox` (or your profile's root), not only into the Orca Shell:

- **Agent hooks** merged into the Claude Code, Codex, Gemini and Antigravity config. They report to Orca and do nothing outside it.
- **Codex hooks marked trusted** in Codex's config.
- **A Claude Code keybinding**, `ctrl+alt+g`, in `~/.claude/keybindings.json`.
- **Credentials stored unencrypted** under Orca's data directory: the headless server has no keyring.

To undo them, stop the Orca Shell, then remove the Orca entries from `~/.toolbox/.claude/settings.json` and `~/.toolbox/.claude/keybindings.json`, the Codex, Gemini and Antigravity hook config under `~/.toolbox`, and delete `~/.toolbox/toolbox/state/orca` to drop Orca's data and credentials.

## Troubleshooting

- A forgotten Orca Shell, or a container that keeps running with no terminal: [a shell stays in `toolbox list`](troubleshooting.md#a-shell-stays-in-toolbox-list-after-every-terminal-closed).
- `container toolbox-orca exists and is not detached`: [the not-detached refusal](troubleshooting.md#exists-and-is-not-detached-run-toolbox-stop--first).
