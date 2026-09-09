# The session network is the developer's to choose: a name toolbox joins, hashes and never creates

Status: accepted

Every figure below is **as measured when this decision was taken** — evidence for the choice, not a description of the repo today. Nothing here is kept in sync; current values live in the files that set them.

Toolbox never calls `NetworkCreate` and never sets `NetworkMode`, so a session
container lands on whatever network the daemon hands it: one interface, one
default route. That is the right default and it stays the default. What is
missing is a way out of it.

The failure that names the gap was measured on 9 September 2026, on macOS with
Docker Desktop. A WireGuard tunnel routed `172.18.0.0/16` to a remote machine;
separately, a local compose stack had taken `172.18.0.0/16` from Docker's
default address pool. From the host terminal everything worked — Docker's
bridges live inside Docker Desktop's Linux VM, not in the host's routing table.
From inside a [Shell](../../CONTEXT.md#shell) the name resolved, the connection
hung, and it timed out after minutes with nothing that named a routing
conflict. A `git push` to that host looked like a broken remote or a credential
problem.

That specific packet dies in the VM's routing table, *after* it has left the
container, so no choice of network would have saved it — only keeping local
Docker networks off VPN-routed space does, which is a daemon-level
`default-address-pools` decision and documentation, not code. But the case next
door has no answer at all today: when the subnet of the network the session
container *itself* joins overlaps a route the host already owns, the container's
own connected route shadows that route and there is no way to move out of the
way. This ADR is about that case.

So: a `network:` config key naming a Docker network to join, with
`toolbox shell --network <name>` as the per-run override, and three decisions
around it that are less obvious than the key.

**The name is folded into the container identity, through the hash seed.**
`HostConfig` is fixed at `ContainerCreate`, so a network that changed under an
existing container would be a lie the shell reattaches to. Every other
`HostConfig`-fixed setting is already folded into the container name — the
profile identity and the peer opt-in both are — and this one joins them. It
folds into the *hash seed*, the way `peerDiscriminator` folds the peer opt-in
for a workspace shell, and never into the visible basename: a network name is
free text from the user, and the injectivity that `.peer`'s dot separator buys
for a named shell cannot be bought for arbitrary input. Hashing sidesteps the
question instead of answering it.

**Toolbox refuses `host`, `none` and `container:<id>`.** They are legal
`NetworkMode` values and none of them serves the reason this key exists.
On Docker Desktop `host` shares the VM's network namespace, so it does not
escape the VM's routing table at all, and it silently redefines what `-p` means.
`none` is a different feature wearing this key's clothes. `container:<id>`
overlaps the peer-messaging namespace design and invites a foot-gun. The key
means one thing — a network to join — and an explicit refusal keeps it there.

**Toolbox never creates the network.** A missing one is refused before create,
with the `docker network create --subnet=… <name>` to run named in the error.
The subnet is the single decision only the developer can make: it has to avoid
routes toolbox cannot see, which on macOS live outside the VM entirely. Creating
the network would put IPAM in this codebase and would have to guess.

## Considered options

**Make the container's network configurable to fix the measured failure.** This
was the original request, and it does not work: the collision is resolved in the
VM's routing table after the packet leaves the container, so every network —
`host` included — produces the same timeout. Shipping the key as a fix for that
case would have been a promise the code cannot keep. The key ships for the
adjacent case, which is real and unaddressed, and the measured case gets the
`default-address-pools` documentation instead.

**Record the subnet in the plan, not just the name.** A network can be removed
and recreated with a different subnet under the same name, and the shell would
reattach without a word — the original silent timeout, returning. Rejected
because a check needs something to compare against: the host's routes, which
toolbox cannot read, and on macOS cannot reach. A subnet recorded but never
validated is worse than a name, because it looks like a guarantee. Any subnet
awareness belongs to a diagnostic that compares Docker's networks against the
container's own routes, which is a separate piece of work.

**Warn on mismatch instead of folding into the identity**, the way a container
created before the peer socket volume existed is diagnosed. Rejected: the
warning is what you build when a second container is unacceptable, and here it
is not — two networks are two legitimate concurrent sessions. The fold also
makes the mismatch case unreachable rather than merely reported.

**Make the key a tri-state or otherwise env-bound.** Neither earns its keep. The
key's absence and an empty value mean the same thing, and `--network` already
covers the per-run override, so a `TOOLBOX_NETWORK` would add a permanent
surface for a need the flag serves.

## Consequences

**`--network` has to be replayed by the re-entry form.** It decides the
container name, so a [Session Reload](../../CONTEXT.md#session-reload) that
dropped the flag would reload into a different container from the one its
payload names for teardown — the same reason `--profile` and `--peer` are
carried.

**No mismatch warning is needed, and none should be added.** With the fold, a
changed key yields a different container name, so the case
`peerMismatchWarning` covers — a healthy container carrying a configuration the
plan no longer asks for — cannot arise for this key.

**The refusal belongs to the key's row, not to the shell.** The three rejected
`NetworkMode` values have to be refused by the key's own `Scalar` verdict in
`config.Keys()`, which is enforced twice over: as a fail-fast through
`config.ValidateKey` for whichever presentation layer is writing, and
authoritatively by `applyValidationTail`, which every write reaches through
`configedit.ApplyChecked`. Putting the check on the session path instead would
let every config surface store a value that the next `toolbox shell` rejects —
the error arriving as far as possible from the place it was typed. Because the
editor reads its row from `config.Keys()` too, `network:` needs no new editing
mode (`EditorText` already serves the other free-text key) and inherits the
refusal by construction rather than by a second implementation.

**The "no network chosen" fallback has to go through the shared accessor.**
`TestRendererParity` requires the renderer, `config.EffectiveValue` and the
editor's own display to agree for every fallback-bearing scalar, so what a
session shows when the key is unset — the network the daemon would pick on its
own — is spelled once in the accessor and read from there. A fallback written
straight into the editor fails that test, naming the key.

**Peer messaging composes with it.** Verified on Docker Desktop: a container
with `PidMode: container:<anchor>` joined a user-defined network while the
anchor stayed on the default one — own network namespace, shared PID namespace,
named volume mounted, `ExtraHosts` intact. Docker neither refused nor
overrode the network.

**The host bridge is unaffected on Docker Desktop, and proximo is the open
risk on native Linux.** Verified: `host.docker.internal:host-gateway` resolved
to the same host address on a user-defined network as on the default one — not
to the network's own gateway — and a TCP connection to a listener on the host
loopback succeeded from both. The unix transport is network-independent by
construction, since it is reached through a bind mount. On native docker-ce,
`host-gateway` is documented as the default bridge's gateway, and whether that
address is reachable from a user-defined bridge could not be verified here. For
the bridge that is moot — the unix socket wins there — but proximo has no
second transport, so its `.test` reachability is what a wrong answer would
break.

## Verification

The Linux question above is the one that gates shipping: a real-daemon gate on
a Linux runner has to confirm that `host-gateway` is reachable from a
user-defined network before the key ships, or proximo's guarantee becomes
platform-dependent without saying so. It belongs beside the existing
real-daemon gates, which exist for exactly this class of fact — something the
development machine cannot demonstrate, where the last assumption made in its
place shipped a green build over a broken bind mount.
