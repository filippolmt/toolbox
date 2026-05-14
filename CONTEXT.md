# Context

Project-specific vocabulary used by AI tooling and code reviewers. Add a
term here when a refactor or design conversation gives a new concept its
name; the file is the single source of truth for what those concepts mean
in this codebase.

## Glossary

### Mount Plan

The full pipeline that turns a `Config` and a workspace path into the
typed bind set handed to `ContainerCreate`, plus the shell `WorkingDir`.

Concretely: `defaults() → applyMountsRoot → mergeMounts → resolveAll →
append workspace bind → append host-path mirror (when safe)`. Owned by
`internal/mountplan`. The single seam runtime callers and tests cross is
`mountplan.Plan(cfg, workspace)`; pure merge inspection (no filesystem
side-effects) is exposed as `mountplan.Merge(cfg)`.

Why the term exists: before this concept was named, the same logic was
spread across `internal/config` (defaults + merge + root retarget),
`internal/mount` (filesystem resolve), and `internal/container/lifecycle`
(workspace + DooD-mirror append). Reading any one stage missed the
others; tests stubbed each in isolation and bugs hid in the handoffs.
The "Mount Plan" name turns one fragmented walk into one deep module.

### Tool Catalog

The canonical declaration of every bundled tool: a single typed table
whose entries describe each tool's key, default state, Dockerfile
`INSTALL_*` ARG name, and (for later phases) description, init script,
and smoke-test hook.

Concretely: `Entries → Keys / BuildArg / Defaults / IsDefault /
WriteCanonical`. Owned by `internal/catalog`. Consumers are
`internal/build/tag.go` (build args + canonical-encoded image hash via
`WriteCanonical`) and `internal/config` (thin shims over `Defaults` and
`IsDefault`); the future Phase 10 init manifest reads the optional
`Description` / `InitScript` / `SmokeTest` fields. Optional fields are
excluded from the canonical hash encoding so populating them is
hash-neutral for users.

Why the term exists: before this concept was named, three parallel
hand-maintained literals described the same 30 tools — a `KnownTools`
slice and a `ToolBuildArg` map in `internal/config/tools.go`, with the
upcoming Phase 10 init manifest poised to be the third. Adding a tool
meant editing three files plus the Dockerfile install layer; missing
one site silently broke either the build args, the image hash, or
(eventually) the boot init. The "Tool Catalog" name turns three
fan-outs into one declaration with typed accessors.

### Config Plan

The full pipeline that turns the cobra `--config` flag plus the host's
`.toolbox.yaml` files into a fully-resolved, fully-validated `*Config`
handed to subcommands.

Concretely: `viper-defaults (per-call viper.New) → walk-up search from
CWD for the nearest .toolbox.yaml → file-load (global ~/.toolbox.yaml +
project + explicit --config when set) → tool-defaults from
catalog.Keys() → mount-defaults (no-op on *Config; mountplan.Plan owns
the actual mount-defaults) → validate (ValidateMountsRoot +
ValidateShell)`. Owned by `internal/config`. The single seam runtime
callers and tests cross is `config.Plan(searchFrom, explicitOverride)`;
pure merge inspection (no filesystem, byte-input only) is exposed as
`config.Merge(global, project, explicit []byte)`. Each invocation uses
a fresh `*viper.Viper` so callers see no cross-call state.

Why the term exists: before this concept was named, the same logic was
split across `cmd/root.go::initConfig` (walk-up + viper-seeding +
file-load + env-prefix) and `internal/config/Load` (unmarshal +
validate). Reading either site alone missed half the contract;
subcommand tests primed the global viper singleton to drive `Load`,
forcing `viper.Reset()` ceremony in every test body. The "Config Plan"
name turns one fragmented init flow into one deep module mirroring
the Mount Plan + Tool Catalog deepening pattern, and the per-call
`viper.New()` instance retires `viper.Reset()` from the test surface.

### Session Plan

The full pipeline that turns a resolved `*Config`, a workspace path,
`--publish` specs, and the host CLI version into the typed plan handed
to `internal/container.Shell`: image reference, bind set, publish specs,
env, working dir, container name, container command, and security
options.

Concretely: `parsePublishSpecs → build.ResolveImage → mountplan.Plan
(or mountplan.Merge for pure inspection) → ContainerNameFor → shellEnv
→ ResolveShellCmd → NestedSandboxSecurityOpt`. Owned
by `internal/sessionplan`. The single seam runtime callers and tests
cross is `sessionplan.Plan(cfg, workspace, ports, cliVersion)`; pure
inspection (no filesystem side-effects) is exposed as
`sessionplan.Merge(cfg, workspace, ports, cliVersion)`. Port-mismatch
detection is a separate pure function `sessionplan.MissingPublishPorts(plan,
inspect)`; `internal/container` formats and emits the warning so the
UI-conventions concern stays at the Docker edge. SessionPlan does NOT
encode host-process identity (UID/GID) or daemon-fs state (sock GID);
those are read at the Docker edge by lifecycle.

