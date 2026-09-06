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
it. Provenance (`configedit.diffLayer`) reflects over `Config` generically —
scalar/slice/map/pointer/struct fields compared by `DeepEqual` keyed by tag,
so a new field is attributed with no per-field branch (shells + mounts keep
per-entry attribution via `perEntryDiffKeys`). Everything else — validation,
`config show`, the annotated example, `config ui` — reads the key's
[Key Row](#key-row), the table `SchemaKeys()` is the index of. The deprecated
`browser_bridge` alias is the one documented exception — tracked in
provenance, but rendered only as the canonical `bridge` (its row is
`KindAlias`, which is what says so).

Why the term exists: before this concept was named, the sixteen config
fields were hand-enumerated across five independent sites (struct, validation
tail, provenance diff, resolved renderer, annotated example) with no
cross-reference tie. Adding a field meant editing each by hand, and the drift
this invited had already shipped — `managed_statusline` reached only the
struct and its consumer, silently missing provenance, renderer, and example;
`agent` missed provenance and example. The "Config Schema" name makes the
reflected tag list the one authority and turns every omission from a silent
runtime gap into a red coverage test, mirroring the Tool Catalog deepening.

### Key Row

The one declaration of a config key. `config.Keys()` returns one
`config.Key` per [Config Schema](#config-schema) key, in Config
declaration order, carrying everything the surfaces used to restate: the
summary and human default `config ui` shows, the annotated block
`config example` prints, the value `Kind` `config show` renders and the
`Editor` the TUI opens, the typed readers those present the value through
(`Str` / `Tri` / `List` / `Pairs`), the `Validate` half of the load path's
validation tail, the `Scalar` fail-fast verdict behind
`config.ValidateKey`, and the `Effective` fallback behind
`config.EffectiveValue`. Owned by `internal/config` (`keys.go`).

The four surfaces read rows instead of restating them: the validation tail
runs each row's `Validate` in table order; `configrender` walks `Keys()`
and shapes each key by its `Kind` (the three block keys — `worktree`,
`shells`, `mounts` — carry entries too structured for a generic shape and
keep their own writer); `configexample` concatenates each row's `Example`,
splicing in at `config.ExampleListing` the two live listings config cannot
compute itself (the catalog's eligible CLIs, the default mounts —
`mountplan` imports config, so config cannot import it back); `configui`
opens the editor the row names and seeds it from the row's readers, adding
only what is presentation ([Key Descriptor](#key-descriptor)).

Declaration order is presentation order: `config show`, the annotated
example and the `config ui` key list all walk this one table, so moving a
field in the struct moves it in all three (`TestSchemaKeys` pins the
order).

Why the term exists: `SchemaKeys()` made drift *loud* — a new key turned a
coverage test red instead of shipping a silent gap — but it left the
fan-out itself intact. One key was still declared in six places (struct
tag, validator table, the `ValidateKey` switch restating that table,
`KeyDocs`, the example's prose, the renderer's printf, the TUI
descriptor), and the per-surface coverage guards existed to make that fact
fail visibly rather than to remove it. Naming the row turns "add a config
key" into one row, and collapses those presence guards into
`TestEveryKeyHasACompleteRow` — one guard, over the one table — which
frees each surface's own tests to assert its output rather than the
presence of a row.

### Config Scope

Which layer of the config a value is read from or written to: the
per-user global file, or the project file the walk-up from the current
directory finds. One axis with three faces, each owning one verb —
`configedit.Where` writes (`Resolve` turns it into a path),
`configui.Scope` selects (the tab in the config editor), and
`configedit.Origin` attributes (which layer a resolved value came
from). Owned by `internal/configedit`, with the selection face in
`internal/configui`.

`mountplan.Origin` is a different subject under the same name and the same
user-facing column heading: it attributes a *mount* to the merge set it came
from (see [Mount Plan](#mount-plan)), which is set arithmetic over one file,
not a layer of the config. Each name is
right in its own package and neither is renamed; a reader tracing "where does
origin come from" starts here, and the answer is *which column* — the config
layer, or the mount's merge set.

Concretely: every writer command targets a Where; the config editor's
tab moves a Scope, and every save, reset and preview it performs is
resolved through the matching Where, never through a path of its own.
Not every key admits both sides of the axis: a key whose effect is
anchored to the workspace the container mounts has no coherent global
expression, and `configedit.WorkspaceOnlyKey` is the authority on which
ones those are — asked by the config editor before it opens a key for
editing and before its reset touches the artefacts a key owns, never
restated at either site. Rationale and rejected options:
[ADR 0011](docs/adr/0011-the-sdd-opt-in-is-anchored-to-the-workspace.md).

Why the term exists: the three faces were named independently and two
of them disagree on the word for the same side (`WhereLocal` vs
`ScopeRepo`), so nothing in the vocabulary said they were one axis —
and a key whose write surface must be narrower than the axis had
nowhere to say so. The gap that named it: `toolbox sdd init` joined its
own path from the current directory while the editor resolved one
through the Scope, so the two "identical" write paths targeted
different files, and a Global-scope enable wrote a flag applying
everywhere next to a fence applying to one repo. Naming the axis is
what makes "which face is this, and does this key admit both sides?" a
question with an owner. The identifiers keep their three names: a
rename is its own refactor, not a passenger on a behaviour fix.

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
closes over its arguments (`configedit.Scalar`, `Scalars`, `Bool`,
`StringList`, `StringMap`, `Shell`, `ShellEnv`, `Shells`, `RemoveShell`,
`Mount`, `MountDisabled`, `MountsDisabled`, `RemoveMount`,
`WorktreeSeed`, `SDDEnabled`, `Remove`). Owned by `internal/configedit`
(`mutate.go`).

The point of the term: a caller that must both *show* an edit and
*perform* it holds one object instead of describing the edit twice.

This is the package's whole edit vocabulary — a CLI writer command is a
named constructor plus one `ApplyChecked` call at the `cmd` edge, so
"describable" and "written" are the same value seen twice rather than two
families of functions.

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

The same defect had a second home, and the term is what let it be seen:
`configedit` went on describing the CLI's edits a second time, as
typed writers that took a path and wrote immediately. Same nodes, two
spellings — so the one rule stated in both (the mounts disable shape)
could drift, and the CLI's edits were unrenderable, which is to say no
writer command could grow a `--dry-run` without being ported first. They
were collapsed into this family: a writer command now names the mutation
and calls `ApplyChecked` itself, and `Shell` is the case that shows the
collapse is not flattening — `shells add --env` commits both halves of
one command or neither, so both halves are *one* mutation rather than two
calls at the edge.

That `--dry-run` then cost nothing is the term paying out. `Preview` is
`ApplyChecked` minus its final write — one shared body, so the file a dry
run prints is the file the write would commit and not a claim about it,
doctor verdict included: an edit the gate would refuse fails the dry run
with the same message. A writer command therefore reaches the disk
through a single lane at the `cmd` edge (`applyOrPreview`), which decides
write-or-show on the flag, and the flag is registered with `--where` in
one call so a writer added later cannot take the targeting and leave the
preview behind. The generality is the point: no command implements a
preview, because a mutation that can be rendered is one every surface can
already show — the config UI's diff and the CLI's dry run are two
readings of the same value.

### Key Descriptor

What `config ui` adds to a key's [Key Row](#key-row): the option sets, the
Pending Mutation constructor, and the display facts (collection noun, entry
names, where a scope file holds the key's entries, scalar hint). Owned by
`internal/configui` (`descriptor.go`), keyed by Config Schema key. The row
itself carries the editor kind and the typed readers that seed it, so which
editor a key opens is declared once, in `internal/config`, and read here.

Concretely: `displayValue`, `scopeDisplay`, `detailEntries`,
`enumOptions`, `hasEditorEscape`, `Model.openEditor` and
`Model.pendingMutator` read row and descriptor rather than switching on
the key themselves, so the editor a key opens and the mutation it writes
are the same declaration. `openEditor` reads the kind *once*: the row's
`Editor` indexes `editorSeeds`, one seed per kind, rather than a second
switch in the tea half re-deriving per key what the row already said. The
row declares which kind; the seed table lives with the kinds in `model.go`,
because what a kind opens with is bubbles widgets and the descriptor holds
presentation facts, not UI state. The per-key facts that are *not*
presentation stay where they belong and are asked, not restated: the value
shape and editor kind (the Key Row), which keys are attributed per entry
(`configedit.PerEntryKey`, read by `originFor`), what one layer's file itself
sets (`configedit.FileValues`, read by `scopeStates`), and how a deprecated
alias folds into its live key (`config.FoldDeprecatedAliases` — the one
implementation, called by the load path and by `FileValues` alike; `Keys`
asks `config.DeprecatedAliases` only to drop the alias rows). Nothing here
demands a row per key any more — the behavioural sweeps do: every key
displays something, every editable key opens a seeded editor, every open
editor has a Pending Mutation behind it, and a key the UI cannot edit fails
all three by name — the first two driven through `Update`, the seam
bubbletea crosses.

Why the term exists: `configui` was the last Config Schema consumer
whose omissions surfaced as a runtime status message instead of a red
test. The same key list was re-derived by ten switches across two files,
so a key missing from one of them rendered a blank row, listed no
entries, or reported "no interactive editor yet" — with a green suite.
Naming the per-key row turned "add a config key" into one row plus its
`TestPreviewMatchesWriterForEveryEditableKey` case; the Key Row then took
the half of that row which was schema rather than presentation, leaving
here the option sets (one of them, the default mount names, comes from
`mountplan` and so cannot live in config at all), the writers, and the
display facts.

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

### Session Intent

The typed description of a session to open — workspace, named shell, published
ports, loopback bridge, profile, peer opt-in, the optional worktree branch, and
the re-entry form — handed to `cmd.startSession`, the single composition root
for "open a session".

Concretely: `cmd.sessionIntent` + `cmd.startSession` (`cmd/session.go`). Cobra
keeps the flag globals; `runShell` resolves the `shell` flags and
`openWorktreeSession` its arguments into one intent, and everything either
command *does* with a session lives past that boundary — consume the
[Session Reload](#session-reload) handover, resolve the host once, resolve the
[Proximo Availability Gate](#proximo-availability-gate), migrate
legacy toolbox state, offer the bridge install tip, construct the Docker
client, resolve the running image's repo digest, call
[Session Plan](#session-plan), seed a [Worktree](#worktree) checkout, install
the signal handler and attach. The intent carries `sessionplan.PlanInput`
itself rather than a parallel copy of its fields — the caller fills its own
half, the assembly overwrites the fields it resolves — so the seam cannot drift
from the plan it feeds. The ordering that assembly must hold, and the tests
that pin it, are the container-runtime rule's half of this concept.

The intent is also the test surface: `startSession` is drivable from a value,
so what a `shell` invocation resolves its flags *into* is asserted directly
rather than only through the flag globals no test can set without mutating
them.

Why the term exists: `shell` and `worktree` were two composition roots for the
same act, and they had already diverged in four ways — the legacy-state
migration and the bridge tip ran on the `shell` path only, while `--profile`
and `--peer` had no worktree flag to resolve at all. Two of those were bugs (a
worktree session left the state relocation to whichever `toolbox shell` came
next, and a developer whose every session is a worktree session was never told
the forwarding they had enabled was not installed); the other two are
deliberate absences, and the intent is where they are now *declared* — a nil
`Profile` and a config-only `Peer` — rather than implied by a call site that
omitted a field. Each root's helpers were pure and directly
testable while the assembly calling them was reachable only by driving a cobra
command through its flag globals — which is the shape the name fixes, because
the bugs lived in *how* those helpers were called, and an ordering invariant
written as a comment on two call sites is enforced at neither.
There is no module here whose deletion would be invisible — the test is
inverted, because the complexity was already spread across two call sites in
two files, and the bar is that a third entry point adds a value, not a third
copy.

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

Concretely: `parsePublishSpecs → imageref.ResolveImage → mountplan.Plan
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
composition is what the Architecture Deepening milestone set out to build.

### Run Plan

The runtime decision step inside `container.Shell`: given a
`ContainerInspect` result, decide whether to connect to a running
container, start a stopped one, or create a fresh one. Pure function, no
Docker side-effects — the typed `Op` is dispatched at the Docker edge by
`lifecycle.go::dispatchOp`.

Concretely: `runplan.Compute(inspect, inspectErr) → Op{Action, ExistingID}`
with `Action ∈ {ActionConnect, ActionStart, ActionCreate}`. Owned by
`internal/runplan`. An `InspectResponse` with an empty `ID` and an errdefs
`NotFound` both route to `ActionCreate` so callers never dispatch on a
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

### Image Ref Identity

Which image reference a session resolves to, and which content digest the
local store holds for it — the ref half of the image family, with no build
context anywhere in it.

Concretely: `imageref.ResolveImage(image, registryMirror)` applies the
selection precedence (full `image` override > `registry_mirror` host swap >
`DefaultRegistryImage`), `imageref.SplitRegistryHost` is the one registry-host
heuristic, and `imageref.RepoDigest` / `imageref.LocalRepoDigest` are the one
spelling of *this ref's entry in a `RepoDigests` list* and *what the local
store answers for it*. `LocalRepoDigest`'s second return says the store
answered, which is not the same as carrying a digest: an image built locally
exists with none, the [Image Prefetch](#image-prefetch) abstains on exactly
that, and `restampImageDigest` stamps whatever the store says, empty included.
Owned by `internal/imageref`, a leaf on the stdlib plus the Docker client, so
[Image Plan](#image-plan), [Image Prefetch](#image-prefetch),
[Image Reclamation](#image-reclamation), `localimage` and `sessionplan` reach
the ref functions without reaching anything heavier.

Why the term exists: these four lived in `internal/build` — the package that
embeds the whole Docker build context and lints it — joined to the image
builder by a package name and nothing else. Four image modules therefore took
on an `embed.FS` and a Dockerfile-lint suite to reach a handful of ref
functions, and the ref tests were a minority in a suite about `apt-get`
ordering and shell shims. Naming the ref half separately is the same move
[Docker Identity](#docker-identity) makes at the Docker edge: a narrow concern
gets its own home, and its tests are then about it. `package build` keeps the
driver (`BuildImage`, `BuildOverlay`, `tarEmbeddedContext`), the embedded
assets, and the suite that pins the [Invalidation Floor](#invalidation-floor).

### Image Plan

The two-phase decision tree that guarantees the image referenced by a
`SessionPlan.Image` is ready before `ContainerCreate`.

Concretely: `imageplan.Sync(ctx, cli, image, stateDir, reason)` —
policy in, reason in, `Outcome` out. It runs at the top of
`container.Shell` and, on the [Session Reload](#session-reload) path,
before that reload destroys anything; it best-effort syncs the image
against its registry, steered by the Image's pull policy — `never` skips
the round-trip, `always` forces an unconditional pull, `auto` (default)
probes and then *asks*, see
[Start-up Refresh Prompt](#start-up-refresh-prompt); errors are
swallowed.

The `imageplan.Reason` is the caller's answer to *why is this sync
running*, and the only thing about the calling branch the tree learns.
`ReasonCreate` and `ReasonStart` are the two shell-start forms and differ
only in the [Prompt Stake](#prompt-stake) they imply; `ReasonReload` is
the one that asks nothing and confirms nothing — and the only one that
trusts the TTL cache, because a reload adopts what the store holds and
the [Image Prefetch](#image-prefetch) is what advanced it. There is
deliberately no second entry point for that: a silent form reachable by
naming a different function is a form a caller can reach by mistake,
whereas a reason is a value the branch already has.

Sync takes the session's resolved state dir, because that is
where the TTL marker lives: `<state dir>/pull-cache/<sha256-of-ref>`. It
used to be derived from `$HOME`, which pinned it to the *default* state
location while every other toolbox-managed marker followed a
`mounts_root` or `--profile` retarget — `imageprefetch` took `StateDir`
as a declared input already, `localimage` now takes it too, and
`docs/configuration.md` already described the pull cache as
mounts_root-aware. A session that resolves no state mount at all resolves
no cache either: one round-trip per invocation instead of one per TTL,
which is the honest cost of having disabled the mount, and better than a
cache the container cannot see. The overlay marker is the one deliberate
exception to *that* half, and only to that half: with no state mount it
falls back to the default state location under the overlay Dockerfile's
own root, because losing it costs not one extra check per shell but a
rebuild of the derived image on every shell for the life of the setting.

One TTL stayed outside: `imageprefetch`'s `probeTTL`, which paces the
background probe. Folding that package in is not available — `internal/container`
calls its `Start`, `ClearResult` and `Input` directly, so its deletion would
not be invisible the way the pull's was. Nor is handing the cadence in the way
`StateDir` is handed: the calls that read it (`AheadOfStore`, `Poll`) are
reached from `internal/container`, so a declared input would make *that*
package import this one for a constant to pass on — a dependency added to
relocate a number. What was available was the reason the two needed a
paragraph apiece to disambiguate: one of them was called `TTL`. Named
`probeTTL` against `pullTTL`, the prose that existed only to say which was
which is gone.

The pull itself is a file in this package, not a package of its own. Its
whole interface was two functions differing by one cache check, nothing
outside the owner ever called either, and the asymmetry between them —
one stamps a marker only the other reads — was invisible in both
signatures and cost three paragraphs to disambiguate from the prefetch's
own cadence. Folded in, the policy switch and the two forms it chooses
between read as one body, and the TTL and its cache are private state.

The pull policy steers the **synchronous** refresh only: the background
[Image Prefetch](#image-prefetch) reads the same key on a two-state
basis — on under `auto` and `always`, off under `never` — and keeps its
own cadence under both on-states.
`imageplan.Ensure(ctx, cli, image)` runs inside the `ActionCreate`
branch and is a hard guarantee: present in the local store → done;
otherwise fatal, because the pull already had its chance. A
[Session Reload](#session-reload) calls the same pair a second time and
earlier — before it destroys anything — which is what turns "no usable
image" from a spent session into a no-op. **`Ensure`
never builds** — `toolbox build` is the explicit user-driven path for a
local rebuild (the auto-build branch died with the local-hash image
tag). Owned by `internal/imageplan`.

Why the term exists: before this concept was named, the policy was
split — the TTL-cached refresh ran inline at the top of
`container.Shell` and a package-level `ensureImage` closure inside
`internal/container/lifecycle.go` covered the create-branch guarantee.
Reading either site alone missed half the contract ("when do we
rebuild?" lived only in the closure; "when do we refresh?" lived only
in the inline call). Tests of code that exercised the not-found branch
redeclared the same auto-build stub closure in every body. The "Image
Plan" name turns the two-phase policy into one named owner and the
create-branch guarantee into a single var inside `imageplan`.

### Run Image

The ref this session's *container* runs, as distinct from
`SessionPlan.Image`, the base it was planned from. They are the same ref
on an ordinary session and diverge whenever a `~/.toolbox/Dockerfile`
made `localimage.Ensure` build the derived `:local` overlay.

Concretely: `container.Shell` keeps the overlay's answer in a local
`runImage` and hands it to `dispatchOp` → `createAndStart`, which is the
whole of its reach — `imageplan.Ensure` and the `ContainerCreate` config.
Everything else reads `plan.Image`, which therefore means the base on
every line of the package: the [Image Prefetch](#image-prefetch) and
[Image Reclamation](#image-reclamation) track the ref that gains a
generation per merge, `restampImageDigest` stamps the digest that ref
carries, and both host-global halves of peer messaging — the
[Peer Anchor](#peer-anchor) and the socket-volume initialiser — are
created from it.

Why the term exists: the overlay's result used to be assigned back into
`plan.Image`, so the one field named the base above that line and the
overlay below it. Every reader that wanted the base back took a
corrective `base` parameter — except the peer-messaging pair,
`ensureAnchor` and `ensurePeerSocketVolume`, which created host-global
containers from whichever session's private overlay reached them first,
contradicting `ensureAnchor`'s own doc comment. It worked only because
the overlay is `FROM` the base. Naming the second ref instead of
overwriting the first removes the corrective parameters and the class of
mistake with them: a callee that reads the wrong ref can no longer do it
by reading the obvious name.

### Start-up Refresh Prompt

The one branch of the [Image Plan](#image-plan) that asks: a countdown
offering the download when the registry is ahead of the local store, and
treating a "no" as *later*.

Concretely, six settled cases and one question. An image missing from the
store is pulled without asking (there is no session to start otherwise),
`pull: always` pulls without asking (a policy that has already said yes on
every shell cannot coherently be asked again, and one about downloads has
said nothing about containers, so it recreates nothing either), `pull: never`
neither probes nor asks (not probing is that policy's whole promise, and a
probe is a round-trip), no tty neither asks nor probes (the default inverts to
start-now-fetch-behind: the interactive default is justified by the work that
follows the wait, and a script has no work that follows), a *running*
container is never asked about (`op.Action == ActionConnect` — Docker cannot
swap the image under it, and replacing it would end whatever else is attached;
the [Idle Reload](#idle-reload) is the accepted answer there), and a
[Session Reload](#session-reload) skips the act whole (it has already
refreshed and proved the image, and the same path is what an unattended
trigger walks). What is left — `auto`, image present, registry ahead, a tty,
and a container that is either absent or stopped — is the prompt:
`ui.ConfirmCountdown`, where `y` pulls synchronously and `n` starts the
session on what is already there.

What a yes costs beyond the wait is the [Prompt Stake](#prompt-stake), and it
is what selects the [Elapsed Answer](#elapsed-answer) — the two entries carry
that rule and why it inverts here. Honouring a yes is `imageplan.OutcomeAccepted`,
which is the answer *and* the pull that landed, and is deliberately distinct
from the derived `Outcome.Synced()` in both directions: a pull happens on cases
nobody was asked about, and a yes the registry could not honour would spend a
container on an image that never arrived. The settlements are one value rather
than a set of bools precisely so that "accepted but nothing downloaded" cannot
be spelled. `container.replaceForRefresh` then destroys the stopped container and
the branch becomes the `ActionCreate` that already pulls, creates and starts —
not the reload, which exists to replace a *live* session and carries a
handover payload there is nothing here to fill. Two rules guard the teardown.
**Everything that can fail runs first**: the pull (in `OutcomeAccepted`), the
`:local` overlay build, and the create's own port pre-flight, since no removal
can undo a host port another container holds — each of the three fails the
shell with the container intact. And **the container is read a second time**,
because the answer describes the moment it was given and the question held the
terminal for seconds: `runplan.Compute` on the fresh inspect decides, and *what
that read produced* — the inspect and the op together — is what the session
then acts on. So a sibling shell that started it meanwhile keeps it and this
session joins it by that second read's own record: the collateral connect is
never asked about, and a sibling that recreated the container rather than
starting it left the pre-prompt pair describing a container the daemon no
longer has — an ID that names nothing to start or exec into, and a creation
digest that would baseline this session's update banner on an image it is not
running. One already removed leaves the name free, and an unreadable answer
destroys nothing and learns nothing, so the start stays exactly the one the
question was put about. The removal itself is `removeAndWait`, shared with the
reload, and it is followed by
`imageprefetch.ClearResult` for the reload's own reason — the banner's cache
describes the container that was just replaced. The question owns the terminal in
raw mode for as long as it is asked, so a single `y` or `n` answers it on the
keystroke — a question with a countdown on it cannot also wait for a Return,
or the developer watches a clock they have already stopped. What is typed
behind that key is swallowed before stdin is let go, because the session
attaches to the same stdin a moment later and would otherwise receive the tail
of an answer as its first keystrokes. Raw mode also takes `ctrl+c` from the
terminal driver, so the prompt raises the interrupt itself **and reports it**:
declining this download and stopping the command behind it are different asks,
and `imageplan.OutcomeInterrupted` is what keeps the second from being recorded
as the first — a ctrl+c stamps no postponement, announces nothing, and the session is
abandoned rather than built, because whether the signal context has cancelled
by then is a matter of scheduling. The countdown is **visible**
because a few seconds of silence is indistinguishable from a hang, and a
developer who looks up to find a download running should be able to see why.

Two properties are load-bearing. **Knowing whether to ask is itself a
round-trip**, so it is answered from the prefetch's shared probe cache
whenever that stamp is warm (`imageprefetch.AheadOfStore`) — a sibling
session established the fact a moment ago, and re-establishing it would
reintroduce, one step higher, the latency this decision is about; only a
cold stamp probes, which is precisely when the answer is most likely to be
yes. That reuse has one boundary, carried by `StoreState.Probed`: a cached
answer may decide the *question* but must never be reported as a sync, or the
poller's attempt clock would be re-stamped from a cached digest on every
shell start and a developer opening shells faster than the TTL would stop
probing altogether. And **a "no" is a postponement**: `reload.TouchDeclined`
stamps the moment beside the [Reload Marker](#reload-marker), which is one of
the two origins of the [Session Quiescence](#session-quiescence) window and
what arms the [Idle Reload](#idle-reload) for that session alone. Nothing new
downloads on that path — the prefetch already runs an immediate pass when a
session opens, and a second fetch of the same ref at the same moment is what
a download started here would be; for the same reason `AheadOfStore`
deliberately does not stamp the attempt clock, since that clock is what gates
the pass which fetches the postponed bytes.

Why the term exists: the refresh used to be unconditional and cache-gated, so
the cost landed unevenly — a cold cache made the developer wait out a pull
they never asked for, a warm one gave them nothing back for the wait they did
not have, and either way the shell decided something the developer has an
opinion about. Naming the *prompt* separates the question from the act it
guards: the tree above is a set of answers that are already settled, and only
the case where none of them applies is worth a developer's attention. The
alternative readings and why they lost are in
`docs/adr/0005-prompted-image-refresh-on-shell-start.md`, and which branches
reach the question at all in
`docs/adr/0008-refresh-prompt-on-a-stopped-container.md` — which supersedes
0005 on that one clause, the create-only rule having rested on a
justification that was only ever true of a running container. Owned by
`internal/imageplan` (the tree), `internal/ui` (the countdown),
`internal/container` (which branch, and honouring a yes) and
`internal/imageprefetch` (the shared answer).

### Prompt Stake

What a yes to the [Start-up Refresh Prompt](#start-up-refresh-prompt) spends
besides the developer's time.

Concretely: it is no longer a value of its own but a property of the
[Image Plan](#image-plan)'s `Reason` — `ReasonStart` is the destructive one,
because a container already exists and a yes replaces it, discarding whatever
was written inside it outside the bind mounts; every other reason costs only
the wait. Derived rather than told: the caller knows which branch it is on,
and that a stopped container makes a yes destructive is the tree's own
conclusion, not something it may be handed wrongly. `Reason.offer()` returns the question, the
[Elapsed Answer](#elapsed-answer) and the postponement line as one value: the
three are one editorial decision, and a question worded around a container
that a clock could accept would be a bug on its own. A stake the method does
not know is worded as the download — the form that spends nothing but time.
A reason the method does not know is worded as the download — the form that
spends nothing but time, and the reading the zero value must get.
`container.offerRefresh` derives the reason and returns it alongside the
outcome, and that is the **only** place the branch is classified: honouring a
yes reads the reason rather than re-deriving from the [Run Plan](#run-plan)'s
`Op` what a yes meant. Owned by `internal/imageplan`; the branch that supplies
it by `internal/container`.

Why the term exists: while the prompt fired on a fresh create alone, what a
yes cost was a constant — a download — and needed no name. Extending the
question to a stopped container gave the same tree a second caller whose yes
also destroys something, and the choice was between a second case inside the
tree (which is about the registry and the store, and has no business knowing
which container branch it was reached from) or an input. Naming the stake is
what keeps the branch out of the tree while still letting the wording, the
countdown's default and the postponement line differ by branch. It later
stopped being an input, and then a type, and became what the `Reason` implies:
folding the silent form into one entry point gave the tree a reason to be told
anyway, and a two-valued relabelling of a three-valued reason was a middle man
between the branch and its wording. The decision
is `docs/adr/0008-refresh-prompt-on-a-stopped-container.md`.

### Elapsed Answer

The answer a countdown gives for a developer who is not looking, and the
default it shows on screen while it runs.

Concretely: `ui.Elapsed`, `ElapsedYes` or `ElapsedNo`, passed to
`ui.ConfirmCountdown` by the caller and selected by the
[Prompt Stake](#prompt-stake). It decides four things at once, which is why it
is one value and not a flag on the render: the window running out, a stdin
that cannot be read, a bare Return, and an answer nobody can parse — every
form of *nothing was said*. It also renders, as `[Y/n]` against `[y/N]`, so
the default is legible to the developer it will answer for. The rule it
encodes: **an unattended window may answer only what the caller would have
done anyway.** A download the shell would once have started unconditionally
qualifies; discarding a container never does. Owned by `internal/ui`.

Why the term exists: the countdown answered yes on every uncertainty, and the
justification lived in a comment about its one caller — the caller only asks
when it is about to do something it would otherwise have done unconditionally.
A second caller, whose yes also destroys a container, made that comment true
of one site and false of the other. Naming the default turns it from a
property of the prompt into a decision each caller has to make and be read
against, and puts the rule where the type is rather than beside one call.

### Image Prefetch

The single host-side detector that answers "is there a newer runtime
image or CLI, and are its bytes already here?" for as long as a shell is
attached — and downloads them when they are not. The separation it names is
*when the bytes arrive* from *when a session moves onto them*, which is the
design rather than an implementation detail.

Concretely: `imageprefetch.Start(ctx, cli, Input{Ref, ContainerDigest,
StateDir, StartSynced})`, launched from `container.Shell` behind the
`startPrefetch` var and cancelled with the session. Its ticker is only an
**alarm**: the "poll now?" decision is a `stat` on an attempt stamp
(`<state>/update-check.stamp`), so the cadence lives on the state mount,
is shared across sibling sessions, and would survive a re-exec of the
host CLI for free. Each tick **cancels the poll it started last time**
before starting the next, so a registry that accepts the connection and
then stops talking costs one tick rather than the whole session — the
bound that lets the act refuse backoff and metering entirely. Cancelling
is free: a partial ingest is never a blob, it expires on its own, and the
next pull resumes from what landed.

`StartSynced` closes the cold start. The refresh at shell start is itself
a probe, so when it established the store to be current *here and now* — a
pull that landed, or a live probe that found it current already, never an
answer read from the shared cache — it takes that TTL's
turn: the poller stamps on its behalf and publishes the banner **from the
local store** instead of asking the registry the same question seconds
later. A failed probe, a failed pull and a declined download set nothing,
and the poller does its own probe: none of them left the store provably
current.

**The gate stops the registry, not the banner.** One state mount serves
every workspace, so the published result is whichever session wrote it
last — and `image_update` is computed against *that* session's
container. A sibling already on the new image publishes a `0` that is
true only for it, and keeps warm the very stamp that holds the gate
shut; a session whose own container is older would render the sibling's
answer for as long as that lasts. So every poll turned away at the gate
still publishes from the store: the session axis is a local comparison
and owes the registry nothing. Every pass, not just the first — a
session outlives many ticks, and fixing only the first would hand the
sibling all of them. It
restates that axis and nothing else — `image_latest` is the *registry's*
digest, which `knownRemote` reads back for `AheadOfStore`, and
`image_state`'s `unavailable` rides a first-failure clock that a
groundless "the store is current" would reset forever. On the connect
branch, where the start-up refresh is never offered, that publish is what
keeps the banner a channel at all.

One poll is probe → prefetch → publish:
`DistributionInspect` resolves the remote digest through the daemon
(so a `registry_mirror` is honoured and no registry HTTP lives in this
repo), `ImagePull` drained with `ImagePullResponse.Wait` fetches it when
the local store's `RepoDigests` entry differs, and the result is written
to `<state>/update-check` — the cache the in-container zsh `precmd` hook
has always rendered. Everything is silent: the host process's stdout is
the attached tty. The cache contract is **additive**: `image_state`
(`none|ready|unavailable`) is written *alongside* a retained
`image_update`, so an image predating the field still renders its own
true sentence instead of going quiet. `unavailable` is earned, not
reported on sight — a first-failure timestamp beside the attempt stamp
must be a full cadence old before the word is used, because one failed
download is a dropped connection and not a broken registry.

**Two comparisons, not one.** *Remote vs local store* decides whether to
pull; *local store vs the digest the container was created from* decides
whether the session is behind, which is the fact the banner states. They
diverge exactly at the moment that matters — right after a successful
prefetch the first says no and the second says yes. The second baseline
is read off the running container (`sessionplan.ImageDigestEnv`), never
recomputed, so it stays true on the connect path. Two refusals: `pull:
never` silences probe, prefetch and banner as one act (a probe talks to
the registry), and the prefetch abstains while the resolved ref has no
repo digest — the fingerprint of a local `toolbox build`, so an explicit
act by the developer is never undone by an automatic one.

Why the term exists: detection used to be a baked helper
(`bin/toolbox-update-check`) polling GHCR with `curl` from inside the
container, while only the host could act on what it found. Two
detectors over two transports could disagree with nobody to reconcile
them, the in-container one was `precmd`-driven so a shell left at a
prompt never re-polled — the multi-day session, which is the case that
matters — and it could not know whether the bytes had landed, because it
spoke to a registry and not to the local content store. Collapsing
detection and prefetch into one host-side act is what lets the banner
state a fact instead of prescribing an exit. Owned by
`internal/imageprefetch`; the render half stays in `zshrc.sh`, because
the image owns the words.

### Superseded Image

An image in the local store that carries a `RepoDigests` entry for the
toolbox repo the config resolves to and **no** `RepoTags`: this CLI
pulled it, and a later move of `latest` took its name away.

Concretely: the selection predicate of Image Reclamation, read off
`ImageList`. The repo constraint is what keeps the act inside its own
perimeter — an image this project never pulled carries no digest for
this repo and is therefore never a candidate, whatever else is true of
it. The digest the current session runs is excluded by name rather than
left to inference: a config that pins `image:` to a digest instead of a
tag produces a running image with no tags at all, so the predicate on
its own would nominate the very image the shell just started from.

Why the term exists: the obvious word for these is *dangling*, and it is
not merely imprecise but false. Docker's `dangling=true` filter does not
match them, because losing a tag leaves the repo digest behind — an image
is dangling only when it has neither. A reader who believes these are
dangling images concludes `ImagesPrune` covers the case, writes three
lines instead of a package, and reclaims nothing; the same filter would
meanwhile sweep images belonging to other projects on the machine. The
name exists to put the wrong word out of reach.

### Image Reclamation

The opportunistic sweep that removes Superseded Images once the session
is already anchored to the new one. What it names is a contract, not a
mechanism: **the daemon is the arbiter of use** — the sweep asks, and a
refusal is an answer rather than a failure.

Concretely: `imagereclaim.Start(ctx, cli, Input{Ref, KeepDigest})`,
launched from `container.Shell` behind the `reclaimImages` var and
cancelled with the session — after `dispatchOp`, never before. The
ordering is the design and not an optimisation: only once this
workspace's container exists and references the new image is every
surviving reference to the old one somebody else's real reference. Run
any earlier and the removal is guaranteed to be refused, because the
session doing the reclaiming is itself the last holder. `ImageRemove`
runs with neither `force` nor `PruneChildren`; the summary line appears
only when something was actually removed, and a refusal says nothing at
all. Cancellation is safe because the act is idempotent — a candidate
the sweep did not reach is still a candidate at the next shell. Gated by
`image_reclaim`, a tri-state `*bool` like `bridge` and `proximo`: the
act runs unless the developer disabled it in so many words, and an
absent key must stay distinguishable from a written `false` or a
merged config layer would silently re-arm it.

**Zero generations are retained.** Every Superseded Image is a
candidate, with no keep-the-previous-one rule and no grace window. The
only use for a retained generation would be rolling back onto it, and
no such path exists — Session Reload moves forward only. A grace window
would not help either: the single real race is a sibling session that
resolved the old digest and is creating its container right now, and
that is measured in milliseconds, so an hours-long window would hold
gigabytes for a window it does not cover.

Why the term exists: *prune* was the available word and carries the
wrong scope, because `docker system prune` is a sweep of the machine and
this is a sweep of what one CLI downloaded. Naming the act apart from
Superseded Image also splits two readers: the predicate is read by
whoever changes what counts as a candidate, the daemon contract by
whoever changes how one is removed. Why no container census stands in
front of the removal, and what that costs, is
[ADR 0007](docs/adr/0007-daemon-refusal-as-in-use-check.md).

### Session Reload

Moving an attached session onto a newer runtime image, on the
developer's word, without exiting and reopening by hand. Process
continuity is not preserved; **conversation** continuity is — history,
agent transcripts and credentials survive because they already sit on
bind mounts, not because anything copies them.

Concretely, a reload is a **tail call**, not a loop. `container.Shell`
returns a typed `*reload.From` when the exiting shell left a
[Reload Marker](#reload-marker), `cmd.runSession` performs the
`syscall.Exec` behind the `execSelf` var, and each host process still
handles exactly one attach. The order is the whole safety argument:
**re-exec first, verify, then destroy.** The riskiest step is therefore
also the first, when the old session is still alive; `imageplan.Sync`
(under `ReasonReload`) + `Ensure` gate the teardown, so a reload with no usable image leaves
the developer exactly where they were. The new binary owns the destroy,
which means a `brew upgrade` landed meanwhile takes effect on the
teardown policy too, not just on the create.

`TOOLBOX_RELOAD_FROM` is the handover across the exec: one JSON
variable, consumed and unset by `reload.Take()` before any container env
is built. Every field is optional with a safe zero value **except the
container name** — lose that and nothing destroys the old container, the
next `toolbox shell` resolves the same deterministic name and reuses it,
and the developer lands silently back on the old image. So an
unparseable payload is a hard error printing the re-entry command, never
a degrade. One item is carried deliberately, the working directory
(`sessionplan.reloadWorkingDir`, validated against the workspace, silent
fallback); everything else is **re-derived** by a fresh
`sessionplan.Plan`, so the `TOOLBOX_*` identity is right for the *new*
image instead of replaying the old one's `PATH` into it.

The **re-entry form** is the argv the next process runs, and it is
*normalised, never replayed*: `worktree create` comes back as `worktree
open <branch>` with the resolved `--agent` pinned, and `shell` drops the
`--create`/`--path` bootstrap half. Everything else the developer typed
is carried, because the flags are identity: `--profile` and `--peer`
feed the container name, `--profile` also moves the mount root, and
`-p` fixes the port bindings at creation — a form that dropped them
would have the reloaded process destroy the container the payload names
and then create a *different* one. `cmd.reentryFlags` walks the Changed
flags rather than a hand-kept list, so a flag added later is carried
without anyone remembering; a flag left at its default is never emitted,
which is what keeps the tri-state `--peer` resolving against config.
The same form is what a failed reload prints as the way back.

The reload gates on nothing and confirms nothing. It looks once
(`ContainerTop`, cgroup-scoped even under the shared peer PID namespace)
and prints what it killed as part of a before/after summary, because a
dev server or a watcher is the normal state of a working shell and a
prompt there would fire on nearly every reload. The summary is not
cosmetic: the command **always** reloads, including when nothing is
newer, so it is the only thing distinguishing a successful-but-pointless
reload from one that failed silently. Sibling attached panes die with
the container — see [Teardown](#teardown).

Why the term exists: the alternative reading was "auto-update", which
put the decision on the tool and the surprise on the developer, and
invited an in-place mutation of the live container that would cover
barely half the update surface while diverging the container from its
image digest. Naming the act *reload* puts the moment in the
developer's hands and keeps one mechanism instead of two. Owned by
`internal/reload` (the two names and the payload), `internal/container`
(the destructive half), and `cmd` (the exec).

### Reload Marker

The file an exiting shell writes to ask the host for a
[Session Reload](#session-reload), and the host-injected variable that
declares the host is able to read it.

Concretely: `TOOLBOX_RELOAD_MARKER` carries the marker's absolute path
into the container (`sessionplan.reloadMarkerEnv`, under the state
mount, named after the container); the `toolbox-reload` zsh function
writes `$PWD` there atomically and exits; `container.Shell` reads and
**deletes** it exactly where `execShell` returns, which is where the
teardown decision was already being made. Deleting on read is what stops
a marker orphaned by a crashed session from firing later.

Two properties are load-bearing and easy to lose. **Presence is the
capability**: the image ships on merge and the CLI on tag, so a new
image can meet an old CLI that would write nothing, notice nothing and
tear the session down for good — `toolbox-reload` therefore refuses *at
the prompt, without exiting*, naming no required version because a
presence marker means the image never learns one. And **the value is a
path**, not a boolean, because the container cannot build the path: it
would need the state mount's target, the naming convention and its own
container name, and the hostname it can read is Docker's short id.

The format has two writers and only one of them runs in production: the
zsh function writes it, while `reload.WriteMarker` is the writer the Go
tests driving the reload path use. Keeping a second spelling is the
cheaper trade — those tests would otherwise each hand-roll the bytes —
but only because the equality is executed rather than asserted in a doc
comment: `TestReloadMarkerWriterMatchesGo` runs the shipped function and
compares. A drift is silent in the worst way, since a marker the host
cannot parse leaves the old container holding the name the next
`toolbox shell` resolves to.

It earns its own entry only because two host-injected names share a
prefix and point in opposite directions: `TOOLBOX_RELOAD_MARKER` travels
host → container and never leaves it, `TOOLBOX_RELOAD_FROM` travels
host → host across the exec and never enters one. Owned by
`internal/reload`; bound to the shell side by `TestReloadMarkerContract`
(the names) and the writer tests above (the bytes).

### Idle Reload

A [Session Reload](#session-reload) nobody typed: the shell asks for it on
the developer's behalf once the session is provably not in the middle of
anything.

**Decided, not yet built.** The decision is
`docs/adr/0006-idle-reload-onto-a-newer-image.md`; what ships today is only
the producer this entry names last — the decline stamp written by the
[Start-up Refresh Prompt](#start-up-refresh-prompt). The trigger, the
`update.idle_reload` key and the clauses below describe the target, and the
entry stays here so the term means one thing while it is being built.

Concretely: the same act as the typed one, with a different cause. The
`precmd` hook in `zshrc.sh` writes the [Reload Marker](#reload-marker) and
exits exactly as `toolbox-reload` would, so the host side — teardown,
create, re-exec, re-entry form — is reached through one mechanism and not
two. It fires only while [Session Quiescence](#session-quiescence) holds,
and only when armed: either `update.idle_reload` is set, or the developer
declined the start-up refresh prompt, which arms it for that session alone
because *not now* is a request to postpone rather than to refuse.
`TOOLBOX_NO_UPDATE_CHECK` disarms it along with the probe and the banner —
one umbrella, since a session told to say nothing about updates must not
recreate itself either. Abstention is spoken, not silent: the banner names
the single clause that failed, in the fixed order sibling → window → job,
so an automation that is waiting cannot be mistaken for one that is broken.

The typed command is not part of this and survives every switch above,
because it reloads onto whatever the store holds — useful after a local
`toolbox build`, where no update check is involved at all. It earns its own
entry because the reload's own documented rule is that it *gates on nothing
and confirms nothing*: that rule holds precisely because a human typed it,
and an automatic trigger is the one thing that could quietly void its
premise. Owned by `zshrc.sh` (the trigger) and `internal/reload` (the act).

### Session Quiescence

The predicate that authorises an [Idle Reload](#idle-reload) — the property
of a session that can be destroyed and rebuilt without taking work with it.

**Decided, not yet built**, with its trigger — see the note under
[Idle Reload](#idle-reload). Of the five clauses, only the window's second
origin exists today: the decline stamp of the
[Start-up Refresh Prompt](#start-up-refresh-prompt).

Concretely, five clauses, all evaluated in the `precmd` hook: the shell is
at the prompt; zsh's `jobs` is empty; exactly one interactive shell holds a
tty inside the container; the reload window has elapsed; and
`TOOLBOX_RELOAD_MARKER` is present. The sibling clause is load-bearing
rather than cautious — attached panes die with the container, so an
unattended reload would end a session its owner never volunteered. The
window has a single origin, the most recent of the container's own age
(PID 1, which *is* the session) and the moment a start-up prompt was
declined; one clock and one constant, because both express the same fact —
this session has already had its turn recently. Two of the clauses are
deliberately shallow: a detached `nohup` is not a job and will not hold the
reload back, and that limit is documented rather than patched with a
process heuristic that would misfire in both directions.

It is named for the property and not the mechanism — the sibling of
[Invalidation Floor](#invalidation-floor) in that respect — because the
mechanism is the part most likely to be replaced, and every clause is a
decision about what "safe to destroy" means rather than about how zsh
happens to detect it. Owned by `zshrc.sh`.

### Invalidation Floor

The highest layer a change touches, and therefore the boundary below
which every layer is rebuilt with a fresh digest — the build-side
counterpart to the Image Plan's host-side pull policy. It is what a
change actually costs the people pulling the image, measured in
transferred bytes, not in lines of Dockerfile edited.

Concretely: `COPY --link` raises the floor, because such a layer is
built independently of the filesystem beneath it and neither depends on
what precedes it nor invalidates the COPYs beside it. Any `RUN` that
*consumes* a copied file lowers the floor back down for that COPY: the
COPY must be declared above the RUN, so bumping it re-runs the whole
tail. The rule that follows: the ordering that matters is not
rare→frequent among the `RUN`s, it is how few consumers each COPY has
below it.

Why the term exists: the Dockerfile's build-strategy header claimed a
Renovate bump of one tool "re-runs only that stage + its COPY — never
the tail". Measured against the published manifests, a one-line version
bump moved half the image, because the `COPY --link` declarations sat
above the entire `RUN` tail. The claim was true of the COPYs relative to
each other and false of the tail, and no single word existed to say which
of the two a given edit was about. The figures live in
`docs/adr/0002-layer-ordering-by-invalidation-floor.md` — this entry
defines the term, not the incident.

### Archive Drift

A layer whose bytes change because an unpinned upstream package archive
published something new, with no edit on our side. Not an Invalidation
Floor: the floor is a positional property of the Dockerfile, this is a
content property of an input we do not control.

Concretely: the final stage's `apt-get install` is not version-pinned, so
a Debian archive update gives that layer a fresh digest. Every layer
built on top of it in the same stage is then rebuilt too, and the
`fetch-*` stages, which share `fetch-base`, re-execute for the same
reason. One archive update therefore moves the base layer plus the whole
parent-chained tail beneath it.

Why the term exists: the layer-count gate could not tell this apart from
the regression it was built to catch, because both show up as "many
substantial layers moved". Measured on `sha-2fe107a` to `sha-14bb4c0`,
whose only image-affecting edit was a one-line `GCLOUD_VERSION` bump, 12
of the 16 moved layers above 1 MB — 587 MB of 639 — were archive drift.
The gate now excuses them: see
`docs/adr/0002-layer-ordering-by-invalidation-floor.md`.

### Fetch Nondeterminism

A `fetch-*` stage whose `/out` is not a function of the version it pins,
so its `COPY --link` layer comes back with a fresh digest whenever the
stage re-executes. Distinct from Archive Drift, which moves layers the
stage sits on rather than the stage's own output.

`freeze-mtimes` normalises timestamps, which is what makes a stage that
unpacks a release artefact reproducible, but it cannot normalise
content. The two that clone git repositories instead —
`fetch-omz` and `fetch-brew` — need `freeze-git` as well, and it is the
answer to this term rather than a second convenience: it makes a
checkout a function of the commit it pins by recompressing the pack
locally (what upload-pack sends is the server's choice, and it varies),
dropping the reflog, and reading the index back from `HEAD` with its
stat cache zeroed. Neither stage can simply drop `.git`: Homebrew *is* a
git checkout, and `omz update` needs one.

`rtk-builder` belongs here on arm64 and not on amd64, and the two are
worth separating. On amd64 its binary comes from a checksummed tarball
and is identical across builds; its layer moved because the COPY named a
single file, and a `--link` layer stamps the destination directories it
has to synthesise with the build clock — a defect of the copy, not of
the stage's output. On arm64 the binary is compiled, and a base image
tag that floats is enough to make `/out` stop being a function of
`RTK_VERSION`: the same version and the same `--locked` dependency graph
built on two toolchains produce two different binaries. That is this
term and not Archive Drift, because what moves is the stage's own output
rather than the layers it sits on — the base image here is not a host
for a download, it is compiled *into* the result. Holding the base still
would close it, and is deliberately not done: the moved-layer gate
measures amd64 only, so the pin would cost a digest PR on every upstream
rebuild and buy nothing anything measures. Accepted at the stage, not
excused in the gate.

The cost of any of this stays invisible while BuildKit reuses the stage
and appears in full whenever anything invalidates it — an archive
update, or a lost build cache.

Why the term exists: `COPY --link` is what buys a bump the price of one
layer, and that guarantee is worth exactly as much as the reproducibility
of the stage behind it. Two stages did not hold up their end, and the gap
had no name because the ADR that introduced the ordering had only
measured mtimes.

### Declared Docker Surface

Every module that talks to the Docker daemon declares, unexported in its
own package, an interface holding exactly the daemon methods it calls —
named for the role the daemon plays there rather than for Docker.

Concretely: `imageplan.registry` (`ImagePull`), `imagereclaim.imageStore`
(`ImageList` + `ImageRemove`), `localimage.overlayBuilder`,
`imageprefetch.registryStore`, `imageplan.imageSource`,
`teardown.containerRuntime` (the container it inspects, stops, removes or
kills, plus the execs it asks about), `imageref.localStore` (the one
`ImageInspect` behind `LocalRepoDigest`) and the `imageBuilder` that
`internal/build`'s two build functions share. No one package owns the
concept: each module owns the interface it declares, and
`internal/dockertest` owns the shared half — the double all of them are
tested through. Exported functions take these
unexported types: a caller passes a value that satisfies one and never
needs to name it. Go assigns interface to interface only when the
target's method set is a subset of the source's, so a module's declared
surface is the union of its own calls and those of every callee it hands
the value to — which is what pulled the build driver's two image
functions into the same slice as `imageprefetch` and `localimage`.

`internal/container` is exempt and keeps `client.APIClient`. It *is* the
Docker edge, it calls the daemon directly on many endpoints and passes
its client down to most of the leaves, so its union would be a large
fraction of the SDK for no depth — and would have to be re-checked every
time a call into a leaf is added, with the compile error landing in the
package that did not change. The three seam vars it holds into the image
family (`startPrefetch`, `reclaimImages`, `refreshAtStart`) are therefore
wrappers rather than plain assignments: a bare `var x = leaf.F` would
take the type of an interface no other package can spell, and no test
there could write a stub for it.

`internal/worktree` keeps `client.APIClient` for the same reason, one
level removed: it hands its client to `container.Stop`, so its own
declared surface would have to be a superset of that parameter, which is
the edge's. Both packages therefore keep a hand-rolled adapter in their
tests, and will as long as the edge holds the concrete client —
`dockertest.Fake` cannot stand in where the parameter is
`client.APIClient`, by the same design that makes it useful everywhere
else.

The shared test double is `dockertest.Fake`: one function field per
method the narrowed modules call, a nil field panicking with the method's
name, and deliberately **no** embedded `client.APIClient`. Embedding the
SDK interface would satisfy every narrow interface in the tree by
accident and undo the narrowing in exactly the place it was bought —
`TestFakeIsNotAnAPIClient` is the guard. The panic is load-bearing:
several assertions in the image family are assertions of *absence* (the
registry is asked nothing while the attempt stamp is fresh; `ImageRemove`
takes neither `force` nor `PruneChildren`) and are spelled as "no stub,
therefore no call".

Why the term exists: before it, which daemon calls a module makes — in
what order, with which options — was carried in prose and nowhere in a
signature, because every module declared the whole SDK surface and called
a handful of methods. The seam was real (a live client on one side, a
hand-rolled adapter per test package on the other) and merely declared at
the wrong width; each package's copy of the same fake was the visible
cost. `internal/dockertest` states the concept's shared half, the
interfaces state the per-module half, and the name says what is being
declared and by whom. Reasoning and consequences: [ADR
0010](docs/adr/0010-each-module-declares-the-docker-methods-it-calls.md).
The two packages still on
`client.APIClient` are the two that cannot leave it, not a remainder
waiting for a later slice.

### Declared Host

`fsx.Host` — the ambient host facts a run depends on, resolved once at the
`cmd` edge by `fsx.CurrentHost()` and passed down as a value: `Home` (the
directory every `~/.toolbox` path hangs off) and `LookPath` (how a host
binary is found). Threaded through the seams that already carried the
session's inputs — `mountplan.PlanInput`, `sessionplan.PlanInput`,
`bridge.DaemonOptions` — plus the exported functions behind them
(`mountplan.Merge` / `Classify` / `Names` / `StateDirPath` /
`OverlayDockerfilePath`, `proximo.CAPath` / `Resolve`,
`bridge.ResolveHostState` / `NewAgent` / `Install` / `Uninstall` / `Status`,
`configedit.Doctor`).

Neither field falls back to the process, and that is the whole
discipline. `Home` is a plain string with no default, so a zero-valued
`Host` is a visible bug rather than a silent read of the real home:
`Host.Validate` re-states at the seam the strictness `fsx.Home` enforces
on the ambient read, and every entry point that turns the home into a
path calls it. `LookPath` may be nil, but nil means *this host resolves
no binaries* — not "ask the process PATH". The convenience fallback was
written first and removed: it reinstated the ambient read in the one
place nothing would notice, since a caller that declared only a home
would keep probing the real machine and a test written against it would
pass for whatever happened to be installed. `Expand` and `Join` are the
two path shapes the threading actually needed; nothing else lives on the
type.

Why the term exists: `$HOME` and PATH were inputs no signature mentioned.
A package read them wherever it happened to need them, which meant a test
could only choose them by mutating the process — `t.Setenv("HOME", …)`
before calling a planner, a scrubbed PATH before probing for a binary.
That is a global write, so no test that did it could run in parallel with
one that read it, and the read itself was invisible at the call site: you
could not tell from `Plan(in)` that it would resolve a home, nor from
`Merge(cfg, nil)` that it would stat under one. Making the host a declared
value moves both facts into the signature, and the tests that used to
rewrite the process now construct the host they mean — which is also what
makes them deterministic where the ambient answer was not (this project's
own image ships a real `proximo` on PATH, so a PATH scrub never proved the
not-installed branch was reachable).

`mountplan.Merge` is the one entry point that takes a `Host` without
validating it. Two things behind it read the home — `inherit_host_auth`'s
pre-stat and the `~/.proximo` fallback behind `proximo.Resolve` — and both degrade
when it is empty rather than fail, which is what the discarded
`os.UserHomeDir()` error used to give them. The read-only `cmd` surfaces
that reach it (`mounts list`, `mounts disable`'s validation, `config
doctor`) keep that tolerance through `hostBestEffort`; the writers behind
them still fail loud on their own.

Not everything is threaded yet. `configio.GlobalConfigPath` still
resolves its own home, and `configedit`'s write gate calls
`fsx.CurrentHost()` at one named seam rather than threading a Host
through every `ApplyChecked` call site and the `configui` model — one named
read where the lints behind it previously took two unnamed ones. Their
callers reach them through packages this concept has not crossed; until
they do, a test that exercises those paths still sandboxes `$HOME`.

The pull cache was on that list and came off it differently: its marker
never wanted a home, it wanted the session's resolved state dir, which
`SessionPlan.StateDir` already carries. Taking that instead removed the
ambient read *and* put the cache where `docs/configuration.md` always
said it was — mounts_root-aware, beside the overlay marker, which then
took the same input rather than re-deriving the root from a sibling path.
See the [Image Plan](#image-plan) entry.

That was the last ambient home read on `internal/container`'s path, so
the helper that used to set the process `$HOME` *and* return a matching
Host is now plainly `planHost`, and the per-test guards are gone. What
keeps them gone is that package's `TestMain`: it points `$HOME` at an
empty directory and fails the run if anything lands in it, so a
regression to an ambient read is a failing gate rather than a quiet write
into a home nobody inspects.

### Docker Identity

The host-process → container-identity translation at the Docker edge:
the `"<uid>:<gid>"` user spec passed to `ContainerCreate` and the
supplementary group IDs needed for the runtime user to talk to a
bind-mounted `/var/run/docker.sock`.

Concretely: `dockeridentity.Resolve(bindTargets) → Identity{UserSpec, GroupAdd}`.
Owned by `internal/dockeridentity`. The single seam `container.Shell`
calls before `ContainerCreate`. `Identity.UserSpec` is built from
`os.Getuid` / `os.Getgid`; `Identity.GroupAdd` is nil unless
`/var/run/docker.sock` is in the bind set, in which case it joins gid 0
(Docker Desktop reprojects the socket as root:root) plus the host
socket GID (Linux: usually the `docker` group). The package-level
`statSockGID` var is the test seam for simulating both deployment
modes. The parameter is the in-container target paths, not
`[]mountplan.Bind`: the caller reads `b.Target` off its typed binds, so a
renamed field still breaks the build, while this leaf keeps its
stdlib-only dependency set (`mountplan` would drag in `config`, `fsx`,
`proximo`). The cost is a second copy of the `/var/run/docker.sock`
literal — pinned to `mountplan`'s **default** mount set by a bijection
test that imports `mountplan` from the test file only (a user `mounts:`
patch retargeting `docker-sock` is outside that pin). Session Plan deliberately does NOT
encode this concept (host
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

One caller deliberately does **not** use this policy: a
[Session Reload](#session-reload) needs an unconditional destroy that
waits for the removal, because skip-if-sibling would spare a container
still holding the deterministic name the reload's own create is about
to ask for. Considerate refusal there turns into a name collision, so
the reload owns `container.removeAndWait` instead — shared since with
the [Start-up Refresh Prompt](#start-up-refresh-prompt)'s recreate,
which destroys a stopped container for the same reason and names
itself in the error through the same `act` argument.

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

### Bridge Run Mount

The one **read-write** bridge bind: `~/.toolbox/toolbox/bridge/run` →
`ContainerRunDir` (`mountplan.defaults`, mount name `bridge-run`), nested
inside the read-only state-dir bind so the shim can reach `run/bridge.sock`
without the token and port files becoming writable.

Concretely: it is live for as long as a bridge-enabled toolbox shell is open
(`bridge: false` drops all three bridge binds), and on a Docker Desktop host
that makes the state dir undeletable. `toolbox bridge
uninstall` therefore treats `os.RemoveAll` on the state dir as best-effort
(`bridge.stateDirOutcome`, `TestStateDirOutcome`): a warning naming the path
and the remedy, exit 0 — the daemon and its service file, the half that cannot
be undone, are already gone by then. A surviving `token` file is the one
exception and still fails hard, uninstall+install being the documented token
rotation, and the removal fails closed there: a token whose absence cannot be
proven counts as live.

Why the term exists: the failure reads as a permission bug, not as a mount.
Docker Desktop serves the bind over virtiofs, which answers the unlink with
`EACCES` — "permission denied" on a directory the user owns. A native-Linux
host does not fail at all: the bind only exists in the container's mount
namespace, so the host directory unlinks clean and the open shells keep the
orphaned inode until they exit. Naming the mount puts the cause in the
vocabulary, so the next reader of that message looks for an open shell instead
of for a `chmod`, and does not go hunting on Linux for a warning that host
cannot produce.

### Sound Handoff

The container→host transfer of a sound to play: the bridge's `/sound` route,
carrying the MP3's **content** rather than a path to it.

Concretely: herdr runs in the container and spawns an external player; the
image has none, so `internal/build/assets/bin/paplay` reads the file herdr just
wrote and POSTs its bytes base64-encoded, and `bridge.playSound` writes them to
a temp file the **daemon** names before spawning the host's player detached
(`afplay` on macOS, the first installed entry of `soundPlayers` on Linux). The
response is `200` before playback starts: nothing waits for a chime, and two
completions moments apart overlap instead of queueing. Decided in
[ADR 0009](docs/adr/0009-sound-handoff-through-the-bridge.md).

Why the term exists: "play a sound on the host" hides the one decision that
matters, which is *what the request is allowed to name*. A caller-supplied host
path is precisely what `internal/bridge/allowlist.go` refuses for `/open`, and
what [ADR 0004](docs/adr/0004-proximo-full-surface-through-the-bridge.md) had to
justify for `/proximo` — there the daemon cannot derive the path, here it can,
so it does. Naming the handoff after what it carries keeps the next route from
reintroducing the path axis by habit, and puts the residual risk where it
actually is: on the content, where a hostile container process can make the
host play arbitrary audio, which is annoyance under a ceiling the bridge token
already sets.

### Probed Shim

An image shim that answers to a name **another tool probes for** — as opposed
to a name a human or a script calls.

Concretely: `internal/build/assets/bin/paplay` is the only one. herdr walks a
fixed chain of player names and spawns the first that exists, so the shim's
*name* is what selects it; taking the first name in the chain costs no failed
`exec`, while a later one would burn one per skipped name, invisibly. Nothing
in this tree calls it, and nothing should: its caller is upstream's probe.

Why the term exists: every other shim in the image shadows a binary someone
types (`xdg-open`, `code`, `proximo`) or that git resolves by configuration
(`git-credential-toolbox`), so "find the callers" is how a reader establishes
what a shim is for. Here that search returns nothing, which reads as dead code
— and a rename or a deletion would be silently correct at review time and
silently broken at runtime, since herdr logs only when *all* the names fail.
The term also carries the honesty cost the name itself imposes: `paplay` never
speaks to PulseAudio, and the mitigation is the reason in the file header plus
the fact that PulseAudio is not in the image and is not coming.

### Proximo Execution Modes

The two ways the bridge daemon runs the **host** proximo binary on behalf of a
container-side request: *plain*, and *with the agent home rewritten*. Decided in
[ADR-0004](docs/adr/0004-proximo-full-surface-through-the-bridge.md).

Concretely: plain execution covers every verb whose effect is on the host or is
pure output — `up`, `down`, `status`, `errors` — and is just the resolved binary
with the request's argv. Home-rewritten execution exists for exactly one verb,
`skill`, whose effect is *files an in-container agent must read*: the daemon sets
`HOME` and `CODEX_HOME` to the host directories *the calling session* binds to
`/home/toolbox/.claude` and `/home/toolbox/.codex` and passes `--scope global`,
so upstream's own resolution (`$CODEX_HOME`, else `os.UserHomeDir()`) writes
where the container reads. Those paths travel with the request
(`TOOLBOX_HOST_AGENT_HOME` / `TOOLBOX_HOST_CODEX_HOME`, emitted by
`sessionplan`) because the daemon cannot derive them: `mounts_root`, `--profile`
and `inherit_host_auth` each move that source, and the last can move one of the
two without the other. A third mode is
deliberately absent: nothing runs proximo *inside* the container, and nothing
runs it elevated. Owned by `internal/bridge`.

Why the term exists: "run proximo from the container" reads like one operation
and is three, distinguished by *where the effect has to land* rather than by what
the verb does. Without the distinction the obvious designs are both wrong in ways
that only show up at runtime — a binary in the image clobbers the host's compose
stack through the mounted Docker socket, and a bridged `skill install` writes to
the host's `~/.claude`, which is not the `~/.claude` any container-side agent
reads. Naming the modes makes "which home does this verb write into" a question
the reader is forced to ask.

### Proximo Availability Gate

The single predicate for "is proximo usable in this shell": the presence of
proximo's root CA at the container path `/etc/ssl/proximo-ca.pem`.

Concretely: host-side the gate is one resolved value, `proximo.Gate{Enabled,
CAPath, CAExists}`, derived by `proximo.Resolve(host, cfg)` once per
*configuration it is asked about* — which for a session is once, full stop.
That is the precise invariant, and the loose reading ("once per process") would
be wrong in a way that matters: a write lints the candidate stack against the
current one, `proximo:` is an editable key, so those two stacks are two
questions and each is entitled to its own answer. What the value forbids is the
same configuration being asked twice
— explicit `proximo: true`/`false` wins, `nil` auto-detects from the host CA's
existence, and `false` short-circuits before the `proximo config ca-path` query
so an opted-out workspace never pays that subprocess. Everything downstream
*reads* that value rather than re-deriving the rule: `Gate.CAMount` is the bind
`mountplan` injects, `Gate.Env` the CA-trust variables `sessionplan` composes,
and `Gate.Enabled` the discovery flag the Docker edge acts on. It reaches both
planners through their `PlanInput`, the seam that already carries the session's
resolved host-side facts, and `cmd.startSession` is where the one derivation
happens — beside the [Declared Host](#declared-host) it is resolved against.
No function derives it on the side: `mountplan.Merge` and everything built on
it (`Classify`, `Names`, `StateDirPath`) take the gate as an argument, so the
read-only surfaces resolve one at their own command edge exactly as a session
does at `startSession`. Handing the derivation to a shared callee is what let
a single invocation pay for it more than once while every call site still
looked like it was only reading a list. So the
mounted file *is* the in-container shadow of that decision: `entrypoint.sh`
self-gates its whole trust block on it; the bridge shim tests the same file
before any POST, and refuses with one message naming both causes (proximo
absent on the host, or disabled for this workspace). No third state, no extra
env var, and no round-trip to the daemon to learn the answer.

Why the term exists: enablement was readable from three unrelated places — a
tri-state config field on the host, a file test in the entrypoint, and, for
anything else, an error returned by the daemon after a network round-trip. Calling
the CA's presence *the* gate collapses those into one testable fact and settles
what a proximo-less host should look like from inside a container: a command that
refuses clearly, not a command that is missing (which invites an agent to install
it) and not a command that fails only after reaching the host.

Why it is a value: "the single predicate" was the claim, and three exported
host-side entry points (`Enabled`, `CAMount`, `Env`) each re-deriving it from
`(host, cfg)` was the reality — subtly different spellings of one rule, each
paying its own CA query, so a single `toolbox shell` spawned the proximo
binary once per entry point it happened to reach, to answer one boolean. Resolving the gate into a value and threading it makes the claim
true rather than aspirational: the mount and the env can no longer disagree
with the decision they follow, because they are answers *on* it.

Threading it is necessary and not sufficient, and the residual trap is worth
naming: the derivation also rides inside `mountplan.Merge`, so *any* second
merge re-pays it. A session that read the state directory through
`mountplan.StateDirPath` after planning did exactly that — a subprocess spawn
hiding behind what reads like a path lookup. The merge already settled the
answer, so the plan publishes it (`mountplan.Result.StateDir`) and the session
reads it there. The rule that generalises: once a pipeline holds a plan, ask
the plan, never the function that would rebuild one — and a function that
would rebuild one should say so by taking the gate, not by quietly resolving
another.

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
**interactive session launch** for create/open deliberately stays at the `cmd`
Docker edge — `container.Shell`'s TTY attach is not mockable across packages —
but it is not a second assembly: `cmd.openWorktreeSession` only builds a
[Session Intent](#session-intent), and `cmd.startSession` opens it, the same
entry point the `shell` command routes through. That mirrors how Session Plan /
Docker Identity keep daemon-edge state out of the pure plan. What a worktree
session *is*, though, is not a `cmd` decision:
`PlanInput.Worktree{RepoRoot, Agent, Prompt}` carries it, and Session Plan
derives both halves — the main repo's `.git` bind (through Mount Plan, so a
missing source is a soft skip like any other mount) and the `ExecCmd` that
launches the agent over the resolved shell. `cmd/worktree.go` is then flag
parsing + dispatch; the gitignored-state seeding (`seedWorktreeFiles`) is
driven by a non-nil `Worktree` on the intent, which is what keeps both create
and open re-seeding without either owning the call.

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

### Workspace Install Refresh

The rule every Init Sequence script follows when it re-runs a tool's *per-repo*
installer: refresh only when the bundled tool version differs from a
toolbox-owned stamp, **or** the artefact that installer should have written is
missing. Members today are `30-graphify.sh`, `31-codegraph.sh` and
`40-playwright-cli.sh`.

Concretely: the stamp lives outside the workspace at
`$HOME/.toolbox-state/install-refresh/<sha256($PWD)[0:16]>-<tool>` (host
`~/.toolbox/state/`, so it survives `toolbox stop` and is per-host), and its
content is the version last installed from. The emptiness guard scopes to the
version half alone — `{ [ -n "$ver" ] && [ "$stamped" != "$ver" ]; } || [ ! -f
<artefact> ]` — because an unreadable version must not read as "differs" (the
gate would reopen every shell) and must not suppress the artefact half either (a
deleted install would stop self-healing). One artefact per tool is watched, on
purpose: `SKILL.md` for graphify and playwright-cli, `.mcp.json` for codegraph,
and never the PreToolUse hook block or the `## graphify` section in `CLAUDE.md`,
which a user may remove deliberately. `30-graphify.sh` additionally narrows the
matchers `graphify install` writes (`Bash|Grep` → `Grep`, `Read|Glob` → `Glob`)
only while they are still the known upstream values, so a hand-edited hook
survives — and, because that rename is exactly what blinds upstream's own
"drop what I wrote last time" filter (it keys on the wide literal **and** the
entry mentioning `graphify`), the same jq first drops the graphify-owned entries
already parked at the narrowed matcher, which are the previous run's. Ownership
is checked only on the drop, and the drop only fires when a wide graphify entry
is actually present, so neither a hand-written `Grep` hook nor a failed install
loses anything. `graphify hook install` stays outside the gate, because `.git/hooks/`
is never committed and so absent from a fresh clone. Held by
`TestWorkspaceInstallRefreshGate` + `TestGraphifyHookInstallOutsideGate`
(`internal/build/workspace_install_refresh_test.go`) over the embedded scripts,
and by the `Workspace Install Refresh` block in `smoke-test.sh`, which boots
`30-graphify.sh` in a throwaway workspace and reads the result back out.
Rationale and rejected options: `docs/adr/0001-workspace-install-refresh.md`.

Why the term exists: before this concept was named, the three scripts re-ran
their installer on *every* shell, gated only on the opt-in artefact existing.
Those installers write into the workspace, so every image upgrade that moved a
bundled tool version rewrote tracked files — `CLAUDE.md`,
`.claude/settings.json`, `.claude/skills/` — and handed the user a dirty tree
they never asked for. The scripts already referenced each other in comments
("mirrors graphify/codegraph") without a written rule, the same unnamed fan-out
this repo collapsed before under Tool Catalog and Config Schema: unnamed, the
fourth member would have copied the old pattern.

### Peer Anchor

The toolbox-owned container whose PID namespace every peer-messaging session
joins: `toolbox-peer-anchor`, named by `sessionplan.PeerAnchorContainerName`.
It is not a shell — nothing runs a workspace in it, and its only job is to give
the namespace a stable owner.

Concretely: the runtime image with its entrypoint overridden past the image's
shell-start init but **not** past tini — `tini -g -- sleep infinity`, because
the anchor's PID 1 is PID 1 for every session that joins the namespace and
reaping orphans is PID 1's job — `AutoRemove: false`, created lazily by
`container.ensureAnchor` on
the first participating shell — with `peer_messaging` defaulting to true, that
is effectively the first shell opened (it reuses `runplan.Compute` for the same
connect / start / create branch the session container takes). Participating
sessions get `PidMode: container:toolbox-peer-anchor` plus the
`toolbox-cc-socks` volume mounted at `/tmp/cc-socks`. It carries the
`toolbox-` prefix so `StopAll` sweeps it up, and `List` excludes it by name
because that command enumerates shells. An anchor that cannot be created warns
and the shell starts without peer messaging.

Because it outlives every session, an anchor can outlive the spec it was
created from — and one created before the tini override kept a PID 1 that never
reaped. `ensureAnchor` therefore replaces an anchor whose entrypoint is not the
current one, but only when no session holds its namespace: Docker does not
refuse the removal of an in-use anchor, it kills the sessions on it. That
replacement is **load-bearing for the [Session Reload](#session-reload)**, not
merely tidy: the reload destroys before it creates, so it is the one moment a
single-session developer stops holding the anchor — and a legacy anchor's
failure mode there is not accumulated zombies but the loss of every sibling
session, which the reload turns from occasional into routine.

Rationale and rejected options:
`docs/adr/0003-cross-container-peer-messaging.md`.

Why the term exists: the reason a *separate* container is needed is not
self-evident from any one file. Session containers are `AutoRemove: true` and
each owns a workspace, so none can host a namespace the others outlive — the
anchor exists precisely because no session can play that role. Unnamed, the
next reader sees an odd extra container in `docker ps` and the obvious
"simplification" is to point `PidMode` at one of the shells, which breaks the
moment that shell exits.
