# Cross-Container Peer Messaging: share a PID namespace through a toolbox-owned anchor

Status: accepted

Claude Code's cross-session messaging (`ListAgents` / `SendMessage`) lets one
session deliver a message to another on the same machine. Toolbox gives each
workspace its own container, and upstream states the consequence plainly: *"A
container has its own filesystem, so a session inside it and a session on the
host can't reach each other. Two sessions inside the same container can still
message each other."* Three conditions have to hold for two sessions to find one
another, and toolbox satisfies exactly one of them today:

- **A shared session registry** (`~/.claude/sessions/<pid>.json`) — already
  satisfied: the `claude` default mount binds one `~/.toolbox/.claude` into every
  container, so each session already *sees* the others' registry entries.
- **A reachable inbox socket** (`/tmp/cc-socks/<pid>.sock`) — not satisfied:
  `/tmp` is per-container. Claude Code binds the socket and then `chmod`s it,
  so the shared directory must also be one where `chmod(2)` works on a socket
  inode — see the volume-vs-bind option below.
- **A resolvable pid** — not satisfied: the registry is keyed by pid and carries
  a `pidDomain`, and the liveness check runs in the reading session's PID
  namespace. Two containers can also hold the *same* pid, so the registry key is
  not unique across them.

We make all three hold, **on by default** — `peer_messaging:` in the config
(default `true`), with `toolbox shell --peer=false` / `--peer` as the per-run
override, because the namespace is shared across workspaces and declining it for
a single run has to be as cheap as leaving it on: a toolbox-owned anchor container
holds a PID namespace that participating session containers join
(`PidMode: container:<anchor>`), and a toolbox-owned Docker volume
(`toolbox-cc-socks`) is mounted at `/tmp/cc-socks` so the sockets land in one
shared directory. Sharing the namespace also makes pids unique by construction,
which removes the registry collision rather than working around it.

The anchor runs the **toolbox runtime image** with its entrypoint overridden to
a bare `sleep`, not a second minimal base image. The image is already on disk on
any host that can open a shell and its layers are shared, so the anchor costs no
extra disk — and, more to the point, there is no registry pull that can fail on
an offline host, which is the very failure mode the degrade-on-missing-anchor
rule below exists to absorb. It is `AutoRemove: false` (it must outlive the
sessions referencing it), carries the `toolbox-` prefix so `toolbox stop --all`
sweeps it up, and is filtered out of `toolbox list`, which enumerates shells.

The setting is folded into the container name. Mounts and `HostConfig` are fixed
at `ContainerCreate`, so a session whose setting changed would otherwise reattach
to an existing container carrying the old `PidMode` — and the failure is silent:
the session starts, looks healthy, and simply sees no peers. The same reattach
wart is tolerated for `--share` on named shells, where a wrong mount set shows up
immediately as a missing directory; here nothing shows up at all.

The fold has to be injective, or it reintroduces exactly the collision it
exists to prevent. The workspace branch seeds the hash (`\x00peer=1`), which is
injective by construction; the named branch appends `.peer` to the sanitized
name. The separator is the whole point: `-peer` would put `toolbox shell infra
--peer` and `toolbox shell infra-peer` in the same container, and the second
would silently inherit a shared PID namespace it never asked for. A `.` is
legal in a Docker container name and `SanitizeShellName` cannot produce one.

## Considered Options

**Bind a host directory (`~/.toolbox/cc-socks`) instead of a volume.** What
this ADR originally specified, and what shipped in #796. Rejected after it was
found broken on the primary platform: Docker Desktop for macOS serves host
binds over virtiofs, where `chmod(2)` on a socket inode fails with `EINVAL`.
Claude Code chmods each inbox socket right after binding it, so the listener
never starts — the session publishes no `messagingSocketPath` and is
unreachable, its own `ListAgents` included, with nothing on screen to say so.
`touch` and `chmod` on a *regular* file both succeed there, which is why the
gate stayed green. A named volume lives in the daemon's own filesystem on every
platform, so the bind-then-chmod sequence behaves the same everywhere. The cost
is that the directory is no longer inspectable from the host, and that a volume
is created root-owned while the session container runs as the unprivileged host
UID — hence the ownership init below.

**Run both sessions in one container.** Upstream's own answer, and it works
today. Rejected as the general answer because it collapses the per-workspace
isolation that is the point of toolbox: the second session would run against the
first workspace's mounts, not its own.

**Delegate with `docker exec` instead of messaging.** DooD is already a default
mount (`docker-sock`), so any session can already run `claude -p` inside another
toolbox container, in the right workspace, with the shared credentials. Rejected
because it starts a *new* process: it delegates work but cannot reach the live
session, which is the feature being asked for. It remains the better tool for
fire-and-forget delegation and needs no code.

