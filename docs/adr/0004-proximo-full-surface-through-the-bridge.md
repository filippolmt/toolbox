# Proximo's full surface: reach it through the bridge, never through a container-side binary

Status: accepted

Proximo (<https://github.com/filippolmt/proximo>) runs on the host: it terminates
TLS there, installs a host resolver, and its stack bind-mounts host paths. Toolbox
already carries the two ingredients that make routed `.test` names reachable from
inside a container — `ExtraHosts` pins to the host gateway and the root CA
bind-mounted at `/etc/ssl/proximo-ca.pem` (see [proximo integration](../proximo.md)).
What was missing is *administration*: an agent inside the container could only run
`up`, `down` and `status`, as bare verbs with no arguments, through the bridge shim.

The driving use case is agent-facing, and it is narrow. An app container labelled
`proximo.inspect=true` gets a JavaScript agent injected into its HTML responses;
that agent reports browser errors and DOM state, and `proximo errors` reads them
back. Both halves of that loop already work from inside a toolbox container:
`internal/inspect/assets/agent.js` upstream states the reports are *"served from
the page's own origin, so it reports same-origin and nothing here needs CORS"*, so
a browser in the container — Playwright included, whose bundled NSS store the
entrypoint already seeds with proximo's CA — reaches the inspector by the same
route it fetched the page. What is missing is only the read side, plus a way to
install proximo's own agent skill where the container's Claude and Codex can see it.

## The container never executes proximo

No proximo binary is baked into the runtime image, and no catalog row is added for
it. Under DooD the container's `docker` talks to the *host* daemon, while a
container-side proximo would resolve its state home to `/home/toolbox/.proximo`.
Upstream's stack is bind-mounted, not volume-backed — `docs/architecture.md`:
*"The watcher and Traefik share the host directory `~/.proximo/data/traefik`
(bind-mounted into both at `/etc/traefik/dynamic`, not a Docker named volume)"* —
so `proximo up` from inside would hand the host daemon a bind source that does not
exist on the host. Docker would create it empty and Traefik would come up with no
routes and no certificates.

The failure is worse than a conflict, because it is silent. The compose project
name collides under either hypothesis: if proximo pins it to a literal, it
collides by definition; if it derives it from the stack directory, both candidates
are named `stack` (`~/.proximo/stack` on the host, `/home/toolbox/.proximo/stack`
in the container). With a colliding project name `docker compose up` *recreates*
the running containers instead of failing on the already-published `:443` — the
working host stack is replaced by one pointing at empty directories, with no error
saying so. `install`, `down` and `update` reach the same place.

Keeping the real binary out of `PATH` behind a dispatcher is a mitigation, not a
barrier: an agent that can read the filesystem invokes it by absolute path. The
only robust boundary is absence.

## One endpoint, argv passthrough

`/proximo` carries a `command` plus an `args` array (`FieldArgs` in
`internal/bridge/contract.go`); the shim stops composing JSON with `printf` and
encodes it with `python3`, as `bin/code` already does. The verb allowlist in
`internal/bridge/proximo.go` grows from three entries to five — `up`, `down`,
`status`, `errors`, `skill` — but arguments are no longer refused wholesale.
Classification lives only in the daemon: the shim forwards argv and knows nothing
about proximo, so a verb or flag added upstream needs no toolbox change.

The [security boundary](../bridge.md#security-boundary) survives this in the form
it is written: the daemon still resolves exactly one binary and execs it directly
with no shell, so an attacker inside the container still *"cannot make the daemon
exec an arbitrary binary"*. What changes, and what that section must now say, is
that arbitrary *arguments* reach one known binary. One argument-shaped rule
follows: `--out` / `-o` on `errors transcript|dom` is rejected, because via the
bridge it would write to the **host** filesystem, and shell redirection inside the
container is the correct way to capture output. `errors dom` is refused outright
rather than by flag: it writes an HTML file with or without `--out`, defaulting
into the host's temp dir, so no flag rule can reach it — and through the bridge
that file lands where the container cannot read it, so nothing is lost.
Everything else passes, `--image` on `up` included — pulling an arbitrary reference is the same trust the container
already holds over the mounted Docker socket.

Two alternatives were rejected. A per-flag allowlist would be a third list to keep
aligned with a CLI we do not release, and it buys nothing the verb gate plus the
`-o` rule does not already cover. One typed endpoint per command — `since`,
`service`, `json` as fields — moves flag knowledge into the daemon at the price of
an endpoint per verb, for a surface that changes on someone else's release
schedule.

## `skill` runs with the agent home rewritten

`skill install` writes files an agent must *read*, and the container's agent homes
are not the host's. The daemon therefore runs this one verb with `HOME` and `CODEX_HOME` pointed at
the host directories *this session* binds to `/home/toolbox/.claude` and
`/home/toolbox/.codex`, at `--scope global`, so the skill lands where the
in-container Claude and Codex look.

Those directories are **not** derivable by the daemon. `mounts_root`, `--profile`
and `inherit_host_auth` each move the host source behind those two targets, and
`inherit_host_auth` can move one without the other — a daemon that assumed
`$HOME/.toolbox` would write outside the container's mounts and exit 0, the
silent no-op this ADR exists to avoid. Only the session plan knows, so
`sessionplan` exports the two resolved host paths as `TOOLBOX_HOST_AGENT_HOME`
(parent of the claude source, since upstream resolves the Claude dir as
`$HOME/.claude`) and `TOOLBOX_HOST_CODEX_HOME`; the shim forwards them, and the
daemon refuses anything that is not a clean absolute path to an existing host
directory rather than falling back to a default. A shim that sends neither — an
older image — gets the default mounts root.

Scope is forced on the `install` / `uninstall` leaves only. Upstream registers
`--scope` there and gives the `skill` parent no flags of its own, so appending it
to a bare `proximo skill` yields `unknown flag`; the default it overrides is
`project`, which resolves against the daemon's working directory. `skill install`
does not read proximo's own state home, so the rewritten `HOME` costs it no
config, TLD or CA.

The rewrite is load-bearing and was verified against upstream rather than assumed:
`internal/skill/skill.go` resolves an agent's base directory from `$CODEX_HOME`
when set and otherwise from `os.UserHomeDir()`, which on unix returns `$HOME` with
no `getpwuid` fallback. The same file's agent detection then finds both
`~/.toolbox/.claude` and `~/.toolbox/.codex`, so a single run installs for Claude
and Codex together. The response must tell the caller to restart the agent session.

## `install`, `uninstall`, `trust` and `config tld` stay host-only

`install` is impossible by construction: the daemon has to *exec* a binary that,
on a host without proximo, is not there — which is what `ErrProximoNotInstalled`
already reports, and it is extended to name the host command to run. The other
three need host root, and bridging them would mean either a GUI elevation prompt
(`osascript`, `pkexec`) or a `NOPASSWD` sudoers rule scoped to the proximo binary.
The container is where an AI agent runs, so an unattended path from container to
host root is a path a prompt-injected agent inherits. Host bootstrap stays a
one-per-machine manual step, and toolbox does not become another project's
installer.

## Availability is gated on the CA, in the shim

Before any POST the shim tests `[ -f /etc/ssl/proximo-ca.pem ]` — the same
predicate `entrypoint.sh` already self-gates its trust block on, and the
in-container shadow of `proximo.Enabled`. A host without proximo, or a workspace
with `proximo: false`, gets one clear refusal naming both possibilities instead of
a round-trip to the daemon, and the check works even when the bridge is not
installed. Deleting the shim from the image at runtime was rejected: it mutates
image content after build, and `command not found` invites an agent to install
what is deliberately absent.

## Consequences

`errors transcript` is documented upstream as *"Unredacted; may contain
credentials"*. This design routes that output from the host's in-memory buffer to
the stdout of an agent inside the container; the toolbox skill has to say so.

The availability gate lives in the shim alone, and the bridge token is readable
inside the container by design — a direct `curl` to `/proximo` bypasses it. So a
workspace that opted out with `proximo: false` is protected from *accident*, not
from a deliberately hostile container process, which can read the host's
unredacted transcript. This is accepted rather than fixed: the daemon has no way
to know which workspace a request came from, and the bridge already forwards the
host's git credentials through `/credential` — the endpoint does not widen a
boundary that was narrower before it.

The skill is installed per **host** (`~/.toolbox/.claude`) while proximo enablement
is per **workspace**, so a workspace with `proximo: false` still has an agent that
can read the skill. The shim's refusal is the whole defense, which is why its
message names the workspace case.

Uninstalling proximo from the host leaves the skill behind, and at that point the
daemon has no binary with which to remove it: `proximo skill uninstall` has to run
*before* the host uninstall, or the directory has to be deleted by hand.