Why the term exists: before this concept was named, `cmd/shell.go::runShell`
and `internal/container/lifecycle.Shell` each ran the same five-stage
sequencing inline, with image / mounts / ports / name / env derivations
scattered across two call sites and three packages. Tests of the
sequence required Docker SDK mocks (`mockClient` + `captureStderr`) just
to assert image-tag resolution or container-name determinism. The
"Session Plan" name turns the sequencing into one observable typed plan
that tests construct without Docker — the SESS-05 acceptance heart.
Together with Mount Plan, Tool Catalog, and Config Plan, the four-Seam
composition is what the v1.3 milestone calls Architecture Deepening.

### Teardown

The container stop/remove + shell-exit cleanup policy that previously
lived inline at the bottom of `internal/container/lifecycle.go::Shell`.

Concretely: `teardown.StopOne(ctx, cli, name, grace)` is the single
container-stop seam used by `toolbox stop`, `toolbox stop --all`, and
the shell-exit defer; NotFound on either ContainerStop or
ContainerRemove is tolerated. `teardown.HasActiveExecs(ctx, cli, name)`
probes for a sibling shell still attached to the same container —
inspect errors are treated as "no active execs" so a daemon hiccup
never strands a container. `teardown.OnShellExit(cli, name)` composes
the deferred policy: fresh-context (parent ctx may be Ctrl+C cancelled,
must not block teardown), skip-if-sibling, otherwise StopOne. Owned by
`internal/teardown`. Timing constants `DefaultTimeout` (30s) and
`DefaultStopGrace` (2s) live on the package, not on the lifecycle file.

Why the term exists: before this concept was named, the policy was a
4-deep nested defer block inside `Shell`, with the timing constants as
package-level vars in `lifecycle.go` and the active-exec + stop+remove
helpers loose at the bottom of the same file. Adding any
pre/post-cleanup step (log dump, longer grace for a busy daemon)
required editing inside the defer block. The "Teardown" name flattens
the defer to one call and gives `toolbox stop` and the shell-exit path
one named owner.

### Docker Identity

The host-process → container-identity translation at the Docker edge:
the `"<uid>:<gid>"` user spec passed to `ContainerCreate` and the
supplementary group IDs needed for the runtime user to talk to a
bind-mounted `/var/run/docker.sock`.

Concretely: `dockeridentity.Resolve(binds) → Identity{UserSpec, GroupAdd}`.
Owned by `internal/dockeridentity`. The single seam `container.Shell`
calls before `ContainerCreate`. `Identity.UserSpec` is built from
`os.Getuid` / `os.Getgid`; `Identity.GroupAdd` is nil unless
`/var/run/docker.sock` is in the bind set, in which case it joins gid 0
(Docker Desktop reprojects the socket as root:root) plus the host
socket GID (Linux: usually the `docker` group). The package-level
`statSockGID` var is the test seam for simulating both deployment
modes. Session Plan deliberately does NOT encode this concept (host
process + daemon-fs state are read fresh at the Docker edge so the
plan stays a pure design-time artifact composable in tests without OS
state) — Docker Identity is that edge.

Why the term exists: before this concept was named, three loose
functions (`hostUserSpec`, `dockerSockGroups`, `statSockGID`) lived
mid-file in `internal/container/lifecycle.go`, sharing a file with the
lifecycle state machine. Reading `Shell` to trace "what user does the
container run as?" meant chasing three helpers plus a var-stub seam.
Giving the concept its own package + typed `Identity` retires the
in-package stub-var and concentrates the policy in one named owner,
preserving the SessionPlan-stays-pure boundary from CONTEXT.md.

### Image Plan

The two-phase decision tree that guarantees the image referenced by a
`SessionPlan.Image` is ready before `ContainerCreate`.

Concretely: `imageplan.Refresh(ctx, cli, image)` runs at the top of
`container.Shell` and best-effort syncs registry images against their
remote (delegated to `imagepull.RefreshIfStale`, TTL-cached, errors
swallowed); no-op for local hash-tagged images. `imageplan.Ensure(ctx,
cli, image, buildArgs)` runs inside the `ActionCreate` branch and is a
hard guarantee: present locally → done; registry tag missing → fatal
(pull already had its chance); local hash tag missing → auto-build via
`build.BuildImage` using the SessionPlan's `BuildArgs`. Owned by
`internal/imageplan`. `Ensure` is exposed as a package-level `var` so
lifecycle tests can swap it without redeclaring the closure at every
call site; the inner `buildImageFn` seam lets imageplan's own tests
assert the build call without touching the embedded Dockerfile context.