**Share only the socket directory, and send with our own command.** Delivery
would work — reaching the socket file is enough on Linux, where the auth line is
optional. Discovery would not: the pid liveness check drops entries from another
namespace, so `ListAgents` would not list the peers and `SendMessage` could not
address them by name. That leaves half the feature resting on an undocumented
wire format, for less than the full one costs.

**Remote Control on both containers.** The only *supported* path: each container
connects and they see each other as sessions on other machines. Rejected as the
default because the messages then travel through Anthropic servers and it
requires a claude.ai sign-in as the active authentication — a heavy round trip
for two containers on the same host. It stays the fallback for anyone unwilling
to take on the risk below.

## Consequences

- **Participating containers can see each other's process table.** The isolation
  cost is real, and defaulting to on means every workspace pays it unless told
  otherwise: workspaces that must not see each other — different clients, say —
  need an explicit `peer_messaging: false`, which a project's own
  `.toolbox.yaml` can set without touching the global config.
- **The anchor is effectively always-on.** With the default on,
  `toolbox-peer-anchor` is created by the first shell of the day and outlives
  the sessions referencing it. `toolbox stop --all` still sweeps it up.
- **Flipping this default orphaned every container created before it.** The
  setting is part of the container identity (below), so the same workspace now
  resolves to a different container name than it did under the opt-in default;
  the first shell after the upgrade creates a new one and leaves the old one
  behind for `toolbox stop`.
- **We depend on undocumented internals.** The pid-keyed registry and the
  liveness check are implementation detail, not contract; a Claude Code upgrade
  can end this without notice, and the image upgrades Claude Code on its own
  cadence. Accepted deliberately.
- **The volume's ownership is initialised once, by a throwaway root
  container.** A Docker volume is created root-owned, the session container runs
  as the unprivileged host UID (host-UID mapping), and no bind spec can hand
  over ownership — so `container.ensurePeerSocketVolume` runs the runtime image
  as root once, to `chown` the volume to the host UID/GID and `chmod` it to
  `0700`. It runs on volume *creation* only, because confirming it otherwise
  would cost a container start per shell; what makes that safe is that a failed
  init removes the volume again. Left behind, it would satisfy the
  volume-exists fast path forever, and every later session would fail its bind
  on a root-owned directory instead — silently, the exact failure class this
  ADR exists to avoid.
- **The regression gate asserts the mechanism, not the feature.** A
  `docker-ci.yml` step starts two opted-in containers and checks that the second
  socket is visible under `/tmp/cc-socks` and that the peer's pid resolves —
  the two conditions we build — plus that the shared directory is `0700` and
  owned by the session user, which is a third condition in practice: Claude Code
  answers anything looser by falling back to `/tmp/cc-socks-<uid>` without
  saying so, and a gate that let that through would pass while the feature was
  dead. The socket-sharing half binds a real UNIX socket and chmods it, the
  sequence Claude Code runs, rather than `touch`ing a regular file: the earlier
  `touch` probe is what let the virtiofs breakage above ship green. Note the
  gate runs on a Linux runner, where a host bind would have passed that check
  too — the fix is the volume, which makes the filesystem the same everywhere;
  the sharper probe only keeps the *mechanism* honest. It deliberately does not parse `/list-agents` output, which upstream may
  reformat at will. The gate therefore cannot catch upstream changing the rules
  underneath us; nothing local can.
- **The gate is a step in the image-build job, not a job of its own.** The image
  it must exercise exists only on the runner that built it (`load: true`), so a
  separate job would have to rebuild it or ship a multi-GB tarball between jobs.
  It runs under `!cancelled()` so a failing smoke test cannot hide it, and takes
  the image under test through the `image:` config override — the canonical ref
  stays in `internal/build` alone.
- **`internal/build/assets/smoke-test.sh` is the wrong home for that gate.** It
  is a single `docker run --rm` over the image and inspects image content; two
  containers and the host CLI are out of its reach by construction.
- **A missing anchor degrades, it does not block.** If the anchor cannot be
  created, the shell starts without peer messaging and warns — the same posture
  the repo already takes for a missing or stopped proximo stack. That degrade
  leaves a container named as opted-in but created without the namespace, and
  every later run would reconnect to it and look healthy while seeing no peers;
  so a reattach whose `HostConfig.PidMode` does not match the plan warns too,
  the same split the repo already uses for published ports (a pre-flight error
  on the create path, a warning on every other). The check runs in **both**
  directions — a container that holds the namespace while the plan wants none
  shares its process table with every opted-in shell, which is the isolation
  cost arriving unasked — and it resolves the anchor through the daemon before
  comparing: Docker rewrites the `container:<name>` it is handed into
  `container:<id>`, so a verbatim comparison would fire on every *correct*
  reattach. The warning names the one container to stop, not `toolbox stop
  --all`, which would take the anchor and every sibling shell with it;
  `toolbox stop` accepts a full container name for exactly that reason.
