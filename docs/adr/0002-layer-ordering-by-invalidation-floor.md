# Layer ordering by Invalidation Floor: move the fetch-stage COPYs below the RUN tail

Status: accepted

The Dockerfile's build-strategy header promised that "a Renovate bump of one
tool re-runs only that stage + its COPY — never the tail". Measured against the
published GHCR manifests, it does not hold: `0.83.0` → `sha-08ef69b`, whose only
image-affecting change is a one-line `OMZ_COMMIT` bump, moves **34 of 71 layers
and 586 MB of 1157 MB** — 51% of the image, for a shallow git clone worth ~30
MB. `COPY --link` builds each layer independently of the filesystem beneath it,
so the 28 fetch-stage COPYs do not invalidate *each other*; but all 28 are
declared **above** the entire `RUN` tail, and a `RUN` is invalidated by anything
that precedes it. The rare→frequent ordering of the tail, measured by Renovate
cadence, therefore only protects against bumps of the npm/pip CLIs installed
*in* the tail. It does nothing for the ~289 bumps per six months that land in a
`fetch-*` stage (`OMZ_COMMIT` 49, `UV` 33, `ZSH_COMPLETIONS_COMMIT` 29, `GLAB`
21, `HOMEBREW` 19, `GCLOUD` 17, `RTK` 15, `DOCKER_CLI` 14, …), each of which
costs every puller 586 MB.

We therefore order layers by **Invalidation Floor** (see `CONTEXT.md`): the
highest layer a change touches, and hence the boundary below which everything is
rebuilt with a fresh digest. The rule that replaces the header's promise is *not*
rare→frequent among the `RUN`s — it is **how few consumers each COPY has below
it**. Concretely: all 29 build-stage COPYs (28 `fetch-*` plus `rtk-builder`)
move to just above `USER toolbox`, with no `RUN` left below them. That requires
dissolving the three `RUN`s that consumed copied files: the shared completions
layer (nine `<tool> completion zsh` invocations, moved into the respective fetch
stages following the pattern bat/fd/eza/brew already use, leaving only `pnpm`
and `codex` to generate theirs in their own install layers); the Homebrew
`safe.directory` layer (its `test -x` was a duplicate — `fetch-brew` already
runs the same check on its own `/out`, and the smoke test runs `brew --version`
as the runtime user); and the user-setup layer (`usermod -d -m` plus
`chmod -R a+rwX /home/toolbox`, resolved by having `fetch-omz` clone straight
into `/out/home/toolbox` and set the permissions itself, so `-m` has nothing
left to relocate). Afterwards a fetch-stage bump moves exactly one layer.

Separately and as a precondition, the fetch stages normalise mtimes
(`freeze-mtimes`, a `touch -d @1` over `/out`). Files in the image today carry the wall-clock time
of whichever build last ran that stage — `jq` 2026-06-20, `kubectl` 2026-07-23,
`go` 2026-08-11 — and `COPY --link` folds mtime into the layer digest. The 28
COPY digests are therefore stable only for as long as BuildKit reuses the stage
without re-executing it. Losing or rotating `buildcache-main` would rebuild all
of them with fresh mtimes and push **the entire 1157 MB** to every user with no
Dockerfile change at all. Without reproducible digests the layer-count gate
below measures cache luck rather than the floor.

## Considered Options

**Leave the ordering and lower the image weight instead.** Attacks the symptom:
the tail would still be invalidated wholesale, just a cheaper wholesale. The
same ~289 bumps would keep re-downloading whatever the tail weighs.

**Intra-layer delta transfer** (zstd:chunked / eStargz / SOCI). Would make the
question moot by shipping only changed chunks, but requires the containerd
snapshotter plus a runtime and registry that negotiate it; on Docker Desktop
against GHCR it is not available today. Revisit if the containerd snapshotter
becomes the default.

**A static test asserting no `RUN` below the COPYs names a fetch-provided
binary.** Rejected: parsing binary names out of concatenated shell inside `RUN`
directives is precisely the "clever regex" this repo already identifies as the
fragile half of `TestSmokeTestVendorCompletionsFloor`. The real case is already
covered — every such `RUN` runs under `set -eux`, so a missing binary fails the
build immediately and legibly.

