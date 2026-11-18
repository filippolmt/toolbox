# Orca is a first-class toolbox command

Status: accepted

The question comes from
[#1426](https://github.com/filippolmt/toolbox/issues/1426), built in
[#1502](https://github.com/filippolmt/toolbox/issues/1502).

Running Orca's agents inside a toolbox container (bundled CLIs, the isolated
`~/.toolbox` credentials, the containment boundary) used to be a hand-built
recipe: download and verify the server, persist its state, publish a port,
start it in a shell, keep a terminal open, and dig the pairing link out of its
output.

The decision:

**`toolbox orca` is a command, and the [Orca Shell](../../CONTEXT.md#orca-shell)
is a session-planning concept.** `toolbox orca [dir] --version <v>` builds a
Session Intent like `shell` does and goes through `startSession`; the Orca
input on `sessionplan.PlanInput` fixes the name, port, env and data
directory, and sets `Detached` ([ADR 0022](0022-shells-can-outlive-their-terminals.md)).
An init script gated on the plan's env installs the server on the state mount
and starts it; the host prints the pairing link from the server's log.

**Remote Orca Server mode, not Orca's SSH mode.** The desktop app pairs with a
server published on host loopback.

## Considered options

**A recipe from generic mechanisms**: named-shell ports, a `--detach` flag on
`toolbox shell`, a startup hook that starts the server. Rejected: every user
assembles the same five steps, each easy to get wrong (a wildcard bind, the
checksum, the data directory on a persistent mount), and a generic detach
flag turns every Shell into a container nobody remembers starting.

**Orca's SSH mode**: the app connects over SSH and deploys its own relay.
Rejected: it needs an sshd in the image, host keys change on every container
recreate, and the relay is rebuilt per container.

**The server in the image or the Tool Catalog**: Rejected: every user would
carry a tool most never run, and the server version has to match the desktop
app, not the image's release cadence.

## Consequences

- One Orca Shell per host and Profile, because Orca's lock admits one runtime
  per data directory.
- The server version is the user's input (`--version`), validated at the CLI
  as a trust boundary: it reaches a download URL and a container env.
- Orca writes into the shared agent config under `~/.toolbox` (hooks, a Claude
  Code keybinding); the [Orca guide](../orca.md) lists what and how to undo it.
