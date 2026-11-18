# herdr-core runs in a Detached Shell reached by docker exec

Status: accepted

The question comes from
[#1527](https://github.com/filippolmt/toolbox/issues/1527), built from
[#1529](https://github.com/filippolmt/toolbox/issues/1529).

The Herdr desktop app (herdr.app) drives agents through **herdr-core** (`hc`),
a runtime separate from the Herdr TUI the image bundles. Without help it can
only run agents on the host, outside the container's bundled CLIs, isolated
`~/.toolbox` credentials and containment boundary.

The decision:

**`toolbox herdr` starts a [Herdr Core Shell](../../CONTEXT.md#herdr-core-shell)
and registers it with the host's herdr-core.** Like the Orca Shell
([ADR 0023](0023-orca-is-a-first-class-toolbox-command.md)), it is a
session-planning concept: a herdr-core input on `sessionplan.PlanInput` fixes
the name and the state volume and sets `Detached`
([ADR 0022](0022-shells-can-outlive-their-terminals.md)).

**Transport: herdr-core's `--via`, with `docker exec -i <container> sh -c`.**
The host's herdr-core treats the Shell as a machine it reaches through that
command instead of ssh.

**Topology: one Shell per host and Profile, registered on the host's
herdr-core** as the machine `toolbox` (`toolbox-<profile>`). The fixed
container name is what the registration points at.

**Install: herdr-core's own `machine add --install`.** It copies the host's
`hc`, at the host's version and checked against the release manifest's
SHA-256 on the host, into the container's writable layer.

## Spike outcome

Verified before the spec, with throwaway containers on a Docker Desktop
daemon, against the herdr-core version current at the time:

- `hc machine add --name <m> --install --via 'docker exec -i <container> sh -c'`
  connects, installs `hc` in the container and reconnects by itself after a
  container restart.
- `hc run --on`, `hc read` and `hc ls` work through it; a remote terminal keeps
  running while the local core is stopped.
- Agent status (OSC 7501) crosses the connection: herdr.app on macOS shows the
  machine connected and an agent's status. Claude Code, Codex and pi report it
  natively in the image, so no hook is needed.
- From macOS, `--install` writes the Linux build of the host's `hc` to
  `$HOME/.local/bin/herdr-core` in the container: the writable layer, lost on
  recreate.
- With no runtime directory, herdr-core keeps its sockets in its state
  directory; that path stays under the OS limit at the container's home.
- Not verified: the herdr.app mobile apps against this machine.

## Considered options

**An sshd in the image**, registered as an ordinary ssh machine. Rejected: host
keys change on every recreate (the same objection as Orca's SSH mode in ADR
0023), and every user carries a daemon only this feature needs.

**A published TCP port.** Rejected: herdr-core has none. It listens on a Unix
socket only, with no API authentication.

**`hc` in the image or the Tool Catalog**, or an init script that downloads
it. Rejected: most users never run it, its version must follow the host's
`hc` rather than the image's release cadence, and herdr-core already installs
it with a verified checksum.

**One Shell per workspace.** Rejected: one machine per repository, and
detached containers piling up.

## Consequences

- Whoever can `docker exec` into the Shell controls its terminals:
  herdr-core is not a sandbox. That is the Docker daemon boundary toolbox
  already has.
- The `--via` line reaches a host command run as an sh line, so it is built
  only from the planned container name.
- A recreate (image update, Session Reload) loses the installed `hc` and ends
  running programs; herdr-core's state volume keeps the terminal records, so
  spaces come back with fresh shells.
- herdr-core is pre-1.0: its CLI flags may move. The version that matters is
  the host's `hc`, which the install copies.
