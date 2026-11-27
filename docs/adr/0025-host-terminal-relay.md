# The Herdr Core Shell reaches host agents through a gated hc relay on the bridge

Status: accepted

The question: an agent in the [Herdr Core Shell](../../CONTEXT.md#herdr-core-shell)
and an agent herdr.app runs on the host should be able to message each other,
locally and without setup.

The host to Shell direction already works: the host's herdr-core reaches the
Shell as the machine `toolbox`, so a host agent runs
`hc send toolbox/t2 "…"` and `hc read toolbox/t2`. The Shell to host direction
does not: the Shell's `hc` has no machine for the host.

The decision:

**The bridge gets a `/hc` route that runs the host's `hc`, and the image a
`hc-host` shim for it** — the [Host Terminal Relay](../../CONTEXT.md#host-terminal-relay).

- **Verbs:** `ls`, `get`, `read [--lines N]`, `send` — `--lines` because a
  bare `read` shows the screen only, without the history above it. No `run`, `exec`,
  `keys`, `file`, `put`, `machine`, `attach`, `wait`, no other flag. The daemon
  rebuilds the argv and puts the positionals after `--`.
- **Targets:** the host's own terminals only — no `machine/t7`, so the relay
  never reaches the machines the host's `hc` reaches in turn.
- **Send gate:** the daemon runs `hc get` first and types only into a terminal
  herdr-core reports as an agent that is `idle` or `done`: never a shell, never
  a working agent or an open dialog (`--force` is not bridged). On `idle` it
  passes `--if idle` too, so hc re-checks at the keystrokes; hc's `--if` does
  not take `done`, so a `done` target keeps a short window between the check
  and the send.
- **Reply address:** `toolbox herdr` sets `TOOLBOX_HERDR_MACHINE` on the Shell;
  `hc-host send` prefixes `[from <machine>/<HC_TERMINAL_ID>]`, and the host
  agent answers with `hc send`, which lands as a message in the Shell's
  terminal. No `wait` through the bridge: an answer is a message, not a
  request held open for minutes.
- **Default on** wherever the bridge is installed; the shim works only in the
  Herdr Core Shell, where an answer can arrive.

## Considered Options

**Claude Code's cross-session messaging (`ListAgents` / `SendMessage`).**
Locally it needs a shared session registry, a reachable inbox socket and a
shared PID namespace ([ADR 0003](0003-cross-container-peer-messaging.md)). On
Docker Desktop the containers run in a VM: neither the macOS PID namespace nor
a host socket crosses into it, so host sessions never list. A relay of the
inbox wire format through the bridge would rest on an undocumented protocol
and still stay out of `ListAgents` — the option ADR 0003 already rejected.

**Remote Control (`remoteControlAtStartup`).** The supported path between
machines, with no code. Rejected: messages and transcripts pass through
Anthropic's servers, and the requirement is a local-only channel.

**Register the host as a machine in the Shell's herdr-core** (`hc machine add`
over ssh to the Mac). Symmetric and native, but a herdr-core machine is a
shell: the container would run any command on the host, which breaks the
[Containment Boundary](../../CONTEXT.md#containment-boundary).

**Opt-in per host terminal** (a label the relay requires). Safer, rejected for
the default: the point is that it works without setup, and the send gate
already excludes shells and dialogs.

## Consequences

- **Any container holding the bridge token can prompt a waiting host agent.**
  The daemon cannot tell containers apart (one token, readable in every Shell
  by design), so the shim's Herdr-Core-Shell-only check is convenience, not a
  boundary. A host agent acts with the host's permissions on what a container
  typed: treat its input like any other untrusted text. Turning the relay off
  today means uninstalling the bridge.
- **The relay rides herdr-core's CLI**: `hc ls/get --json` and the `status`
  object (`app`, `state`) are what the gate reads. A change there makes the
  gate refuse (an unreadable `get` is not a ready agent), never type more.
- **The daemon finds `hc` in `~/.local/bin`** when its service PATH lacks it
  (the macOS LaunchAgent's is `/usr/bin:/bin:/usr/sbin:/sbin`) and passes
  `HOME`, from which `hc` finds the user's server. Verified on the developer's
  Mac before the change: `env -i HOME=… PATH=/usr/bin:/bin:/usr/sbin:/sbin hc ls --json`
  listed the host's and the Shell's terminals.
