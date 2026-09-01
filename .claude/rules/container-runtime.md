---
paths:
  - "internal/container/**"
  - "internal/sessionplan/**"
  - "internal/runplan/**"
  - "internal/imageplan/**"
  - "internal/imageprefetch/**"
  - "internal/teardown/**"
  - "internal/proximo/**"
  - "internal/bridge/**"
  - "cmd/**"
  # Build assets whose invariants are documented here (loopback bridge
  # socat spawner, proximo CA trust in entrypoint):
  - "internal/build/assets/init.d/70-loopback-bridge.sh"
  - "internal/build/assets/entrypoint.sh"
---

# Container runtime gotchas — backstory under [`docs/`](../../docs/) and [`docs/internals/`](../../docs/internals/)

- **Image selection**: defaults to `:latest` from GHCR; `toolbox build` overwrites the local cache for a custom build. No `local-<hash>` fallback, no catalog-driven image hash. Source is relocatable opt-in (global/repo/`TOOLBOX_*` env): `build.ResolveImage(image, registryMirror)` — `image` (full ref, wins) > `registry_mirror` (host swap, preserves path+tag) > canonical. `pull: auto|always|never` steers **two acts** — `imageplan.Refresh` at shell start (never=skip registry; always=bypass TTL; auto=cache-aware) **and** the background [Image Prefetch](../../CONTEXT.md#image-prefetch) (`internal/imageprefetch`, on under auto/always, off under never, same 30-min attempt-stamp cadence under both on-states). The prefetch is the single detector: `bin/toolbox-update-check` is retired, `zshrc.sh` only renders the cache the host now writes. It abstains while the resolved ref carries no `RepoDigests` entry — the fingerprint of a local `toolbox build` — so an automatic pull never overwrites an explicit one. Pinned by `TestUpdateCheckCacheContract` (field + file names across the Go/zsh seam) and `TestShellPrefetchRefusals`. Env keys must be `SetDefault`-seeded in `config.Merge` or `AutomaticEnv` ignores them. Full `image` override ≠ `toolbox build` target (build tags canonical). Edit via `toolbox config set`. → [image-selection](../../docs/configuration.md#image-selection), [session-reload](../../docs/session-reload.md), [tools-removal](../../docs/internals/image-build.md#tools-removal)
- **Port bindings fixed at container creation**: `toolbox stop` before re-`shell -p …`. Corollary — a host port already published by another container can never be acquired later, so the create path pre-flights it: `sessionplan.ConflictingPublishPorts` (pure) + `lifecycle.go::preflightPortConflicts` (daemon read via `ContainerList` + wording) fail before `ContainerCreate`, naming the holder instead of leaving the daemon's "port is already allocated". Best-effort: list failure or a non-Docker holder falls through to the daemon error. → [port-bindings](../../docs/commands.md#publishing-ports)
- **Loopback bridge `-B`**: loopback-bind OAuth CLIs that bind `127.0.0.1` (codex, vanilla wrangler, and range CLIs sonar + cf) need `toolbox shell -B -p <port>:<port>`; init.d/70 spawns socat per port. cf carries NO dist patch — it binds loopback natively post-0.1.0 and is bridged on its 8877-8886 range like sonar; the old `localhost`→`0.0.0.0` sed was dropped (it poisoned the redirect_uri → Cloudflare rejected the `0.0.0.0` callback). Truly dynamic CLIs with no fixed range (gcloud, gws, tofu) can't be pre-bound → no recipe. Wildcard-bind CLIs (oci, `0.0.0.0:8181`) take plain `-p` and must NOT get `-B` — socat on eth0:<port> breaks a wildcard bind (EADDRINUSE). **Bridge is IPv4-only** (socat → `127.0.0.1`); Node 17+ resolves `listen('localhost')` to `::1` (IPv6) first, so the image sets `ENV NODE_OPTIONS=--dns-result-order=ipv4first` to force loopback CLIs to bind `127.0.0.1` — without it cf/wrangler/codex bind `::1` and the callback dies with `ERR_EMPTY_RESPONSE` (smoke test asserts node binds IPv4). Don't drop it without making the bridge dual-stack. `--oauth <tool>` presets expand to the documented recipe (`sessionplan.ExpandOAuth`, map in `internal/sessionplan/oauth.go`) — keep map and the `docs/commands.md` survey in sync. **Breaking UX**: `wrangler login` previously worked with `-p 8976:8976` alone; now requires `-B`. → [loopback-bridge](../../docs/commands.md#loopback-bridge)
## Cross-container peer messaging

### Opt-in and what it does

(`peer_messaging:` / `toolbox shell --peer`, default ON — the default is seeded in `config.Merge` via `seedEnvBoundKeys`, the only place a plain bool can tell "absent" from "explicitly false"; declining is `peer_messaging: false` / `--peer=false`): makes Claude Code `ListAgents`/`SendMessage` reach a session in ANOTHER toolbox container.

### Three conditions

Three conditions must hold and only the first is free: the shared session registry (one `~/.toolbox/.claude` bind everywhere) already holds; the inbox sockets need the `toolbox-cc-socks` **Docker volume** at `/tmp/cc-socks` (`mountplan.peerSocketBind`, appended to the bind set *after* `resolveAll` — a named volume has no host source to expand, create or stat, and no `mounts:` patch should reach a session input).

### A volume, not a host bind

**A volume, not a host bind: Claude Code `chmod`s each socket right after binding it, and Docker Desktop for macOS serves binds over virtiofs where `chmod(2)` on a socket inode fails with `EINVAL` — the listener never starts, the session publishes no `messagingSocketPath` and is unreachable, its own `ListAgents` included, with nothing on screen to say so.**

### Volume initialisation

A volume is created root-owned while the container runs as the unprivileged host UID, so `container.ensurePeerSocketVolume` initialises it once, on first sight, via a throwaway `User: "0:0"` container (named `toolbox-cc-socks-init` so an orphan is reachable, reaped through `context.WithoutCancel` so a Ctrl-C cannot orphan one holding the volume) that chowns it and chmods it `0700` (**anything looser or foreign-owned makes Claude Code fall back to `/tmp/cc-socks-<uid>` in silence**); a failed init **removes the volume** so the next shell retries rather than reusing a root-owned one forever, and a failed *removal* is reported, never swallowed. First sight, not the create path: `dispatchOp`'s `ActionStart` ensures it too, or the documented `docker volume rm toolbox-cc-socks` cleanup would let the daemon recreate it root-owned with no init. Only a `cerrdefs` not-found counts as absent — guessing absence on a transient error runs into `VolumeCreate` returning the *existing* volume, and a failing init would then force-remove one live sessions are using.

### Peer Anchor and the PID namespace

The pid-keyed registry needs one PID namespace, supplied by the **Peer Anchor** (`sessionplan.PeerAnchorContainerName`, `container.ensureAnchor`, glossary: [Peer Anchor](../../CONTEXT.md#peer-anchor)). Both halves are prepared by `container.ensurePeerRuntime`, which returns the `PidMode` **and** the bind set: either half failing degrades the session to a non-participating one — own namespace, socket mount dropped via `mountplan.WithoutPeerSocketBind` — because a session with only one half believes it is reachable and is not.

**The anchor's PID 1 must reap.** Its entrypoint is overridden past the image's shell-start init but NOT past tini (`container.tiniPath`, `["/usr/bin/tini","-g","--","sleep"]` + `Cmd: ["infinity"]`): the anchor's PID 1 is PID 1 for **every** session that joins the namespace, and reaping orphans is PID 1's job. Under a bare `sleep`, which never calls `wait()`, every process reparented after its parent exits stays a zombie for the anchor's lifetime — one PID slot each, accumulated across every shell that ever shared it (measured: 456 in one week-old anchor, `[atuin]`, `[sudo]`, `[herdr]`, `[zsh]`). The session side cannot cover this: there the image's tini is not PID 1, and the baked ENTRYPOINT carries no `-s`, so it never registers as a subreaper. Pinned by `TestShellPeerAnchorReapsOrphans`, and `TestAnchorInitMatchesImageEntrypoint` pins the tini path and `-g` against the Dockerfile ENTRYPOINT so the two cannot drift. **Self-healing is guarded, never unconditional**: `container.isCurrentAnchor` (whole-entrypoint compare against `container.anchorEntrypoint()`) gates `container.replaceStaleAnchor` — a **stopped** stale anchor is force-removed and recreated with no holder scan; a **running** one only once `container.anchorHeld` clears every holder. `anchorHeld` **fails closed**: it clears only `created`, `exited` and `dead`, so `paused`, `restarting`, `removing`, a failed list or inspect, and an inspect with no `HostConfig` all count as held. `replaceStaleAnchor` returns no error and the caller never fails on it: a refused or failed replacement warns and reuses the stale anchor, which is still a working namespace — dropping the session to no peer messaging would be worse than the reaper-less PID 1. Why, and what `docker rm -f` does to an in-use anchor: [peer-anchor-reaping](../../docs/internals/container-lifecycle.md#peer-anchor-reaping). Pinned by `TestShellPeerReplacesUnusedStaleAnchor`, `TestShellPeerReplacesStoppedStaleAnchor`, `TestShellPeerKeepsHeldStaleAnchor`, `TestShellPeerKeepsStaleAnchorWhenHoldersUnknown`, `TestShellPeerKeepsStaleAnchorWhenHolderCannotBeRuledOut`, `TestShellPeerKeepsPeerMessagingWhenReplacementFails`, and `TestShellPeerReusesRunningAnchor` for the no-op direction. → [peer-anchor-reaping](../../docs/internals/container-lifecycle.md#peer-anchor-reaping)


### Ignores mounts_root and --profile

The volume ignores both `mounts_root` and `--profile`: it is toolbox-owned infrastructure, and forking it per profile would leave two opted-in shells discovering each other and silently failing to deliver.

### Opt-in folded into the container name

The opt-in is folded into the container name on **both** branches (`peerDiscriminator` into the workspace hash, `.peer` onto the named-shell suffix — the dot is load-bearing, `SanitizeShellName` cannot produce one, so `shell infra --peer` and `shell infra-peer` stay distinct containers): `HostConfig` is fixed at `ContainerCreate`, so without it a changed opt-in reattaches to a container carrying the old `PidMode` and the shell looks healthy while seeing no peers.

### Reattach warnings

The one hole that fold cannot close — a container created while the anchor was down, or before the socket directory became a volume — is covered on the reattach path by `peerMismatchWarning` (namespace) and `peerSocketMountWarning` (a participating container that mounts no `toolbox-cc-socks`), the same split as publish ports (pre-flight error on create, warning elsewhere). `preflightHostConfig` emits at most one of the two: both prescribe the same targeted recreate. That warning fires in **both** directions (a container holding the namespace against a plan that wants none shares its process table unasked) and compares through `samePidNamespace`, which inspects the anchor first — **Docker rewrites the `container:<name>` it is handed into `container:<id>`**, so a verbatim compare against `plan.PidMode` would warn on every correct reattach. It prescribes `toolbox stop <container>`, not `--all`: `StopByName` takes a full container name verbatim (anything with the `toolbox-` prefix), which is the only handle on a `.peer` or `--profile` container. A missing anchor **degrades with a warning**, it does not block. `List` excludes the anchor by name; `StopAll` must keep sweeping it up.

### Regression gate

Rests on undocumented Claude Code internals — regression gate is `internal/container/peer_gate_test.go` (`//go:build dockergate` — the tag means *needs a real daemon and the built image*, and selects the session-reload gate beside it with no `-run` filter; run from `docker-ci.yml` — whose `paths` filter covers the image assets *and* `internal/{container,mountplan,sessionplan}/**`, because the gate drives the host CLI and every unit test around this wiring runs on Docker mocks), asserting the mechanism only, never `/list-agents` output — and it must bind a real UNIX socket and chmod it, not `touch` a regular file: the `touch` probe is what let the virtiofs breakage ship green. It runs on a Linux runner, so it cannot catch a macOS-only filesystem regression either; that is what the volume removes by construction. → [peer_messaging](../../docs/configuration.md#peer_messaging), [peer messaging](../../docs/commands.md#peer-messaging), [ADR 0003](../../docs/adr/0003-cross-container-peer-messaging.md)

- **Session reload** (meaning + rationale: [Session Reload](../../CONTEXT.md#session-reload), [Reload Marker](../../CONTEXT.md#reload-marker)): `toolbox-reload` is a **zsh function** in `zshrc.sh`, never a `bin/` script. **`cmd` performs the `syscall.Exec` (`execSelf` var), never `container`** — `Shell` returns `*reload.From` and its teardown defer is suppressed by its own named `rl` result. Order is **re-exec → Refresh + Ensure → teardown → create**; the teardown is `container.reloadTeardown`, **never `teardown.OnShellExit`**, and it waits for the removal. `reloadMarkerEnv` is emitted **only when the state mount exists** — without the bind the container still writes and the host still cannot read, which is the silent loss the marker exists to prevent. Re-entry argv is the payload's normalised `Reentry`, **never `os.Args`**. The `syscall.Exec` line is an **accepted, permanently uncovered gap**. Pinned by `TestShellReloadSuppressesTeardown`, `TestShellReloadVerifiesBeforeItDestroys`, `TestShellReloadWithNoUsableImageDestroysNothing`, `TestShellReloadKillsThroughAnAttachedSibling`, `TestPlanOmitsTheReloadMarkerWithoutTheStateMount`, `TestReloadMarkerContract`, `TestExecReloadCarriesTheHandover`, `TestPlanReloadResumesTheAgent`, and the real-daemon `TestReloadReplacesTheContainer` (`dockergate`). → [session-reload-teardown](../../docs/internals/container-lifecycle.md#session-reload-teardown), [session-reload](../../docs/session-reload.md)
- **Codex nested sandbox**: codex always installed → Docker `seccomp=unconfined` always applied. → [codex-sandbox](../../docs/internals/container-lifecycle.md#codex-nested-sandbox)
- **Container teardown = AutoRemove**: containers created with `HostConfig.AutoRemove` (`container/lifecycle.go`). Shell exit `ContainerKill`s and returns — daemon removes async (fast prompt; macOS unmount of many binds is the slow part). Consequence: a stopped container is gone, so `runplan.ActionStart` (reuse-stopped) effectively never fires — every `toolbox shell` recreates. `teardown.OnShellExit` does one inspect → sibling-exec→leave / AutoRemove→kill / legacy→`StopOne`; `StopOne` tolerates the remove `Conflict` race. → [container-teardown](../../docs/internals/container-lifecycle.md#container-teardown)
- **Host platform env**: `sessionplan.hostPlatformEnv` emits `TOOLBOX_HOST_OS` / `TOOLBOX_HOST_ARCH` (GOOS/GOARCH spelling) in **every** session, read off the host CLI's own `runtime`. Reason: `uname` inside the container reports the container, so anything cross-compiling for the host from a toolbox shell — `make go-build` — would silently produce a linux binary the host cannot execute. The Makefile prefers these and falls back to `uname`, the same shape as `HOST_SRC`/`TOOLBOX_HOST_WORKSPACE`. Env is fixed at `ContainerCreate`, so a pre-existing container needs a `toolbox stop` before it sees them.
- **herdr session is per workspace**: `sessionplan.shellEnv` emits `HERDR_SESSION=ContainerNameFor(workspace, "")`. `~/.config/herdr` is one **host-global** bind (`mountplan` defaults, `"herdr"`) and herdr persists its workspace list there with **absolute cwds**, then ignores the startup cwd whenever the restored session already has workspaces (its own log: `restored session already has workspaces; ignoring startup cwd`). Unnamed, every container reopens whatever another project saved last — a path this container does not mount, which herdr answers by **silently falling back to `$HOME`**: the shell opens on `/home/toolbox`, workspace label `~`, the sole visible entry being the `go` mount. The discriminator is deliberately empty: the identity is the **workspace**, so a `--peer` or `--profile` change, which forks the container name over the same mounted workspace, keeps the session. `TestPlanScopesHerdrSessionToWorkspace` pins both halves (basename collisions separated by the path hash; profile-invariance). Scope: the identity is the workspace *path*, so a named shell pointed at the same path as a workspace session shares its herdr session — two containers, one saved layout. Deliberate, and not the bug this fixed: both mount that path, so every restored cwd stays valid. → [herdr-session](../../docs/internals/shell-start.md#herdr-session-per-workspace) Env is fixed at `ContainerCreate` → `toolbox stop` before a pre-existing container sees it, and the pre-fix unnamed `session.json` stays on disk, orphaned.

## Bridge

### What it forwards

opt-in host daemon (`toolbox bridge install`; `browser-bridge` = deprecated alias) forwards in-container `xdg-open` to host browser (`/open`), `code`/`codium` to host editor (`/edit`, fixed allowlist, host-path translation in the shim — daemon stays workspace-agnostic; the shim accepts **both** workspace mount points: `/workspace` (prefix swapped for `TOOLBOX_HOST_WORKSPACE`) and the host-path mirror (`mountplan.WorkspaceMirrorPath`, the shell's `WorkingDir` whenever mirrored — so it, not `/workspace`, is what a bare `code .` resolves against; pass-through, already a host path). Daemon-side launch sees its own `PATH` + app bundles, never host shell aliases), and `proximo up|down|status` to the host proximo binary (`/proximo`, fixed subcommand allowlist, output+exit propagated, 120s budget vs the shared 5s `requestTimeout` — write deadline pushed per-response; child PATH augmented so proximo's own `docker` lookup survives the minimal service PATH).

### State mounts

State `~/.toolbox/toolbox/bridge/` RO-mounted (new + legacy container targets; install migrates the pre-rename `~/.toolbox/browser`) + RW nested `bridge-run` mount for `run/bridge.sock` (glossary: [Bridge Run Mount](../../CONTEXT.md#bridge-run-mount)); `bridge: false` (legacy key `browser_bridge` still read) skips all three mounts.

### Uninstall is best-effort on the state dir

`bridge.Uninstall` returns `(warning, error)`: a failed `os.RemoveAll` on the state dir is a **warning on stderr with exit 0**, not an error — a bridge-enabled shell holds the [Bridge Run Mount](../../CONTEXT.md#bridge-run-mount) open and the daemon is already gone by then. Three constraints the tests pin (`TestStateDirOutcome`, `TestStateDirOutcome_UnprovableTokenFailsClosed`, `TestUninstallSummary`): the decision lives in the pure `stateDirOutcome`, never in an injected remover (`make go-test` runs as root, where no `chmod` makes a removal fail — same reason the sudo guard sits in `cmd/`); it **fails closed on the token** (a stat that is not `fs.ErrNotExist` counts as live, since it can be defeated by the errno that defeated the removal); and only the current state dir is best-effort, `LegacyHostDir` being no mount source. **No pre-flight**, unlike `preflightPortConflicts` — there a check prevents a failed create, here it would block an uninstall that already did the irreversible 90%. Presentation stays in `cmd/`: the `warning: ` prefix (as for `CheckHostCredentialHelper`) and the stdout line from `uninstallSummary`.

### Transport

**Transport**: Linux hosts bind a unix socket too — loopback TCP is unreachable from docker-ce containers (`host-gateway` = docker0 IP, daemon binds `127.0.0.1` only); shim tries the socket first, TCP fallback **only on curl `000`** (covers macOS, Docker Desktop, stale socket — never retry a real HTTP status, `/proximo` would double-exec). Editor/proximo shims exit non-zero on failure (unlike `xdg-open`'s exit 0). → [bridge](../../docs/bridge.md), [editor-shims](../../docs/bridge.md#editor-shims), [proximo-lifecycle](../../docs/proximo.md#lifecycle-from-inside-the-container-bridge-shim)

### Host credential helper lookup

A plain-name `credential.helper` is resolved **the way git resolves it — `git --exec-path` first, then `PATH`** (`lookHelperIn`), never `exec.LookPath` alone: Apple git and Homebrew git both ship `git-credential-osxkeychain` under `libexec/git-core` and never in a `bin/` dir on `PATH`, so a PATH-only check warned on **every** macOS host about a helper that already worked. Legitimate because the consumer is `git credential fill` (`runHostCredential`), which reaches the helper through that same exec-path. Both git queries go through the one `gitOutput` seam (trimmed stdout, `""` on absent/erroring/timed-out git, 5s budget) so `checkHostCredentialHelper` takes git, GOOS and the PATH lookup as parameters — **the composition itself is what fixes the bug, so it is the seam that must stay tested**, not just the halves: `TestCheckHostCredentialHelper` (case `osxkeychain-in-exec-path-only` fails on any revert to a bare PATH lookup), plus `TestEvaluateCredentialHelpers`, `TestLookHelperIn`, `TestParseHelperList`, `TestGitOutput`.

### Never under sudo

`bridge install|uninstall` refuse at euid 0 (`EnsureUserContext` → pure `checkNotRoot`/`rootServiceAdvice`, `TestCheckNotRoot` + `TestEnsureUserContext`). Both supervisors are per-user — LaunchAgent in the caller's GUI domain, systemd unit on their user bus — and root has neither, so `sudo` could only fail *after* writing the plist/unit and a root-owned token into whatever `HOME` sudo passed through (`launchctl bootstrap gui/0` → `Bootstrap failed: 125: Domain does not support specified action`; `systemctl --user` → `Failed to connect to bus`). The guard sits in `cmd/bridge.go`, not in `bridge.Install`: `make go-test` runs the suite as root inside `golang:1.26`, so a guard inside the package would fail locally and pass in CI. `status` is unguarded — a read needs no domain.

## Workspace `safe.directory` is registered at boot, and the cause is not ownership

### What is registered

`entrypoint.sh` adds `/workspace` + `$TOOLBOX_HOST_WORKSPACE` to the **system** gitconfig, before the init sequence (sessionplan always sets that var, whether or not the mirror bind exists — with no mirror the entry is inert; a container predating the block needs a rebuild **and** a recreate, the entrypoint being baked in).

### Measured cause

Measured cause: the workspace mount point transiently reports **uid 0 while its contents keep the host uid** (a 2s probe caught 95 failures: 91 with euid=501 and mount point uid=0 in the same instant, `.git` one level down at 501 throughout; the other 4 had recovered before the probe's own stat), and git checks the *worktree*, not the files in it. A second container mounting the same host path was present in 45 of the 95 — a hint, not a proven trigger, and an undercount since those containers exit fast. Either way the wrong uid comes from Docker Desktop's file sharing, not from this image. Don't chase it in the entrypoint.

### What the fatal hides

That fatal covers three situations and names none: foreign-uid repo, wrong-uid mount point, and an ownership question git could not answer (`is_path_owned_by_current_uid()` reads "not mine" from any `lstat()` failure).

### Four invariants

Four invariants, held by `TestWorkspaceSafeDirectoryRegistration` (reassembles the block from the embedded entrypoint and runs it against stub sudo/flock/git): **system scope only** (host `~/.gitconfig` is a RW bind mount of a *single file* — `--global`, which git itself suggests, fails with `Device or resource busy` and pollutes the host config when it lands), **before the init sequence** (offset-checked: `30-graphify.sh` asks git about the workspace with output suppressed, so a fatal there skips the hook install silently), **idempotent** (`--get-all` before `--add` — the entrypoint runs once per container *start*, not per shell — sibling shells arrive via `ExecCreate`, not the entrypoint — so the re-runner is `ActionStart` on a stopped container; the shared `/tmp/toolbox-gitconfig.lock` is house discipline for `/etc/gitconfig`, not a live race: running above the init sequence is what keeps this clear of the parallel `60-glab.sh`), **non-fatal**, plus a runtime `smoke-test.sh` assertion on the registered entry.

### Scope and escape hatch

Only the exact worktree is covered — a nested repo/submodule needs its own entry. Per-command escape hatch that writes no config: `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0=…`. → [workspace-safe-directory](../../docs/internals/shell-start.md#workspace-safedirectory-dubious-ownership)

## Proximo integration

### What it does

makes [proximo](https://github.com/filippolmt/proximo)-routed `https://<name>.test` apps reachable from inside the container, for ANY client. Tri-state `proximo` (`*bool`, `proximo.Enabled`): omitted → **auto** (on iff proximo's CA exists on host — installed = works everywhere, zero opt-in); `true`/`false` force.

### Host-side discovery

Host-side `toolbox shell` reads `proximo.hosts` labels off running containers → pins each to `host-gateway` in `ExtraHosts` (DNS), and RO-mounts proximo's CA (path via `proximo config ca-path`, fallback `~/.proximo/tls/ca.pem`).

### CA trust

`entrypoint.sh` then makes trust seamless: `sudo update-ca-certificates` (curl/git/wget/python-ssl) + `certutil` into `~/.pki/nssdb` (chromium, incl. Playwright — `libnss3-tools` in base apt layer) + `NODE_EXTRA_CA_CERTS` (node). Lone gap: python-requests/certifi → set `REQUESTS_CA_BUNDLE=$TOOLBOX_PROXIMO_CA`.

### Runtime re-discovery

`--add-host` discovery at create-time only; the in-container `proximo-hosts` command (`bin/proximo-hosts`, sibling shim) is the **runtime complement** — re-discovers `proximo.hosts` via the mounted docker.sock (DooD: `docker ps -q` + `docker inspect`, since `docker ps --format` `.Labels` is a flat string not a map), resolves host-gateway (IPv4-first from `host.docker.internal`, default-route fallback) and rewrites a marker-delimited managed block in `/etc/hosts` (`sudo`) so stacks started after the shell are reachable without re-`shell`. Idempotent; markers always emitted (down'd stack self-clears).

### The watcher

**Automatic**: `entrypoint.sh` (gated on the proximo CA mount, beside the CA-trust block — NOT init.d) backgrounds `proximo-hosts --watch`, which seeds once then re-syncs on every `docker events` start/die of a `proximo.hosts` container (reconnecting loop). Asserted in `smoke-test.sh` (bin present + watcher wired in entrypoint); not a catalog tool → no init.d/completion edit. Trust lives in `entrypoint.sh` (NOT `init.d` → ties to no catalog tool, no bijection edit). `internal/proximo` (pure) + `container/lifecycle.go` (discovery).

### Stack lifecycle from inside

Stack lifecycle from inside: `/usr/local/bin/proximo` shim → bridge `/proximo` (allowlist `up|down|status`; `install`/`uninstall` = sudo → host only; never install the real binary in the image — its compose stack bind-mounts container-side `~/.proximo` paths the host daemon can't see). → [proximo-integration](../../docs/proximo.md), [proximo-lifecycle](../../docs/proximo.md#lifecycle-from-inside-the-container-bridge-shim)

