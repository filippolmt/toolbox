# Image size: two denominators, and where the weight actually is

Status: accepted

Every figure below is **as measured when this decision was taken** (2026-09-14, `ghcr.io/filippolmt/toolbox:latest`, both published arches) — evidence for the choice, not a description of the repo today. Nothing here is kept in sync; current values live in the files that set them.

ADR 0002 made a layer *move* cheap. It said nothing about how much a layer weighs,
and the two are optimised by opposite reflexes: 0002 wants many small independent
layers, size wants few dense ones. This ADR is the size half, and its first job is
to stop the word "size" from meaning two different numbers in the same sentence.

## Two denominators

A `docker pull` transfers **compressed** layer blobs; a disk holds them
**uncompressed**. For this image the two differ by more than 3.5×:

| | amd64 | arm64 |
|---|---|---|
| pull (sum of layer blob sizes in the OCI manifest) | 1.28 GB | 1.26 GB |
| disk (sum of `docker history` sizes) | — | 4.68 GB |

Which number a change improves is not a detail. The three cloud CLIs are the
clearest case: `/opt/oci-cli`, `/opt/google-cloud-sdk` and `/opt/az` are 1.07 GB
of disk — 23% of the image — and 141 MB of pull, because a tree of Python source
compresses about 8×. Deleting a megabyte of Python buys a tenth of what deleting
a megabyte of binary buys. Conversely `codex`, `claude`, `workerd` and `node` are
four opaque binaries that compress 2–3× and therefore dominate what a developer
waits for on a cold pull while looking modest on disk.

So: **a size claim names its denominator, or it is not a claim.**

### The third denominator, which this ADR itself missed

