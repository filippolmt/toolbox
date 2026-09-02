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

### Image Plan

The two-phase decision tree that guarantees the image referenced by a
`SessionPlan.Image` is ready before `ContainerCreate`.

Concretely: `imageplan.RefreshAtStart(ctx, cli, image, stateDir, stake)`
runs at the top of `container.Shell` and best-effort syncs the image
against its registry, steered by the Image's pull policy — `never` skips
the round-trip, `always` forces `imagepull.ForcePull`, `auto` (default)
probes and then *asks*, see
[Start-up Refresh Prompt](#start-up-refresh-prompt); errors are
swallowed. The `imageplan.Stake` is the caller's answer to *what does a
yes cost here besides the wait* — `StakeDownload` on a create,
`StakeRecreate` on a stopped container the yes would replace — and it
adds no case to the tree: it words the question and points the
unanswered window. `imageplan.Refresh` is the same act with nothing to ask,
kept for the [Session Reload](#session-reload), which runs it before it
destroys anything and whose `auto` branch stays on the TTL-cached
`imagepull.RefreshIfStale` — a reload adopts what the store holds, and
the [Image Prefetch](#image-prefetch) is what advanced it. That
policy steers the **synchronous** refresh only: the background
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
that rule and why it inverts here. Honouring a yes is `Outcome.Accepted`, which
is the answer *and* the pull that landed, and is deliberately distinct from
`Synced` in both directions: a pull happens on cases nobody was asked about,
and a yes the registry could not honour would spend a container on an image
that never arrived. `container.replaceForRefresh` then destroys the stopped container and
the branch becomes the `ActionCreate` that already pulls, creates and starts —
not the reload, which exists to replace a *live* session and carries a
handover payload there is nothing here to fill. Two rules guard the teardown.
**Everything that can fail runs first**: the pull (in `Accepted`), the
`:local` overlay build, and the create's own port pre-flight, since no removal
can undo a host port another container holds — each of the three fails the
shell with the container intact. And **the container is read a second time**,
because the answer describes the moment it was given and the question held the
terminal for seconds: `runplan.Compute` on the fresh inspect decides, so a
sibling shell that started it meanwhile keeps it and this session joins it
(the collateral connect is never asked about), one already removed leaves the
name free, and an unreadable answer destroys nothing. The removal itself is
`removeAndWait`, shared with the reload, and it is followed by
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
and `Outcome.Interrupted` is what keeps the second from being recorded as the
first — a ctrl+c stamps no postponement, announces nothing, and the session is
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
besides the developer's time — the one thing the caller knows and the
[Image Plan](#image-plan)'s tree does not.

Concretely: `imageplan.Stake`, either `StakeDownload` (nothing exists yet, so
a yes buys the image and costs only the wait) or `StakeRecreate` (a container
already exists and a yes replaces it, discarding whatever was written inside
it outside the bind mounts). `Stake.offer()` returns the question, the
[Elapsed Answer](#elapsed-answer) and the postponement line as one value: the
three are one editorial decision, and a question worded around a container
that a clock could accept would be a bug on its own. A stake the method does
not know is worded as the download — the form that spends nothing but time.
`container.offerRefresh` derives it and returns it alongside the outcome, and
that is the **only** place the branch is classified: honouring a yes reads the
stake rather than re-deriving from the [Run Plan](#run-plan)'s `Op` what a yes
meant. Owned by `internal/imageplan`; the branch that supplies it by
`internal/container`.

Why the term exists: while the prompt fired on a fresh create alone, what a
yes cost was a constant — a download — and needed no name. Extending the
question to a stopped container gave the same tree a second caller whose yes
also destroys something, and the choice was between a second case inside the
tree (which is about the registry and the store, and has no business knowing
which container branch it was reached from) or an input. Naming the stake is
what keeps the branch out of the tree while still letting the wording, the
countdown's default and the postponement line differ by branch. The decision
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
current. One poll is probe → prefetch → publish:
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
also the first, when the old session is still alive; `imageplan.Refresh`
+ `Ensure` gate the teardown, so a reload with no usable image leaves
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

It earns its own entry only because two host-injected names share a
prefix and point in opposite directions: `TOOLBOX_RELOAD_MARKER` travels
host → container and never leaves it, `TOOLBOX_RELOAD_FROM` travels
host → host across the exec and never enters one. Owned by
`internal/reload`; bound to the shell side by
`TestReloadMarkerContract`.

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

Concretely: `fetch-omz` and `fetch-brew` clone git repositories, and a
shallow fetch does not produce a reproducible pack file.
`freeze-mtimes` normalises timestamps, which is what makes the other
fetch stages reproducible, but it cannot normalise content.

`rtk-builder` was first counted here and does not belong: on amd64 its
binary comes from a checksummed tarball and is identical across builds.
Its layer moved because the COPY named a single file, and a `--link`
layer stamps the destination directories it has to synthesise with the
build clock — a defect of the copy, not of the stage's output. The cost stays invisible while BuildKit reuses the
stage and appears in full whenever anything invalidates it — an archive
update, or a lost build cache.

Why the term exists: `COPY --link` is what buys a bump the price of one
layer, and that guarantee is worth exactly as much as the reproducibility
of the stage behind it. Two stages do not hold up their end, and the gap
had no name because the ADR that introduced the ordering had only
measured mtimes.

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

Concretely: `proximo.Enabled(cfg)` decides host-side whether the CA is mounted at
all — explicit `proximo: true`/`false` wins, `nil` auto-detects from the host CA's
existence — so the mounted file *is* the in-container shadow of that decision.
`entrypoint.sh` already self-gates its whole trust block on it; the bridge shim
tests the same file before any POST, and refuses with one message naming both
causes (proximo absent on the host, or disabled for this workspace). No third
state, no extra env var, and no round-trip to the daemon to learn the answer.

Why the term exists: enablement was readable from three unrelated places — a
tri-state config field on the host, a file test in the entrypoint, and, for
anything else, an error returned by the daemon after a network round-trip. Calling
the CA's presence *the* gate collapses those into one testable fact and settles
what a proximo-less host should look like from inside a container: a command that
refuses clearly, not a command that is missing (which invites an agent to install
it) and not a command that fails only after reaching the host.

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
