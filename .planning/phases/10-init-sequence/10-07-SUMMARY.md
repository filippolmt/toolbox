# Phase 10 Plan 07: init.d iterator wiring + smoke-test bijection — SUMMARY

**Plan:** 10-07 (KEYSTONE — first commit where the runtime image actually
invokes the 5 extracted init scripts at boot).

**Status:** Complete.

## What landed

Two atomic feat commits + this summary, all on branch
`gsd/phase-10-init-sequence`:

| Commit | Subject |
|--------|---------|
| `5fa1d36` | feat(10-07): wire init.d iterator with failure envelope in entrypoint.sh |
| `0487dcb` | feat(10-07): assert init.d bijection + executability in smoke-test.sh |
| (pending) | docs(10-07): add SUMMARY.md for plan 10-07 |

Files modified: 2.

## Iterator block (entrypoint.sh)

Inserted between the `oci` credential check (line 73 `fi`) and the
user-defined startup hooks comment (line 105 in the post-edit file). 30
lines added (lines 75-104). Verbatim per plan, including:

- `INIT_D="/usr/local/lib/toolbox/init.d"` — single source of truth for
  the in-image init.d directory (matches Dockerfile COPY target from
  10-01).
- `TOOLBOX_INIT_LOG_DIR="$HOME/.toolbox-state/init"` — Pitfall 1: the
  container path is `.toolbox-state` (the `state` bind-mount target),
  NOT `.toolbox/state`. The log dir is created on demand with
  `mkdir -p`.
- Per-script log path `${TOOLBOX_INIT_LOG_DIR}/${_name%.sh}.log`.
- `if ! bash "$f" 2>"$_log"; then …; else …; fi` — Pitfall 9: the
  `if !` form neutralises the outer `set -e` so a single failing init
  script never aborts boot; startup hooks and `exec "$@"` always run.
- On failure: marker line `<name>: failed (log: <path>)` plus tail-5
  of stderr indented 6 spaces (`sed 's/^/      /'`) for inline
  diagnostics.
- On success: `rm -f "$_log"` so a clean boot leaves no stray files.
- `unset _name _log f` after the loop, plus `unset INIT_D
  TOOLBOX_INIT_LOG_DIR` after the outer `if [ -d "$INIT_D" ]`, to keep
  the entrypoint env clean before `exec "$@"`.

## Bijection block (smoke-test.sh)

Appended 32 lines at the end of `smoke-test.sh` (lines 244-275),
*after* the existing UID-mapping block. Verbatim per plan; kept as a
**separate `docker run --rm "${IMAGE}" bash -c '…'`** invocation
(mirrors the signal-handling and UID-mapping blocks at the bottom of
the file) rather than folded into the main `docker run … bash -c '…'`
at lines 10-209.

Asserts D-12 (executable bit) and the catalog count contract (>= 5
InitScripts) **inside the built image**:
- `INIT_D=/usr/local/lib/toolbox/init.d` exists.
- Every `*.sh` in `INIT_D` is executable (mode-0755 surviving the
  embed.FS strip + Dockerfile `chmod 0755` from 10-01).
- `count >= 5` (catalog declares 10/20/30/40/50 = 5 InitScripts).

The Go-side bijection (`TestCatalogInitDBijection`) covers the
catalog ↔ embed.FS direction; this shell-side check covers the
Dockerfile COPY ↔ in-image direction. Together: full round-trip
coverage from `internal/catalog` through `embed.FS` through the
runtime image.

## End-to-end verification

All four checks green on this worktree against the freshly built
`toolbox:local` image (built from the commits in this plan):

| Step | Command | Result |
|------|---------|--------|
| go-lint | `make go-lint` | OK (`0 issues.`) |
| go-test | `make go-test` | OK (all packages pass, no `[FAIL]`) |
| build | `docker build --build-arg TARGETARCH=arm64 -f internal/build/assets/Dockerfile -t toolbox:local internal/build/assets` | OK (image `a8bcd78390c4`, 132 layers) |
| smoke-test | `bash internal/build/assets/smoke-test.sh toolbox:local` | OK (55 passed / 0 failed / 0 skipped + signal + UID + **`OK: 5 init.d scripts present and executable`**) |