Cold pull and disk are what a *new* machine pays. What a *returning* developer
pays is neither: it is the **bytes a bump moves**, and that number has its own
name in this repo already — [Archive Drift](../../CONTEXT.md#archive-drift), and
the gate in `.github/scripts/invalidation-floor.sh` that counts it. The final
stage's `apt-get install` is deliberately unpinned, every tail RUN is
parent-chained below it, and so a Debian archive update moves the whole tail for
nothing anyone edited. ADR 0002 measured one: **587 MB of 639 moved** on a bump
whose only edit was a one-line `GCLOUD_VERSION`.

That is the number that hurts in daily use, and it is the one this ADR spent its
whole first draft not measuring — the exact error its own opening line warns
against. Total size and moved size rank the same change differently: the three
cloud CLIs are 23% of the disk and 11% of the cold pull, but `/opt/az` alone was
329 MB of *every drift* until it moved out of the tail. Nothing in the Decisions
below is wrong; they were simply aimed at the two denominators a first-time
puller pays, and a returning one pays a third.

The remedy is not in this ADR. ADR 0002's follow-up 2 already named it — "with
the tail emptied, an archive update moves the base layer and little else" — and
emptying the tail is tracked there, not here.

## Where the weight is

Uncompressed, the image divides into three roughly equal thirds:

- **~1.55 GB of large single binaries.** `codex` 227 MB + `codex-code-mode-host` 63 MB, `claude.exe` 224 MB, two `workerd` 153 + 151 MB, two `node` 123 + 121 MB, `tofu` 105 MB, `sonar` 98 MB, `bun` 79 MB, then helm/kubectl/shellcheck/glab/uv/docker in the 40–60 MB band.
- **~1.07 GB of Python trees** — the three cloud CLIs, each a private venv.
- **the rest** — the Debian and `node:24` bases, the apt layer, `/usr/local/go` (189 MB), and `/usr/lib/aarch64-linux-gnu` (331 MB).

Three checks that came back negative are worth recording so nobody re-runs them:
`codex` is **already stripped**; `claude.exe` strips to 97 KB less than it started
(it is 224 MB of embedded payload, not symbols); and the apt lists, `/var/cache`,
`__pycache__` and the `go` `test`/`api`/`doc`/`misc`/`testdata` trees are **already
cleaned** by the existing stages. The hygiene layer of this problem is done. What
remains is duplication, dead dependencies, and the encoding of the blobs.

## Decision 1 — the vendored Node in the CodeGraph bundle becomes a symlink

`@colbymchenry/codegraph-linux-<arch>` vendors a complete Node runtime (121 MB)
next to its library, and the final stage is `FROM node:24-bookworm-slim`, which
already ships one. Two full Node runtimes of the same major, for one tool.

`fetch-codegraph` now asserts the two majors match and replaces the vendored copy
with an absolute symlink to `/usr/local/bin/node`. The assertion is the point: an
upstream bump past this base image's Node is a **red build**, never a silent
downgrade onto an older runtime. The link resolves in-stage (same base image) and
in the final image, which is what the re-run `codegraph --version` proves; the
stage `/out` goes from 276 MB to 160 MB. Verified beyond `--version` on a live
container by running `codegraph init` and `codegraph explore` against a two-file
tree — the SQLite index (`node:sqlite`), the tree-sitter parse and the blast-radius
query all work on the image's own Node.

This is **−116 MB disk, ≈ −40 MB pull**, and it is the only remaining duplicate of
its kind: the `claude.exe` / `claude-code-linux-arm64` pair that `find` reports
twice is already a hardlink, and the `workerd/bin/workerd` /
`@cloudflare/workerd-linux-arm64/bin/workerd` pairs likewise.

## Decision 2 — zstd for the published blobs

The single largest lever changes **no file in the image at all**. Measured on this
image's own content, `zstd -19` against the `gzip -6` BuildKit publishes today:

| subtree | raw | gzip -6 | zstd -19 | delta |
|---|---|---|---|---|
| `/opt/oci-cli` | 426 MB | 41.4 MB | 22.3 MB | −46% |
| `/opt/az` | 320 MB | 47.9 MB | 26.8 MB | −44% |
| `/opt/google-cloud-sdk` | 323 MB | 52.2 MB | 29.7 MB | −43% |
| `/usr/local/go` | 189 MB | 54.2 MB | 37.1 MB | −32% |
| `@openai` (codex) | 297 MB | 118.5 MB | 86.4 MB | −27% |
| `/usr/lib/<triple>` | 331 MB | 106.9 MB | 79.9 MB | −25% |
| `wrangler` | 227 MB | 58.0 MB | 43.6 MB | −25% |
| `@anthropic-ai` | 224 MB | 98.3 MB | 77.6 MB | −21% |

Those are subtrees, so the whole image was then re-exported both ways —
`docker buildx build --output type=oci,dest=…,force-compression=true` over a
one-line `FROM ghcr.io/filippolmt/toolbox:latest`, which recompresses the real
published layers without rebuilding them. Summing the layer blobs in the two OCI
manifests:

| | layers | pull |
|---|---|---|
| `gzip -6` (what is published today) | 70 | **1.256 GB** |
| `zstd -19` | 70 | **0.997 GB** |

**−259 MB, −20.6%**, on the arm64 image, and the gzip figure reproduces the live
GHCR manifest to the byte — the two are the same measurement. The subtree table
overstated it at −30% because the subtrees are the compressible end of the image;
the whole-image number is the one to quote. Cold export of all 70 layers at
`zstd -19` took ~42 s, which is not a CI concern. Decompression, which happens on
every pull rather than once per publish, is several times faster than gzip's at
any level.

Taken, as `compression=zstd,compression-level=19,force-compression=true` on the
`outputs:` of the push step in `docker-publish-reusable.yml`. Three things about
it are distribution, not bytes, and are the reason it was decided rather than
merged:

1. **It is a client-compatibility change, and that is the whole decision.** zstd
   layers need Docker Engine ≥ 23 or a containerd-backed runtime; every puller
   older than that stops being able to pull at all. Accepted knowingly — this
   image's audience is on current Docker.
2. **`force-compression=true` is required, not an optimisation.** Without it a
   layer restored from the registry cache keeps whatever compression it was stored
   with, and the image ships half gzip, half zstd. The cost is that the **first**
   publish re-encodes all 70 layers, so it trips the Invalidation Floor gate once
   and wants `[floor-reset]` in that commit message — deliberately, once.

   "Once" is a property of the configuration, not of `force-compression`: the
   same three attributes go on the step's `cache-to:` as on its `outputs:`. Leave
   the cache exporter on the default gzip and every restored layer comes back in
   the wrong encoding on every run, so `force-compression` re-encodes the whole
   image on each publish — ~42 s per leg, forever, instead of once. With the two
   sides agreeing, a restored layer is already in its published form and there is
   nothing to re-encode. The Invalidation Floor gate still trips exactly once,
   because `zstd -19` is deterministic and the second publish reproduces the first
   publish's blobs.
3. **Level is a build-time cost.** Measured on a 950 MB tar of this image's own
   content (`/opt/az` + `@openai` + `/usr/lib/<triple>`), one core:

   | | output | ratio | time | vs gzip -6 |
   |---|---|---|---|---|
   | `gzip -6` (today) | 273.3 MB | 3.47× | 20 s | — |
   | `zstd -3` | 254.6 MB | 3.72× | 1 s | −6.8% |
   | `zstd -9` | 226.2 MB | 4.19× | 3 s | −17.2% |
   | `zstd -12` | 223.9 MB | 4.23× | 6 s | −18.1% |
   | `zstd -15` | 222.5 MB | 4.26× | 16 s | −18.6% |
   | `zstd -19` | 193.1 MB | 4.91× | 60 s | **−29.4%** |

   There is no knee to settle for: 9→15 is flat, and 19 is where zstd's long-range
   matcher turns on and takes another 11 points. The choice is **3** (nearly free,
   about a third of the win) or **19** (the whole win, and ~42 s of export over the
   full image). Pin 19.

   One trap for whoever re-runs this: BuildKit's exporter caches a layer's
   compressed blob per *algorithm*, not per *level*, so a second export at a
   different zstd level silently returns the first level's blobs. The levels above
   were measured outside BuildKit for that reason; the whole-image numbers came
   from a cold store.

## Decision 3 — the three small cuts, and the whole-image duplicate scan

None of these is worth 1% on its own; together they are 46 MB, and each is a
build-time deletion of something provably unreachable.

- **`fetch-docker` strips the Docker CLI** (41.5 → 28.2 MB). A survey of the
  thirteen largest binaries found upstream ships eleven already stripped; `docker`
  is the one real exception (the other, the base image's `node`, is a trap — see
  the backlog note). Go keeps panic `file:line` in `pclntab`, which `strip` does
  not touch. `binutils` is installed **in that stage and not in `fetch-base`** on
  purpose: every fetch stage derives from `fetch-base`, so a package added there
  moves all thirty (ADR 0002).
- **`fetch-oci` drops `pip`, `setuptools` and `pkg_resources` from the venv**
  (−11 MB). The installer is not the installed thing: nothing in the image
  re-resolves that venv, and no module under `oci_cli`, `oci` or `services`
  imports `pkg_resources`. Verified in-build with `oci --help` and
  `oci os ns get --help` — a bare `--version` would not do, it never reaches a
  subcommand tree.
- **The az layer drops the C-extension build toolchain** (−22 MB): `/opt/az/include`,
  the static `libpython*.a` (shipped **twice**, once under `lib/` and once under
  the config dir) and `lib/python*/config-*`. Nothing in this image compiles
  against az's bundled interpreter. `az extension add` is untouched — it installs
  wheels through az's own `pip`, which stays, which is why `pip` was removed from
  the `oci` venv and not from az's.

**The `__pycache__` ordering trap.** Both venv layers purge `__pycache__`, and
both had that purge *above* the commands that verify the install. Every `az` or
`oci` invocation writes bytecode back into its tree, and these layers run as root,
so they can — a verification command placed after the purge silently ships what
the purge just removed. Measured: it put ~14 MB back into `/opt/oci-cli`, more
than the removal had taken out. The purge is now the **last** thing each layer
does.

**What the whole image is shipping twice.** A full scan for byte-identical files
over 1 MB at distinct inodes (so hardlinks, which the layer tar preserves, are not
counted) returns **25.3 MB in total** across the entire image. 20 MB of it was az's
doubled `libpython*.a`, taken above. What remains is not ours: git's
`scalar`/`git-shell`/`git` duplicated between `/usr/bin` and `/usr/lib/git-core`
(8.3 MB, Debian's packaging — hardlinking a dpkg-managed tree for 8 MB is not a
trade worth making), the `playwright` / `@playwright/cli` bundle pair (5.9 MB, two
npm trees installed in two different layers, so hardlinking them would cost a
copy-up larger than the saving) and a 1.1 MB debug copy of tree-sitter's wasm.
**There is no hidden duplication in this image** — that question is now closed.

## Ranked backlog — what is left, and what it is worth

Ordered by (pull saved × safety), which is not the order by disk saved.

| # | Lever | Disk | Pull | Verdict |
|---|---|---|---|---|
| ~~1~~ | ~~zstd publish~~ | 0 | ~~**−259 MB**~~ | **taken** — Decision 2 |
| 2 | Make `cf` and `wrangler` resolve one `workerd` | −151 MB | ≈ −37 MB | **the only one left** — costs a Renovate coupling |
| ~~3~~ | ~~Strip the `docker` CLI~~, ~~venv `pip`~~, ~~az build toolchain~~ | ~~−46 MB~~ | — | **taken** — Decision 3 |
| ~~4~~ | ~~Drop `libgl1-mesa-dri` + `libllvm15` + `libz3-4`~~ | ~~−150 MB~~ | — | **rejected, measured** |
| ~~5~~ | ~~Drop the CJK/emoji font packs~~ | ~~−59 MB~~ | — | **rejected, measured** |
| ~~6~~ | ~~Ship `.pyc` instead of `.py` in the three venvs~~ | — | — | **rejected, makes it bigger** |
| ~~7~~ | ~~Hunt for files shipped twice~~ | — | — | **closed** — 25.3 MB image-wide, 20 of it taken |

**On 2.** `cf` and `wrangler` each carry their own `workerd` — 153 MB and 151 MB,
two builds of the same thing differing only by version, which is why npm's global
install cannot hoist them and why a whole-tree scan finds only 1.4 MB of
*identical-version* duplication across every global package. And npm can never be
made to dedupe them by itself: both sides pin `workerd` **exactly**, not by range
— `wrangler` declares `workerd` as a hard dependency, `cf` reaches it through
`miniflare`, and each `miniflare` in turn pins one exact `workerd`. Two exact pins
that differ never intersect, so installing both in one `npm install -g` changes
nothing. Aligning them means pinning two independently-Renovate-bumped packages to
the version pair whose transitive `miniflare` happens to agree, and re-pinning on
every bump of either. That coupling costs more maintenance than 37 MB of pull is
worth until someone says otherwise.

**On stripping, and the one binary that must not be stripped.** The survey behind
Decision 3 found a second candidate with real symbol weight: the base image's own
`node` (122.9 → 103.0 MB, −16%). **It is a trap.** `node` lives in the `node:24`
base layer, so a final-stage `RUN strip` copies 103 MB *up* into a new layer while
the original 123 MB stays underneath — a net **+103 MB**. `docker` is real only
because `fetch-docker` builds it into its own `/out` and the strip happens before
the `COPY --link`. The general rule: **deleting from a layer you did not create
makes the image bigger.** It is why every cut in Decision 3 lives in the same layer
that produced the bytes.

## Measured and rejected

The three that looked best on paper. Each is recorded so it is not re-proposed.

**The mesa/LLVM stack is inert here, and still cannot go.** `playwright
install-deps chromium` costs 282 MB for a browser **that is not in the image** —
no `chromium` binary, no `~/.cache/ms-playwright`; the browser is fetched at
runtime. `libllvm15` (106 MB) is pulled by nothing but `libgl1-mesa-dri` (23 MB),
which also drags `libz3-4` (21 MB). Tested by installing Chromium at runtime and
screenshotting a page carrying Latin, CJK, emoji and a live WebGL canvas, before
and after purging exactly those three: the two PNGs are **byte-identical**, and
`WebGL.RENDERER` reads `WebKit WebGL` both times — Chromium's own SwiftShader,
mesa never in the path. So the 150 MB is genuinely dead for rendering.

It stays anyway, because the dependency chain is atomic: purging `libgl1-mesa-dri`
takes `libglx-mesa0`, `libglx0`, `libgl1` and — at the top — **`xvfb`**, which
`install-deps` installed on purpose and which is the only way to run a *headed*
browser in this image. Trading `xvfb-run` for 150 MB is removing a capability, not
shrinking an image. (Also worth knowing before anyone reaches for `--auto-remove`
here: it additionally takes `libx11-xcb1`, `libxcb-dri3-0`, `libxshmfence1` and
seven more X libraries that Chromium itself links.)

**The font packs are load-bearing.** Same harness, purging `fonts-wqy-zenhei`,
`fonts-unifont`, `fonts-ipafont-gothic`, `fonts-noto-color-emoji` and
`fonts-freefont-ttf`: the screenshot changes. 59 MB buys correct rendering of
every non-Latin page anyone screenshots, which is the feature.

**Precompiling Python to `.pyc` makes the image bigger, not smaller.** The
tempting idea for 1.07 GB of Python source: `compileall -b`, delete the `.py`,
ship bytecode. Measured on `/opt/az/.../azure` (8391 modules, all compiled clean):

| | raw | gzip -6 | zstd -19 |
|---|---|---|---|
| source `.py` | 218.8 MB | 21.0 MB | 6.6 MB |
| `.pyc` only | 222.3 MB | 50.2 MB | 16.4 MB |
| | **+1.6%** | **+139%** | **+148%** |

Bytecode is not smaller than source, and it is far less compressible: Python
source is repetitive text that gzip takes 10× off, bytecode is dense binary that
gives up 4×. On the denominator that matters — pull — the idea more than doubles
the cost. This closes the whole family: `.pyc`-only trees, zipapp/`.pyz` packing,
and any other "compile the Python down" proposal.

## New technology surveyed, and why it is not here

- **`zstd:chunked` / eStargz / SOCI (lazy pulling).** The genuinely new answer to
  "the image is 1 GB": don't transfer it. These formats embed a TOC in the blob
  (or alongside it) so a container starts on the handful of files it needs and
  fetches the rest on demand. For a dev image this size the effect on
  `toolbox shell` cold start would be larger than every lever in the table above
  combined. **Not usable here, and the gap is wider than "not enabled yet".**

  Each needs a **snapshotter on the client**, and Docker Desktop offers no
  supported way to install one. eStargz wants the `containerd-stargz-grpc`
  daemon, a `proxy_plugins` entry in *containerd's* own config, FUSE, and a
  `daemon.json` storage-driver switch — a Linux/systemd-only procedure, per
  [stargz-snapshotter's INSTALL.md](https://github.com/containerd/stargz-snapshotter/blob/main/docs/INSTALL.md).
  `zstd:chunked` has **no Docker implementation at all**: it is the
  containers/storage stack — Podman, CRI-O, Buildah
  ([Red Hat](https://www.redhat.com/en/blog/faster-container-image-pulls)).
  SOCI needs `soci-snapshotter-grpc` as a containerd proxy plugin plus a
  separately built index, and is driven through `nerdctl`; its home is AWS
  Fargate/ECS.

  **The containerd image store is not the feature.** It is the default in Docker
  Desktop and on fresh Docker Engine installs, and Docker's own wording is
  careful — it is "a prerequisite for unlocking … support for using containerd
  snapshotters … such as stargz for lazy-pulling"
  ([docs](https://docs.docker.com/desktop/features/containerd/)). What it
  delivers today is multi-platform images, attestations and Wasm. No lazy pull.

  **One citation to distrust.** stargz-snapshotter's own
  [integration.md](https://github.com/containerd/stargz-snapshotter/blob/main/docs/integration.md)
  says Docker Desktop's containerd image store "uses stargz-snapshotter". That
  line is from 2022, is contradicted by the same repository's README, and is the
  source of most second-hand claims that Desktop has lazy pulling. It does not.
  Check Desktop's release notes before believing any successor to that claim.

  Revisit only on a Docker announcement that names a snapshotter, not on one that
  names containerd.
- **`slim` (ex `docker-slim`).** Traces a running container and rebuilds the image
  from the files actually touched. It is designed for single-purpose service
  images; a dev shell's whole point is that the next command is unpredictable, so
  the trace is worthless here.
- **Wolfi / apko / Chainguard bases.** Would replace a 108 MB Debian base and a
  153 MB `node:24` base — real, but 5% of the problem, against re-validating every
  apt-installed tool in the image on musl-adjacent libc assumptions.
- **`COPY --exclude`.** Useful, but every fetch stage already copies a `/out` it
  built itself, so there is nothing to exclude.
- **Splitting the image into a core + lazily-installed cloud CLIs.** Would move
  1.07 GB of disk off the default path without removing a capability, at the cost
  of a slow first `az`/`gcloud`/`oci`. A product decision, not a build one; noted
  so it is not rediscovered as a build trick.

## Outcome

Rebuilt and smoke-tested on arm64 with Decisions 1 and 3 in (Decision 2 changes
nothing locally — it is a registry encoding):

| | disk | pull |
|---|---|---|
| before | 4.679 GB | 1.256 GB |
| after Decisions 1 + 3 | **4.509 GB** | — |
| after Decision 2, on the same content | — | **0.997 GB** |

**−170 MB of disk** — a little more than the four cuts predict, because the two
`__pycache__` reorderings were paying for themselves twice — and **−259 MB of
pull**, together about **−21% of what a developer waits for** on a cold
`toolbox shell`. `smoke-test.sh`: 109 passed, 0 failed, 0 skipped.

## Follow-ups

1. **The first publish after Decision 2 carries `[floor-reset]`.** `force-compression`
   re-encodes every layer once, so the Invalidation Floor gate will report all 70
   moved, correctly, exactly once. Do not chase it.
2. ~~Run the headless-Chromium screenshot experiment.~~ Done; see *Measured and
   rejected*.
3. ~~Audit `/usr/lib/<triple>`, the one bucket nobody had read line by line.~~
   Done: of 320 MB it is `libLLVM-15.so.1` 111 MB (above), `libicudata` 31 MB,
   the whole `dri/` directory 24 MB — its ~30 `*_dri.so` are **hardlinks to one
   megadriver, and the layer tar preserves them**, so it costs one copy, not
   thirty — and ~154 MB of ordinary system libraries with no passenger among
   them. Nothing to take.