**A pre-merge gate in `docker-ci.yml`.** It builds amd64 with `load: true`, so
it could block before the merge. But a locally loaded image exposes uncompressed
diffIDs while the remote manifest exposes compressed digests; comparing them
means fetching and parsing `latest`'s config blob. The post-merge comparison in
`docker-publish-reusable.yml` is manifest-against-manifest, needs no pull, and
costs one release cycle of latency instead of that machinery.

**A megabyte threshold rather than a layer-count threshold.** A `GO_VERSION`
bump legitimately moves ~120 MB, so a byte threshold reddens on healthy changes
while passing a regression that invalidates thirty small layers. Layer count
describes the floor directly; the byte figure stays in the job log for
readability.

## Consequences

- A `fetch-*` bump moves one layer (~30 MB for `omz`, ~120 MB for `go`) instead
  of 34 layers and 586 MB. A tail bump is unchanged — it was already cheap.
  **Superseded: see the Follow-up below — a tail bump rebuilt the whole tail.**
- Adding shell completion for a CLI changes shape: the `_<tool>` file is
  generated inside that tool's fetch stage, never in a shared layer, because a
  shared layer re-imposes a floor on every COPY feeding it. The literal write
  path stays literal so `TestSmokeTestVendorCompletionsFloor` keeps deriving its
  number; `pnpm` and `codex` keep theirs in the tail, where those CLIs are
  installed.
- `docker-publish-reusable.yml` gains a layer-count gate. The `merge` job
  resolves `latest` to a digest **before** `imagetools create` overwrites it —
  tolerating a missing or half-written baseline, so the first-ever publish
  passes — and fails when the new amd64 manifest diverges by more than 3 layers
  **larger than 1 MB** (raised to 6 in follow-up 2, and moved into the script). The comparison lives in
  `.github/scripts/invalidation-floor.sh` rather than inline in the workflow, so
  it can carry a `--self-test` over fixed data; the workflow runs that self-test
  before every real comparison. A gate whose logic is first exercised on the day
  it must fail is a gate nobody has tested — and the fixture proved its worth
  immediately, since the first version put three big layers against a `-gt 3`
  bound and asserted nothing. Only amd64
  is measured: the floor is a property of the Dockerfile, not of the
  architecture. The size filter is not a refinement, it is what makes the gate
  work: a naked layer count does not separate the two measured cases. The
  `30-graphify.sh` fix moves 11 layers and 0 MB — the asset COPYs and the two
  trailing `RUN`s, all in the tens of kB — while the `OMZ_COMMIT` regression
  moves 34, of which 17 are above 1 MB, for 599 MB on amd64.
- The mtime normalisation trips that gate by design, every layer at once, and
  ships with an explicit single-use `[floor-reset]` suppression. The marker is
  matched against **every commit message in the push**, not just the head
  commit: this repo allows squash, rebase and merge commits, and only the squash
  path puts the PR title on `main`. Matching `head_commit` alone would have
  worked in one of the three cases.
- Normalisation covers all of `/out` with no exception for the `fetch-omz` and
  `fetch-brew` clones. An earlier draft excluded `.git/index` to keep it older
  than the working tree; that turned out to be unnecessary. Git already treats an
  entry whose mtime equals the index's own as *racily clean* and re-hashes the
  content instead of trusting the stat cache, so freezing everything to the same
  second is the safe direction. The cost is one content re-hash on the first
  `git status` in a clone of a few MB.
- The `fetch-omz` change is the one place where a mistake fails silently on a
  green build: the clone would arrive with the fetch stage's permissions rather
  than the world-writable ones `chmod -R a+rwX` used to apply, and would only
  break on a host whose UID is not 1000. `smoke-test.sh` therefore asserts the
  permissions of `~/.oh-my-zsh` directly.
- The build-strategy header, the *Dockerfile build layout* entry in
  `.claude/rules/image-build.md` and the corresponding section in
  `docs/internals/image-build.md` are corrected in the reordering commit, not
  before it — until the code moves, the current wording accurately describes the
  current (wrong) behaviour.

