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
