# Orca

Run [Orca](https://github.com/stablyai/orca)'s runtime inside toolbox, so the agents the Orca desktop app starts use the bundled CLIs, the isolated `~/.toolbox` credentials and the container boundary. The container that hosts it is the [Orca Shell](../CONTEXT.md#orca-shell): a [detached shell](../CONTEXT.md#detached-shell), one per host and [profile](commands.md#profiles). Why it is a command and not a recipe: [ADR 0023](adr/0023-orca-is-a-first-class-toolbox-command.md).

## Start and pair

Tell toolbox once which directory holds your repositories:

```yaml
# ~/.toolbox.yaml
orca_workspace: ~/src
```

Then start the runtime with the server version that matches your desktop app (Orca → About):

```sh
toolbox orca --version 1.4.262
```

The first start downloads `orca-server-linux-<arch>.tar.gz` from that release, checks it against the release's `orca-server-sha256sums.txt`, and installs it. The command returns once the server is ready and prints a link: paste it in Orca under **Settings → Remote Orca Servers → Add Server**. The runtime keeps running with no terminal open until you stop it.

`toolbox orca <abs-dir> --version …` serves another directory for this start, overriding `orca_workspace`. With neither, the command fails and names both.

Every server start creates a new pairing offer: after a restart, pair with the new link, not an old one.

## Stop

```sh
toolbox orca stop
```

It removes the Orca Shell, and succeeds with a warning when there is none. `toolbox stop --all` removes it too, and `toolbox list` shows it marked `(detached)`.

To change the server version, stop first: a second `toolbox orca` joins the running Orca Shell and leaves its version as it is.

## Profiles

`toolbox orca --profile work --version …` starts a separate Orca Shell, `toolbox-orca-work`, with the profile's own credentials and its own Orca data (pairing identity, paired devices). `toolbox orca stop --profile work` stops it. A profile name shaped like eight hex characters is refused, because it would collide with a workspace container's name.

All Orca Shells publish the same host port, so only one runs at a time.

## Port and log

The runtime listens on host `127.0.0.1:6768` only; nothing outside your machine reaches it. When the port is already taken by another toolbox container, the start fails before any container work and names the holder.

Orca's data lives on the state mount, `~/.toolbox/toolbox/state/orca` on the host (under `~/.toolbox/profiles/<name>/` with a profile, or your `mounts_root`). Inside it:

- `server/<version>/` — the cached server, and `server/current`, the one that runs. Installing a version removes every other cached version, so the cache holds one server. A checksum mismatch leaves `current` where it was.
- `serve.log` — what the installer and the server printed. When `toolbox orca` times out waiting for the link, the error names this file; the Orca Shell keeps running so you can read it. A crashed server is not restarted: `toolbox orca stop`, then start again.

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