## Follow-up (2026-08-18): a block of version ARGs defeated the ordering

The Consequences above state that "a tail bump is unchanged — it was already
cheap". That was wrong, and the gate this ADR introduced is what surfaced it: the
first genuine comparison after the reordering rejected a one-line `OCI_VERSION`
bump at **16 substantial layers, 694 MB**.

The cause was not the ordering but the scope of the version ARGs. All 14 of them
were declared as one block at the top of the final stage, and a build ARG that is
in scope lands in the cache key of every RUN below it — the `|16` prefix
`docker history` printed on every tail layer. So each of the 21 tail RUNs was
keyed on all 14 versions: bumping any single tool gave every tail RUN a new key,
the whole tail rebuilt, and the layers came back with new digests.

While that held, the rare→frequent ordering could not do anything. The position
of a RUN only matters if a bump invalidates *some* RUNs; here every bump
invalidated all of them, so ordering them by Renovate cadence bought nothing.
Measured with `.github/scripts/invalidation-floor.sh` against the published
`sha-<commit>` tags, post-reordering:

| Transition | Change | Layers > 1 MB | MB |
|---|---|---|---|
| `2800616` → `12ade4b` | dockerfile ARG bump | 31 | 966 |
| `12ade4b` → `acf2843` | `OCI_VERSION` bump + one init.d asset | 16 | 694 |

Each version ARG is now declared immediately above the single RUN that consumes
it, so a RUN is keyed only on the versions it actually uses and the ordering
finally takes effect. `TestFinalStageARGsScopedToTheirRUN` holds the
placement over the embedded Dockerfile.

Like the mtime normalisation, the move rebuilds every tail layer once and ships
with a single-use `[floor-reset]`.

**Necessary, not sufficient.** Scoping the ARGs only restores the *premise* of the
ordering; it does not by itself bring a bump under the gate's max of 3. Now that
cost is positional, the order itself matters — and it does not match the cadence
measured in `docs/internals/image-build.md`: `oci` (20 bumps in 6 months) sits 4th
of 21 while `codegraph` (15) sits 9th, so a mid-cadence bump still moves more than
three substantial layers. Closing that gap needs a reorder by measured cadence
*and* a `MAX_LAYERS` recalibrated to the resulting worst case. Neither is done
here — both land in follow-up 2 below, and the reorder carries a hazard worth writing down: moving the `oci-cli`
RUN below the `graphifyy` one inverts pip dependency resolution, and the build
verifies graphify *before* installing oci, so a break there would pass green.
Sequence the reorder so the pip pair keeps its relative order, or add graphify's
import check after the oci install.

Two observations from the same investigation are **not** addressed here, and
neither is proven:

- A green publish (run `32074128559`, commit `12ade4b`) logged `Moved 0 layer(s)
  of 70` for a transition measured above at 31 substantial layers. The only
  reading that reconciles the two is a baseline resolved to the run's own output.
  Publishes on `main` are cancelled by the next push, so the gate may have been
  passing vacuously. Unverified. **Corrected: the re-run half of this was
  wrong.** An earlier wording credited `CI Retry Cancelled` with re-running
  them; that workflow fires on `workflow_run` of `CI` and requires
  `event == 'pull_request'`, so it never touches a publish and never runs for a
  `main` push. Nothing re-runs a cancelled publish. Follow-up 2 resolved the
  vacuous pass by another route and left this sentence unchecked behind it.
- Because those cancellations leave `:latest` behind, the gate attributes the
  accumulated cost of the skipped publishes to whichever commit next completes.
  The `OCI_VERSION` bump landed in `fb8dcca`, whose publish was cancelled; the
  gate reported it against `acf2843`.

## Follow-up 2 (2026-08-19): the ordering, now that it does something

Scoping the ARGs (follow-up 1) made position matter. Three publishes on `main`
then measured what position is worth, and confirmed both halves of that
follow-up's prediction:

