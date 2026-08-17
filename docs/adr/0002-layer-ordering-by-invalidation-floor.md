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
(`touch -d @1` over `/out`). Files in the image today carry the wall-clock time
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
- Adding shell completion for a CLI changes shape: the `_<tool>` file is
  generated inside that tool's fetch stage, never in a shared layer, because a
  shared layer re-imposes a floor on every COPY feeding it. The literal write
  path stays literal so `TestSmokeTestVendorCompletionsFloor` keeps deriving its
  number; `pnpm` and `codex` keep theirs in the tail, where those CLIs are
  installed.
- `docker-publish-reusable.yml` gains a layer-count gate. The `merge` job
  resolves `latest` to a digest **before** `imagetools create` overwrites it —
  tolerating failure, so the first-ever publish passes — and fails when the new
  amd64 manifest diverges by more than 3 layers **larger than 1 MB**. Only amd64
  is measured: the floor is a property of the Dockerfile, not of the
  architecture. The size filter is not a refinement, it is what makes the gate
  work: a naked layer count does not separate the two measured cases. The
  `30-graphify.sh` fix moves 11 layers and 0 MB — the asset COPYs and the two
  trailing `RUN`s, all in the tens of kB — while the `OMZ_COMMIT` regression
  moves 34, of which 17 are above 1 MB, for 599 MB on amd64.
- The mtime-normalisation commit trips that gate by design, all 71 layers at
  once, and is merged with an explicit single-use suppression. A gate never
  observed firing is a YAML file, not a gate.
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