Why the term exists: before this concept was named, the policy was
split — `imagepull.RefreshIfStale` ran inline at the top of
`container.Shell` and a package-level `ensureImage` closure inside
`internal/container/lifecycle.go` covered the create-branch guarantee.
Reading either site alone missed half the contract ("when do we
rebuild?" lived only in the closure; "when do we refresh?" lived only
in the inline call). Tests of code that exercised the not-found branch
redeclared the same auto-build stub closure in every body. The "Image
Plan" name turns the two-phase policy into one named owner and the
auto-build seam into a single var inside `imageplan`.

### Run Plan

The runtime decision step inside `container.Shell`: given a
`ContainerInspect` result, decide whether to connect to a running
container, start a stopped one, or create a fresh one. Pure function, no
Docker side-effects — the typed `Op` is dispatched at the Docker edge by
`lifecycle.go::dispatchOp`.

Concretely: `runplan.Compute(inspect, inspectErr) → Op{Action, ExistingID}`
with `Action ∈ {ActionConnect, ActionStart, ActionCreate}`. Owned by
`internal/runplan`. A nil `inspect.ContainerJSONBase` and an errdefs
`NotFound` both route to `ActionCreate` so callers never dereference a
half-populated record; any other inspect error is returned verbatim and
the caller aborts. Composes with Session Plan: SessionPlan resolves
design-time inputs before any Docker call; RunPlan resolves the runtime
branch after `ContainerInspect`.

Why the term exists: before this concept was named, the state machine
lived inline in `internal/container/lifecycle.go::Shell` as a 4-case
switch over the `(hasInspectData, running, inspectErr)` tuple, mixed
with side-effects (`ui.Info`, `ContainerStart`, `ContainerCreate`).
Testing the decision required a Docker client mock and an integration
harness through `Shell`; the nil-base guard pinned by
`TestShellInspectNilContainerJSONBase` was a tripwire for the same
absence of a typed decision Layer. The "Run Plan" name turns the
state machine into one observable typed Op that tests construct without
Docker, mirroring the Mount Plan / Session Plan / Config Plan deepening
pattern.

### Init Sequence

The boot-time per-tool init manifest: a catalog-declared list of small
shell scripts each `entrypoint.sh` runs in a subshell after the credential
check and before user startup hooks.

Concretely: `catalog.Entry.InitScript (Go declaration) → //go:embed
assets/init.d → tarEmbeddedContext walks the subtree → Dockerfile COPY
init.d/ /usr/local/lib/toolbox/init.d/ + chmod -R 0755 → entrypoint.sh
iterator (for f in $INIT_D/*.sh; do bash "$f" 2>"$_log"; done) with
tail-5-on-failure envelope → per-script self-gate (command -v X || exit 0)`.
Owned by `internal/build/assets/init.d/` (the scripts) and
`internal/catalog.Entry.InitScript` (the manifest). Marker logs land at
`$HOME/.toolbox-state/init/<name>.log` inside the container (bind-mount
source `~/.toolbox/state/init/` on the host). The MCP-plugins script keeps
a per-plugin `.toolbox-build-error.log` next to the per-plugin marker
(`.toolbox-built`) — a deliberate exception to the iterator-level envelope
so plugin upgrades naturally invalidate stale logs. Set-equality between
`Entry.InitScript` values and `init.d/*.sh` files is enforced by
`TestCatalogInitDBijection` Go-side and by the `init.d bijection +
executability` block in `smoke-test.sh` shell-side (mode 0755 verified
inside the built image).

Why the term exists: before this concept was named, per-tool boot logic
(rtk hook wiring, cf skill seed, graphify install, playwright-cli skills,
MCP plugin auto-build, and the per-provider credential probes for
gh / glab / gcloud / az / oci) accumulated as inline blocks in
`entrypoint.sh` with heterogeneous failure handling — only the MCP block
had a marker log + tail-5 surface; the others swallowed errors silently,
and the credential probes lived behind a hardcoded "Toolbox credential
check:" header that duplicated the parallel-and-replay pattern the Init
Sequence iterator already owned. Reading `entrypoint.sh` to find "what
runs when I open a shell" required scrolling 250+ lines and tracing
per-block gates by hand. The "Init Sequence" name makes the catalog the
single discoverable list of init scripts (credential probes included —
each provider's probe is the InitScript on its catalog Entry), the
iterator the single failure-envelope owner, and the filename `<NN>-`
prefix the explicit ordering signal — with the manifest-driven shape the
boot sequence is observable from the Go side without parsing the runtime
image.