| Publish | Bump | Where it sat | Layers > 1 MB | MB | Gate |
|---|---|---|---|---|---|
| `80f56e9` | claude-code | last version RUN | 2 | 101 | pass |
| `a9839f8` | codex | 7th of 13 | 7 | 320 | **fail** |
| `6b112a8` | yq | a `fetch-*` stage | 2 | 10 | pass |

Before the scoping, every one of these would have moved 16-31 substantial layers
and 600-966 MB. So the cost of a bump is now `(number of version RUNs at or below
it) + 1` — the `+1` is a trailing non-version layer, measured by attributing the
claude-code publish: its own 96 MB npm layer plus one below it.

Two consequences follow, and this commit takes both.

**The tail is reordered by re-measured cadence** (6-month window): graphifyy 100,
claude-code 95, wrangler 37, pnpm 36, codex 34, oci 20, codegraph 15,
playwright-cli 10, cf 8, azure 7, playwright 7, pyright 5, typescript 2. Least
first, most last. Two orderings inside that are load-bearing rather than
aesthetic, and both fall out of the cadence order anyway: `oci` stays above
`graphifyy`, so the two pip installs keep resolving shared dependencies in the
same order as before — the build verifies graphify *before* installing oci, so an
inversion would ship broken and pass green — and `playwright` stays above
`playwright-cli`.

**`MAX_LAYERS` moves from 3 to 6**, and moves into the script, so the calibration
is one literal that CI reads rather than two that can drift. 6 is the cost of the
fifth-most-bumped tool once ordered, so it admits graphifyy, claude-code,
wrangler, pnpm and codex — 302 of the 376 tail bumps in the window, 80% —
while the structural regression the gate exists for, measured at 16-31 layers,
still fails by a factor of 3 to 5. The `--self-test` fixture now derives its layer
count from `MAX_LAYERS` instead of hardcoding four. At a hardcoded four the
self-test does not go quiet when the bound is raised — it goes red, measured:
`MAX_LAYERS=6` against the old fixture prints `self-test FAILED: regression
accepted` and exits 1, because four moved layers no longer exceed six. So the
fixture had to move with the bound either way; deriving it means the next person
to change the threshold cannot fix that red by weakening the fixture instead.

**The baseline was the other half of the noise, and it is fixed here.** The gate
resolved its baseline from `:latest`, which is mutable and lags whenever a
publish is cancelled by the next push — this repo cancels them and re-runs them.
Two costs followed. One commit was billed for another's churn: `OCI_VERSION` was
bumped in `fb8dcca`, whose publish was cancelled, so `:latest` never advanced and
the gate charged the cost to `acf2843`. And a re-run of an already-published
commit resolved `:latest` to the manifest it was about to push and compared it
against itself, which is the unexplained `Moved 0 layer(s) of 70` in run
`32074128559` — no longer a hypothesis. The baseline is now the immutable
`sha-<commit>` tag of the commit the push replaced, falling back to `:latest`;
and a baseline that equals the manifest just pushed is reported as "no comparison
performed" instead of being banked as a green.

**How often the immutable half actually lands, measured after the fact:** only
when the commit the push replaced happened to publish. `docker-publish.yml` is
path-filtered to `internal/build/assets/**` and its own two workflow files, and
over the twenty `main` commits ending at `fc6f8c7`, 11 matched and 9 did not — so
`github.event.before` names a commit with no `sha-` tag roughly half the time,
and the `:latest` fallback is the ordinary path rather than the exception. That
is not a degradation: `:latest` *is* the last published manifest, which is the
baseline the comparison wants. What the fallback loses is attribution, and only
the part of it that was never recoverable — bytes from a cancelled publish were
never delivered, so the next publish genuinely does push them.