The 5 init scripts present in the image — confirming bijection on the
COPY ↔ runtime side — are the full set declared by the catalog:
`10-rtk.sh`, `20-cf.sh`, `30-graphify.sh`, `40-playwright-cli.sh`,
`50-mcp-plugins.sh`.

## Deviations

### 1. `make build` requires `--build-arg TARGETARCH=arm64` on this host

**Found during:** Task 2 verification (post-commit `0487dcb`, when
running `make build`).

**Issue:** `make build` (which calls `docker build -f internal/build/assets/Dockerfile -t toolbox:local internal/build/assets`)
fails on the legacy classic Docker builder at Step 6/132 with:

```
/bin/sh: 1: TARGETARCH: parameter not set
```

The `rtk-builder` stage references `${TARGETARCH}` (gating amd64
tarball vs arm64 cargo-install). On BuildKit, `TARGETARCH` is
auto-populated. On the classic builder it is not, and `set -eux` makes
the absent variable a hard error. Docker on this worktree-host is
29.4.3 with `buildx` missing, so `DOCKER_BUILDKIT=1` errors with
`BuildKit is enabled but the buildx component is missing or broken`.

**Resolution:** none on this worktree — the issue is **preexisting**,
not introduced by 10-07. The Dockerfile itself is unchanged in this
plan; the iterator wiring lives in `entrypoint.sh` which is COPYed at
a much later layer. Local verification used the explicit build-arg
form instead. CI uses BuildKit (via `docker/build-push-action`) so
this never surfaces in `.github/workflows/ci.yml`.

**Follow-up (out of scope for 10-07):** either install `buildx` on
the dev host so `make build` matches CI, or default the Makefile's
`build` target to `--build-arg TARGETARCH=$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/')`
so the classic builder works too. Logged here for the orchestrator;
not blocking phase 10.

### 2. Worktree filesystem read anomaly during smoke-test verification

**Found during:** Task 2 verification (running `bash smoke-test.sh`
after commit `0487dcb`).

**Issue:** On this Claude-Code worktree filesystem, the file
`internal/build/assets/smoke-test.sh` reported the correct size
(10367 bytes / 275 lines via `stat`, `du -b`, `wc -c`, and `cat | wc
-c`), but `cp` and `bash <file>` only consumed the first 9383 bytes
(243 lines), which truncates the script right after the UID-mapping
block's closing `'`. As a result the freshly-appended bijection block
silently never ran, even though it was present in the file and
already committed in `0487dcb`.

**Diagnosis:** the file content via git (`git show HEAD:…`) and via
`cat` is intact (10367 bytes, ends with `exit $fail \n '`). The
mis-read is a worktree-FS-layer caching glitch, not a content bug.
Re-writing the file with `cp /tmp/smoke-cat.sh internal/build/assets/smoke-test.sh`
(where the source had been produced by `cat`) refreshed the FS view;
subsequent `cp` and `bash` reads then returned the full 10367 bytes
and the bijection block ran successfully (`OK: 5 init.d scripts
present and executable`).

The committed git object SHA is unchanged (`0487dcb` already contains
the full block) — this was a worktree-FS read artifact only, not a
data loss event. CI checks out fresh on a clean FS so the issue
cannot recur there.

**Resolution:** logged as Rule-3-style auto-recovery (worktree FS
hiccup unblocking verification). No source change needed.

## Commit metadata

- Commit 1 (iterator): `5fa1d36`, 1 file changed, 30 insertions(+).
- Commit 2 (smoke-test bijection): `0487dcb`, 1 file changed, 32 insertions(+).
- Branch tip after Commit 2: `0487dcb` on `gsd/phase-10-init-sequence`.

Co-Authored-By: Claude Opus 4.7 (1M context).
