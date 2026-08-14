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
`mountplan.Plan(PlanInput{Cfg, Workspace, Profile, GitDir})`; pure merge
inspection (no filesystem side-effects) is exposed as `mountplan.Merge(cfg)`.
Session-shaped extras enter as `PlanInput` fields, never as a bind appended
to a finished plan — `GitDir` (the worktree's main-repo `.git`) joins the
merged list and goes through `resolveAll` with everything else. Mount provenance is
part of the same seam: `mountplan.Classify(cfg) []ClassifiedMount` tags each
merged entry with its `Origin` (default / patched / user / disabled,
re-including defaults the merge dropped), and `mountplan.Names(cfg)` returns
the sorted name universe a disable patch may legally reference. Both are
pure; the `mounts` CLI reads them instead of re-deriving default-vs-user
set arithmetic per handler (the mirror of the Config Schema provenance
deepening). `runMountsRemove` stays on `configedit.UserMountNames` — a
distinct file-scoped question ("what is written in this file") that is not
the same as merge-set origin.

Why the term exists: before this concept was named, the same logic was
spread across `internal/config` (defaults + merge + root retarget),
`internal/mount` (filesystem resolve), and `internal/container/lifecycle`
(workspace + DooD-mirror append). Reading any one stage missed the
others; tests stubbed each in isolation and bugs hid in the handoffs.
The "Mount Plan" name turns one fragmented walk into one deep module.

### Config Schema

The single source of truth for "which config fields exist":
`config.SchemaKeys()` reflects the `mapstructure` tags of `Config` in
declaration order. Owned by `internal/config` (`schema.go`).

Every consumer that used to hand-enumerate the field set now derives from
or is guarded against it: provenance (`configedit.diffLayer`) reflects over
`Config` generically — scalar/slice/map/pointer/struct fields compared by
`DeepEqual` keyed by tag, so a new field is attributed with no per-field
branch (shells + mounts keep per-entry attribution via `perEntryDiffKeys`);
validation runs the `config.fieldValidators` table, with `noValidationKeys`
the explicit exemption set; the resolved renderer (`cmd/config.go`) and the
annotated example (`internal/configexample`) each stay complete via a
coverage test that reflects `SchemaKeys()` and fails on an unrendered /
undocumented field. The deprecated `browser_bridge` alias is the one
documented exception — tracked in provenance but rendered only as the
canonical `bridge`.

Why the term exists: before this concept was named, the sixteen config
fields were hand-enumerated across five independent sites (struct, validation
tail, provenance diff, resolved renderer, annotated example) with no
cross-reference tie. Adding a field meant editing each by hand, and the drift
this invited had already shipped — `managed_statusline` reached only the
struct and its consumer, silently missing provenance, renderer, and example;
`agent` missed provenance and example. The "Config Schema" name makes the
reflected tag list the one authority and turns every omission from a silent
runtime gap into a red coverage test, mirroring the Tool Catalog deepening.

### Tool Catalog

The canonical declaration of every bundled tool: a single typed table
whose rows name each tool and, where it has one, its runtime init
script and its inheritable host credential path.

Concretely: `Entries → Keys / Find / HostAuthEligibleKeys`. Owned by
`internal/catalog`. An `Entry` carries exactly three fields: `Key`,
`InitScript` (relative path under `internal/build/assets/init.d/`, or
`""` when the tool needs no boot step) and `HostAuthMount` (non-nil iff
the tool's host credentials may be inherited via `inherit_host_auth:`).
Fields once parked as "reserved for a future phase" (`Description`,
`SmokeTest`) were deleted — they concentrated nothing and existed only
to be asserted zero-valued.

Consumers ask the table two questions. *Which tools may inherit host
auth* — `internal/config/plan.go` (validation),
`internal/mountplan/inherit_host_auth.go` (the RW credential binds),
`internal/configui/adapter.go` (the multi-select options) and
`internal/configexample/render.go` (the annotated template).
*Which tools ship a boot script* —
the Init Sequence, held to disk by `TestCatalogInitDBijection` and to
the smoke-test literals by `TestSmokeTestInitDCountLiteral`. A third
test, `TestCatalogDockerfilePresence`, requires every `Key` to appear as
a token in the embedded Dockerfile, so a catalog row without an install
layer fails `make go-test`.

Why the term exists: before this concept was named, three parallel
hand-maintained literals described the same tools — a `KnownTools`
slice and a `ToolBuildArg` map in `internal/config/tools.go`, with the
init manifest poised to be the third. Adding a tool meant editing three
files plus the Dockerfile install layer; missing one site silently
broke either the build args, the image hash, or (eventually) the boot
init. The "Tool Catalog" name turned three fan-outs into one
declaration with typed accessors. Build args and the catalog-driven
image hash have since been retired along with per-tool opt-out (see
`docs/internals/image-build.md`, "Tools removal"); what survives is the
declaration the bijection tests hold the Dockerfile and `init.d/`
against.

### Pending Mutation

One config edit, captured as a value before it happens:
`configedit.Mutator` — `func(*yaml.Node)` — built by a constructor that
closes over its arguments (`configedit.Scalar`, `Bool`, `StringList`,
`StringMap`, `Shells`, `WorktreeSeed`, `SDDEnabled`, `MountsDisabled`,
`Remove`). Owned by `internal/configedit` (`mutate.go`).

The point of the term: a caller that must both *show* an edit and
*perform* it holds one object instead of describing the edit twice.

Concretely: it is the callback shape `configedit.ApplyChecked`, `Render`
and `configio.RenderDocument` all accept, so the same value can be
written to disk or rendered in memory. `configedit` exposes the pair a
truthful preview needs — `ApplyChecked(target, cwd, m)` writes,
`Render(name, src, exists, m)` returns the bytes that write would produce
— and both go through one header policy (`headerAware`), so the rendering
is not a look-alike of the write but the same computation. Every
constructor copies the collection it is handed, so the mutation is a
snapshot and cannot change meaning under a caller still editing its own
state. In `config ui`, `ApplyChecked` is the write side (the Mutator
validated against the doctor before anything reaches disk) and
`configui.previewDiff` is the read side (the same Mutator rendered
against the document as it stood when the editor opened, diffed against
it). `Model.pendingMutator` reads it off the edited key's Key Descriptor
row, so there is one place that decides what a pending edit *is* — and
that place is keyed on the config key, the axis the writers use.

The mutators' own semantics are pinned in
`internal/configedit/mutate_test.go`, on bytes alone — they are pure node
edits, so testing them through the UI's Doctor-gated write path only buys
fixtures (host credential directories, a real repo, rollback in the loop)
that the assertions do not need.

Why the term exists: `config ui` used to hold two independent models of
the same edit. `previewDoc` built a `map[string]any` keyed on the
*editor kind* while the writers mutated a `yaml.Node` keyed on the
*config key*, and they disagreed wherever those axes failed to coincide:
`sdd` previewed a list where a map is written, `shells` under-reported
the `env:` block the writer preserves, and `mounts` — whose checkboxes
select what to *disable* — rendered the exact inverse of the pending
edit. Naming the pending mutation, and making it a value the two sides
share, is what makes the preview structurally unable to lie about the
write. A preview that re-derives the mutation is the defect, not a
convenience.

### Key Descriptor

Everything `config ui` knows about one config key, as a single row:
`configui.keyDescriptors[key]` — the editor kind with the typed
accessors that seed it, the Pending Mutation constructor, and the
display facts (collection noun, entry names, scope-node count, scalar
hint). Owned by `internal/configui` (`descriptor.go`), keyed by Config
Schema key.

Concretely: `displayValue`, `nodeDisplay`, `detailEntries`,
`enumOptions`, `hasEditorEscape`, `Model.openEditor` and
`Model.pendingMutator` read the row rather than switching on the key
themselves, so the editor a key opens and the mutation it writes are the
same declaration. The two per-key facts that are *not* presentation stay
where they belong and are asked, not restated: which keys are attributed
per entry (`configedit.PerEntryKey`, read by `originFor`) and which
deprecated alias folds into which live key (`config.DeprecatedAliases`,
read by `Keys` and `scopeNode`). `TestKeyDescriptorsCoverEveryKey` demands a row per
`Keys()` entry, and three behavioural guards ride on it: every key
displays something, every editable key opens a seeded editor, and every
open editor has a Pending Mutation behind it.

Why the term exists: `configui` was the last Config Schema consumer
whose omissions surfaced as a runtime status message instead of a red
test. The same key list was re-derived by ten switches across two files,
so a key missing from one of them rendered a blank row, listed no
entries, or reported "no interactive editor yet" — with a green suite.
Naming the per-key row turns "add a config key" into one row plus its
`TestPreviewMatchesWriterForEveryEditableKey` case, and turns every
omission into a failing test, mirroring the Tool Catalog deepening.

### Config Plan

The full pipeline that turns the cobra `--config` flag plus the host's
`.toolbox.yaml` files into a fully-resolved, fully-validated `*Config`
handed to subcommands.

Concretely: `viper-defaults (per-call viper.New; only the EnvBoundKeys
seeding AutomaticEnv needs — per-tool defaults are gone) → walk-up
search from CWD for the nearest .toolbox.yaml → file-load (global
~/.toolbox.yaml + project, or explicit --config which short-circuits
both) → TOOLBOX_* env overlay → unmarshal (+ env key-case restore) →
defaults backstop → validation tail (applyValidationTail)`. Mount
defaults are not part of it — `mountplan.Plan` owns those. Owned by
`internal/config`. The single seam runtime
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
`--publish` specs, the host CLI version, and the named shell as the user
typed it into the typed plan handed to `internal/container.Shell`: image
reference, bind set, publish specs, env, working dir, container name,
container command, and security options.

`PlanInput.Name` carries the raw name and both derivations from it stay
behind the seam: `SanitizeShellName` for the container suffix, and
`config.NormalizeShellKey` + `EffectiveEnv` for the `shells.<name>.env`
overlay on the top-level `env:`. `cmd` therefore hands the config over
untouched — it no longer pre-mixes the env layers into `cfg.Env`.

`PlanInput.Worktree` is the other optional branch: non-nil plans a
[Worktree](#worktree) session, adding the main repo's `.git` to
`mountplan.PlanInput.GitDir` and the agent launch to `ExecCmd`. Nothing
downstream of the seam is mutated by the caller — `cmd` no longer patches a
finished plan.

Concretely: `parsePublishSpecs → build.ResolveImage → mountplan.Plan
→ ContainerNameFor → shellEnv → ResolveShellCmd →
NestedSandboxSecurityOpt`. Owned by `internal/sessionplan`. The single
seam runtime callers and tests cross is `sessionplan.Plan(PlanInput)`;
there is no fs-free twin — a test that needs one asserts against `Plan`
with a `t.TempDir()` HOME (`planWorkspace`). Port-mismatch
detection is a separate pure function `sessionplan.MissingPublishPorts(plan,
inspect)`; `internal/container` formats and emits the warning so the
UI-conventions concern stays at the Docker edge. Host-port conflict
detection follows the same split — `sessionplan.ConflictingPublishPorts(plan,
occupied)` decides, `internal/container` reads the occupied set off the
daemon and words the pre-flight error. SessionPlan does NOT
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

### Image Plan

The two-phase decision tree that guarantees the image referenced by a
`SessionPlan.Image` is ready before `ContainerCreate`.

Concretely: `imageplan.Refresh(ctx, cli, image)` runs at the top of
`container.Shell` and best-effort syncs the image against its registry,
steered by the Image's pull policy — `never` skips the round-trip,
`always` forces `imagepull.ForcePull`, `auto` (default) goes through
the TTL-cached `imagepull.RefreshIfStale`; errors are swallowed.
`imageplan.Ensure(ctx, cli, image)` runs inside the `ActionCreate`
branch and is a hard guarantee: present in the local store → done;
otherwise fatal, because the pull already had its chance. **`Ensure`
never builds** — `toolbox build` is the explicit user-driven path for a
local rebuild (the auto-build branch died with the local-hash image
tag). Owned by `internal/imageplan`. `Ensure` is exposed as a
package-level `var` so lifecycle tests can swap it without redeclaring
the closure at every call site.

Why the term exists: before this concept was named, the policy was
split — `imagepull.RefreshIfStale` ran inline at the top of
`container.Shell` and a package-level `ensureImage` closure inside
`internal/container/lifecycle.go` covered the create-branch guarantee.
Reading either site alone missed half the contract ("when do we
rebuild?" lived only in the closure; "when do we refresh?" lived only
in the inline call). Tests of code that exercised the not-found branch
redeclared the same auto-build stub closure in every body. The "Image
Plan" name turns the two-phase policy into one named owner and the
create-branch guarantee into a single var inside `imageplan`.

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

### Bridge Contract

The daemon↔shim wire contract: the container-side paths and state filenames
the shell shim (`internal/build/assets/bin/bridge-lib.sh`) uses to reach the
host bridge daemon, and the Go constants the daemon writes them from
(`internal/bridge/paths.go`: `ContainerDir`, `LegacyContainerDir`,
`ContainerSocket`, `tokenFile`, `portFile`).

Concretely: the contract is two artifacts in two languages linked by one test.
`TestBridgeContract_ShimMatchesGo` (`internal/bridge/paths_test.go`) reads the
shim and asserts each Go literal appears in it verbatim, so a rename on either
side fails the build. The other half of each path (the mount `Target`) is
already pinned by `mountplan.defaults`; this seam pins the shim half.

Why the term exists: before this concept was named, the socket path, the state
dir, the legacy fallback dir, and the `token`/`port` filenames were hardcoded
independently in Go and in shell, held together only by comments ("Must match
BRIDGE_SOCK in bridge-lib.sh"). A rename on either side broke the
container→host transport silently — no failing test, only a user's `xdg-open`
that stopped reaching the host. The "Bridge Contract" name turns that
comment-enforced invariant into a red-on-drift bijection test, mirroring the
Init Sequence `init.d` bijection and the Tool Catalog fan-out collapse.

### Worktree

The per-branch worktree subsystem behind `toolbox worktree` (create / open /
list / rm / prune / sync): one branch → one git worktree under
`<repo-root>/.worktrees/tbx-<branch>` → one path-scoped toolbox container.

Concretely: `worktree.Service{git}` (owned by `internal/worktree`) owns the git
+ filesystem side of every op. Every git invocation crosses one seam,
`Git{Output, Run, PushDelete}` (production impl `RealGit`, a fake in tests) —
`Output` reads, `Run` mutates, and `PushDelete` is the one command needing more
than an arg list (a bounded context and a scrubbed env for the origin
round-trip), a named domain method so the generic pair stays cheap to fake — and container
teardown/status use `container.Stop` / `ContainerInspect` through the existing
`client.APIClient` seam — so `Create` (prepare), `Open` (resolve), `List`,
`Rm`, `Prune` and `Sync` run in tests with a fake git and a nil client, no real
repo and no daemon. Pure decisions (sync/prune plans, porcelain parse, seed
candidate dedup + defaults via `DedupeSeeds`/`DefaultSeeds`, dir/branch naming)
live in `internal/worktree/plan.go`. Seed *gating* itself — the dir-wholesale
vs per-file decision and the `git check-ignore` filter — is a filesystem+git
walk that deliberately stays at the `cmd` edge (it shells out directly rather
than through the `Git` seam, since it is a filesystem-shaped git query, not the
orchestration git the seam abstracts). The
**interactive session launch** for create/open — `resolveImageDigest` +
`sessionplan.Plan` + `container.Shell` (whose TTY attach is not mockable across
packages) — deliberately stays at the `cmd` Docker edge, shared with the
`shell` command, mirroring how Session Plan / Docker Identity keep daemon-edge
state out of the pure plan. What a worktree session *is*, though, is not a
`cmd` decision: `PlanInput.Worktree{RepoRoot, Agent, Prompt}` carries it, and
Session Plan derives both halves — the main repo's `.git` bind (through Mount
Plan, so a missing source is a soft skip like any other mount) and the `ExecCmd`
that launches the agent over the resolved shell. `cmd/worktree.go` is then flag
parsing + dispatch + that launch; the gitignored-state seeding
(`seedWorktreeFiles`) stays beside the launch, as both create and open re-seed.

Why the term exists: before this concept was named, the whole subsystem —
git shell-out (~30 inline sites), container ops, filesystem seeding, and pure
decisions — interleaved in a single 1334-line `cmd/worktree.go`, the only
untestable subsystem in a codebase that had otherwise deepened every peer
(Mount Plan, Session Plan, Run Plan, …). Reading "what does `rm` do?" meant
bouncing across four concerns in one command file, and the orchestration had no
seam to fake git, so its edge cases (rm ordering, prune per-base sweep, sync
resume) were exercised only through real git in temp repos. The "Worktree"
name turns the git orchestration into one named owner behind the `Git` seam,
mirroring the Mount Plan / Session Plan deepening pattern.