**What this still does not fix, and one idea that does not work.** oci (20 bumps)
is bounded at 7 and codegraph (15) at 8, so roughly 75 bumps per window — about
12 a month — will still redden a publish. An earlier draft of this section
proposed the durable fix as a positional invariant: no layer *above* the highest
changed instruction may move. That does not work, and the case that motivated
this whole ADR is the counter-example. With the fetch COPYs declared above the
tail, an `OMZ_COMMIT` bump changed a COPY high in the file and moved the 34
layers *below* it — every moved layer sits at or below the change, so a
positional rule passes it. Legitimate bump and structural regression have the
same shape; they differ only in magnitude, which is what a count measures. So the
count is the right instrument and the residual noise is not an artefact of it:
a bump high in a 13-deep tail is genuinely expensive, and the gate saying so is
the gate working. The way out is fewer substantial layers in the tail — moving
npm/pip installs into `fetch-*` stages where each bump costs one `--link` layer,
as the six tools in that table already do — not a cleverer bound.

That way out is taken for the two worst offenders in the same change. `oci` (20
bumps, bounded at 7) and `codegraph` (15, bounded at 8) now install in
`fetch-oci` and `fetch-codegraph`, so each bump moves one layer whatever its
position, and the tail drops from 13 version blocks to 11 — which also takes two
off the bounded cost of everything that sat above them. Coverage against
`MAX_LAYERS` = 6 goes from 302 of 376 tail bumps to about 337: roughly 6 red
publishes a month rather than 12. `fetch-codegraph` has to use the same node
image as the final stage, because an npm global tree is only valid on the runtime
that resolved it. `fetch-oci` installs into a venv at `/opt/oci-cli` rather than
the system site-packages, which also ends a coupling this ADR had only flagged:
oci and graphifyy shared site-packages, so whichever pip ran last decided the
version of every dependency they have in common, and the only check that would
have caught a break — graphify's import test — ran before oci was installed.
What is left over the bound is playwright-cli (10), cf (8), azure (7), playwright
(7), pyright (5) and typescript (2); the same treatment applies to any of them.

## Follow-up 3 (2026-08-26): what the count was actually counting

Follow-up 2 closed with "the count is the right instrument and the residual
noise is not an artefact of it". Half of that holds. The instrument is right;
the noise was not all residual, and a share of it never measured ordering at
all. Issue #778 is the case that separates them: run `32919323729` on `main`
(`14bb4c0`) failed at **16 substantial layers, 639 MB**, and the diff it was
charged for — `2fe107a..14bb4c0` — touches one image-affecting line,
`GCLOUD_VERSION` 581.0.0 → 582.0.0.

Attributing every moved layer to its `created_by`, against the published
manifests:

| Cause | Layers > 1 MB | MB | Where |
|---|---|---|---|
| The diff | **1** | 52 | idx 64, `COPY --link --from=fetch-gcloud` |
| Archive Drift | **12** | 587 | idx 6, the final stage's apt layer, plus the 11 tail RUNs beneath it |
| Fetch Nondeterminism | **3** | 21 | idx 65 `fetch-omz`, 66 `fetch-brew`, 69 `rtk-builder` |

Both new terms are defined in `CONTEXT.md`. The base image did not move: the
first five layers are byte-identical across the two manifests, Debian's own
included. The drift enters at layer 6, which is ours — `apt-get install` with no
version pins — and cascades through everything parent-chained below it. The
three `COPY --link` layers are a second, unrelated defect: those stages
re-executed because their shared `fetch-base` moved, and unlike the other 27
they do not produce identical bytes twice. `freeze-mtimes` made the fetch
layers reproducible against timestamps; it says nothing about content.

**The issue's first proposed direction is not merely weak, it is fatal.**
Comparing only layers whose `created_by` differs would have counted zero here —
and zero for every regression this ADR exists to catch. Measured: all 70
`created_by` strings are byte-identical between the two images. A build ARG's
*value* never reaches `created_by`, which carries only the `|N` argument count
and the command text, so the follow-up 1 regression (one `ARG` bump rebuilding
the whole tail) and a pure reorder of the `RUN`s both leave it untouched. The
filter would not have softened the gate; it would have retired it.

### The rule

The image has two kinds of layer, and only one of them cascades. A `RUN` is
invalidated by anything above it; a `COPY --link` is built independently of the
filesystem beneath it. So:

> If the final stage's first `RUN` layer moved, every `RUN` layer moving is
> expected, and only the `COPY --link` layers are counted. If it did not move
> and `RUN` layers moved anyway, that is the regression the gate exists for, and
> everything is counted as before.

The first `RUN` layer of the final stage is the apt layer, and it is the one
instruction in the image that no version bump can reach. It is identified
structurally, as the first layer whose `created_by` begins with `RUN |` — the
node image's own layers are `RUN /bin/sh -c`, with no build args and hence no
prefix. Counting the args in that prefix would be more expressive and would
break silently the day a global `ARG` is added; a fixed index would break
sooner. `docker buildx imagetools inspect <ref>@<digest> --format
'{{json .Image}}'` returns the history, which costs one call per image and no
new tooling.

Against every transition measured so far:

| Transition | apt layer moved | Verdict | Correct |
|---|---|---|---|
| `2fe107a` → `14bb4c0` (#778) | yes | 4 counted, pass | yes |
| `12ade4b` → `acf2843` (`OCI_VERSION`, follow-up 1) | no | 16 counted, fail | yes |
| `OMZ_COMMIT` pre-reordering | no | 34 counted, fail | yes |
| a pure reorder of the tail | no | counted, fail | yes |

The layers do not form a clean prefix and a clean block — two `RUN`s sit
interleaved among the asset COPYs at idx 27 and 38 — so the split is by
instruction kind, never by index range. Both weigh under `MIN_BYTES` and have
never counted, but a positional reading of the same rule would be wrong today,
not eventually.

**The hole this leaves.** A genuine ordering regression that lands on the same
day as an archive update is excused. It is caught by the next publish without
drift, which costs one release cycle — the same latency this ADR already accepts
for reporting after the push rather than before it.

### Calibration, failure, and what the log says

`MAX_LAYERS` stays at 6 and stays one literal. It was calibrated on the
drift-free path, which is where it still applies unchanged; on the excused path
it bounds a population that measures 4 today. Two literals would be two things
that drift apart, which is why follow-up 2 moved this one into the script in the
first place.

A history that cannot be fetched is a notice and a pass, matching the existing
treatment of a missing baseline and for the same reason: a publish that could
not measure must not redden. Falling back to the old unclassified count would
reproduce exactly the unactionable red being removed here, at the moment nobody
would understand why.

The log keeps one line, unchanged when the canary is quiet and naming the split
when it fires:

```
Moved 34 layer(s) of 70, 16 above 1 MB, 639 MB total — 12 excused as archive drift, 4 counted (max 6).
```

The real cost to pullers stays the first thing on the line. `Invalidation Floor`
in `CONTEXT.md` is deliberately left alone: it defines what a change costs the
people pulling the image, and that cost is real whoever caused it. What changes
is only which part of it we treat as a regression.

### The invariant, and a test for it

The canary depends on a fact nothing currently pins: the final stage's first
`RUN` has no version `ARG` in scope. Put one there and the canary fires on a
legitimate bump, excusing the entire tail — a gate that goes green in silence,
which is the worst way it can break. `TestFinalStageARGsScopedToTheirRUN` gains
an assertion that only `TARGETARCH` and `DEBIAN_FRONTEND` appear between the
final `FROM` and the first `RUN`. It goes in that file because it is the same
parse read by a second consumer, and its comment has to name the canary — read
without that, it looks like pedantry and gets deleted.

The `--self-test` fixtures grow a third column for `created_by` and two cases:
drift excused, and drift *plus* a real regression among the COPYs, so the new
branch asserts something in both directions. The second derives its layer count
from `MAX_LAYERS`, as the existing fixture already does.

### What this does not fix

Nothing here reduces a single byte anyone downloads. Archive Drift is real
transfer, and 587 MB of it. What removes it is the direction follow-up 2 already
named — fewer substantial layers parent-chained below the apt layer — and #778
is further evidence for it rather than an alternative to it: with the tail
emptied, an archive update moves the base layer and little else. Fetch
Nondeterminism is a separate defect with a separate remedy and is tracked in
issue #780; without it the worst case does not stay under the bound, because
three layers move on every cache miss regardless of ordering.

Three changes, in this order: the measure, then the tail, then the
reproducibility. The measure comes first because the other two have no honest
signal to be judged by until it does.
