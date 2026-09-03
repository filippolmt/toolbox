# Image build internals

Maintainer notes on how the runtime image is built: Dockerfile layout, layer ordering, version pinning, and the build-time decisions behind individual tools. User-facing image knobs (`image`, `registry_mirror`, `pull`) live in [configuration](../configuration.md#image-selection).

## Build layout: parallel fetch stages + frequency-ordered tail

The Dockerfile is structured for minimal rebuild time, not for linear readability:

- **Every static-binary tool lives in its own `fetch-<tool>` stage** (parent: `fetch-base`, a `debian:bookworm-slim` with curl/CA/git). Artefacts land under `/out` mirroring the final filesystem; the final stage imports them with `COPY --link --from=fetch-<tool> /out/ /`. Consequences: cold builds download all tools in parallel; helper packages a fetch stage installs (unzip, python3, jq) never reach the final image.
- **Those COPYs are declared below the whole RUN tail**, immediately above `USER toolbox`. `--link` builds each layer independently of the filesystem beneath it, so the position changes nothing about their content and everything about what invalidates them — a bump moves that one layer instead of every RUN under it. Until [ADR 0002](../adr/0002-layer-ordering-by-invalidation-floor.md) they sat above the tail, and a one-line version bump moved half the image — that ADR carries the measurements. There are no exceptions: every fetch stage writes at the final path and sets its own permissions, so no tail RUN has to relocate or `chmod` a copied tree — `fetch-omz` clones straight into `/out/home/toolbox` and `chmod -R a+rwX`es it there, which is what let its COPY move down too. **Never add a RUN below the COPY block and never let a tail RUN read a copied file** — both drag the COPY back above the tail for everyone. This is what [*Invalidation Floor*](../../CONTEXT.md#invalidation-floor) names; the moved-layer gate in `docker-publish-reusable.yml` measures it on every publish.
- **Every fetch stage ends with `freeze-mtimes`**, which freezes mtimes under `/out` (`touch -h -d @1`). `COPY --link` folds mtime into the layer digest, so without it a downloaded artefact carries the wall-clock time of the build that fetched it and the digests are stable only while BuildKit reuses the stage — losing the registry cache would republish the entire image with no Dockerfile change. `freeze-mtimes` itself is installed outside `/out`, so it never reaches the final image.
- **Final-stage RUN layers (apt/pip/npm — can't fan out) are ordered rare→frequent** by measured Renovate cadence (≈6-month window: graphifyy 100 bumps, claude-code 95, wrangler 37, pnpm 36, codex 34, playwright-cli 10, cf 8, azure 7, playwright 7, pyright 5, typescript 2). `oci` (20) and `codegraph` (15) used to sit in here and no longer do: sorting cannot make a tool near the top cheap, so both were moved into their own `fetch-*` stage, where a bump costs one `--link` layer whatever its position. That is the escape hatch for any tail tool whose cadence outgrows its depth — `fetch-codegraph` must use the same node image as the final stage, and `fetch-oci` installs into a venv at `/opt/oci-cli` rather than the system site-packages. Least-bumped first, most-bumped last, so a bump rebuilds only what sits below it: the daily graphify and claude-code bumps move 2-3 substantial layers where they used to move the whole tail. Two orderings inside that are load-bearing rather than cosmetic — `oci` stays above `graphifyy` so the two pip installs keep resolving their shared dependencies in the same order (the build verifies graphify *before* oci, so an inversion would pass green), and `playwright` stays above `playwright-cli`. Both fall out of the cadence order anyway. This ordering covers only bumps that land *in* the tail; the majority, which land in a fetch stage, are handled by the point above. Re-measure with `git log --since=… -p -- internal/build/assets/Dockerfile | grep '^+ARG'` before reshuffling.

  The ordering only pays off if **each version ARG is declared immediately above the single RUN that consumes it**. An ARG in scope enters the cache key of every RUN below it — that is the `|N` prefix `docker history` shows per layer — so while all 14 were declared as one block at the top of the stage, all 21 tail RUNs were keyed on all 14 versions: any single bump rebuilt the entire tail and the position of a RUN bought nothing. The gate measured 16 layers above 1 MB and 694 MB on the transition that surfaced it — a transition carrying an `OCI_VERSION` bump plus an unrelated asset change, and billed by a gate that had not compared since the previous publish was cancelled, so read the figure as the scale of the churn rather than the price of one bump (ADR 0002, Follow-up). `TestFinalStageARGsScopedToTheirRUN` holds the placement, and scoping alone is not enough to clear the gate — see the follow-up.
- **`make build` seeds from the CI registry cache** (`ghcr.io/filippolmt/toolbox:buildcache-main`, written by `docker-publish.yml` with `mode=max`, multi-arch — includes the arm64 rtk cargo build). First build on a fresh machine ≈ a layer pull. Cache-import failures are warnings, so offline builds still work.
- Version checks (`<tool> --version`) run **inside the fetch stage** — they catch wrong-arch / GLIBC-mismatch before the smoke test, same as the old in-layer checks.

## Host UID mapping

The CLI runs the container with `--user $(id -u):$(id -g)`. Because the runtime UID rarely matches the baked `toolbox` user (UID 1000), `/home/toolbox` is made world-writable in the image. Don't revert to a fixed UID without understanding why — host file ownership would invert and writes inside `~/.toolbox/` would fail for anyone whose host UID isn't 1000.

## SSH host-key trust

The image bakes `/etc/ssh/ssh_config.d/10-toolbox.conf` (embedded asset `ssh_config.toolbox`, `COPY --link`'d in the final stage) with `StrictHostKeyChecking accept-new` and `UserKnownHostsFile ~/.toolbox-state/known_hosts`. This fixes the git-over-ssh deadlock: the `ssh` credential mount is a read-only symlink to the host (by design — keeps host `ssh-keygen` in sync), so the Debian default `ask` policy prompts on first contact and then can't persist the accepted key to the read-only `~/.ssh/known_hosts`, re-prompting forever. `accept-new` removes the prompt while still rejecting *changed* keys; pointing `known_hosts` at the writable, persistent `state` mount lets keys persist across sessions. User-facing description in [mounts](../mounts.md#ssh-host-key-trust-git-over-ssh).

The drop-in is loaded via `Include /etc/ssh/ssh_config.d/*.conf` in `/etc/ssh/ssh_config`. ssh uses the *first-obtained* value per keyword, so the Include must sit at the top of the base config (ahead of any built-in default) for `accept-new` to win — Debian's default already places it first, but a final-stage `RUN` re-asserts it idempotently rather than silently depending on upstream not moving it. Rejected alternatives: making the `ssh` mount writable (breaks the symlink host-sync design); boot-time `ssh-keyscan` seeding of common forges (adds a network dependency at boot plus a new `init.d` script → bijection/smoke churn, and `accept-new` already removes the prompt). The smoke test asserts the policy is effective via `ssh -G` rather than trusting the COPY landed.

## Passwordless sudo

The base apt layer installs `sudo`, and the user-setup layer drops `/etc/sudoers.d/toolbox` with `ALL ALL=(ALL:ALL) NOPASSWD: ALL` (`!requiretty`, `!fqdn`). The runtime UID is the host's and rarely matches the baked `toolbox` user, so the rule is deliberately UID-agnostic — it matches whatever UID the entrypoint injects into `/etc/passwd`. This lets `sudo apt-get update && sudo apt install …` (or any root op) work inside a running container without baking the tool into the image (apt lists aren't baked, so `update` runs first). Safe because the container is `AutoRemove` (see [Container teardown](container-lifecycle.md#container-teardown)): everything installed at runtime vanishes on exit. **Caveat:** sudo writing into bind-mounted host paths (`/workspace`, `~/.toolbox/*`) produces `root:root` files on the host — escalate for in-container/system state, not for editing mounted project files. `visudo -cf` validates the drop-in at build; the smoke test asserts the `sudo` binary is present and setuid root.

## Docker CLI checksum

The `fetch-docker` stage of `internal/build/assets/Dockerfile` installs the static Docker CLI binary without a SHA256 verification step because Docker doesn't publish `.sha256` files for those releases. Version pin + HTTPS is the only guard. Tracked as accepted risk T-01-08.

## Tool version pinning

Every external binary in the Dockerfile is pinned by version, and Renovate bumps them. SHA256 verification is applied only when upstream publishes a checksums file: the fetch stage downloads it and pipes through `sha256sum -c -` (e.g. `fetch-gh`, `fetch-helm`, `fetch-kubectl`, `fetch-rtk`), which self-heals across bumps and re-tags. Tools whose upstream ships **no** checksums file (bat, fd, eza, zoxide, shellcheck, shfmt, Docker CLI, gcloud) download over HTTPS only. Hand-pinned per-arch SHA256 literals were removed: they broke the build on every version bump and even when upstream re-tagged a release in place without changing its version, while providing no guarantee a self-authored hash could actually deliver. Adding a new tool touches four files, plus one or two more when it needs a boot script or persists state — the `add-cli` skill drives the whole sequence:

1. Install layer + pinned `ARG <TOOL>_VERSION` in `internal/build/assets/Dockerfile` (own `fetch-<tool>` stage for a static binary, final-stage `RUN` for apt/npm/pip).
2. New row in `internal/catalog/catalog.go` `Entries` — `TestCatalogDockerfilePresence` requires the `Key` to appear as a token in the Dockerfile.
3. `check_optional` line in `internal/build/assets/smoke-test.sh`, plus the derived count literals when the tool adds an `init.d` script or a vendor completion.
4. `customManagers` entry in `renovate.json`, or the pin silently freezes.
5. (optional) `init.d/<NN>-<tool>.sh` matching the row's `InitScript`, when the tool needs a boot step.
6. (optional) A `~/.toolbox/<tool>` bind in `internal/mountplan/defaults.go`, when the CLI stores credentials or state that must survive `toolbox stop`.

There is no per-tool opt-out: every CLI is installed unconditionally. The `ARG INSTALL_<TOOL>` build-arg pattern was removed (see [#276](https://github.com/filippolmt/toolbox/issues/276)). Use `inherit_host_auth:` in `.toolbox.yaml` to share host credentials with the container — see [inherit-host-auth](../configuration.md#inherit-host-auth).

## rtk arm64 is built from source

Dockerfile `rtk-builder` stage + final-stage `COPY --link`. Upstream only ships `aarch64-unknown-linux-gnu`, linked against a newer GLIBC than the Debian base image carries — the prebuilt binary aborts with a `'GLIBC_<ver>' not found` loader error. There is no `aarch64-unknown-linux-musl` release, so the arm64 binary is compiled in-tree instead.

Fix: multi-stage build. A `rust:1-slim-bookworm` stage runs `cargo install --git rtk-ai/rtk --tag v${RTK_VERSION} --locked` against the bookworm sysroot (so the resulting binary ABI-matches the runtime), version-checks it in-stage, and the final stage imports it with a single `COPY --link --chmod=0755`. The same stage handles the amd64 tarball download too.

The base image can't move to Debian trixie yet because the Microsoft Azure CLI apt repo currently has no trixie suite.

## Rust base image tag scheme

`<ver>-slim-<distro>`, not `<ver>-<distro>-slim`. Docker Hub publishes `rust:1-slim-bookworm` (correct) but **not** `rust:1-bookworm-slim` (404). PR #89 hit this — the rtk-builder stage failed at image-pull time. When bumping or referencing the rust base, use `<ver>-slim-<distro>` (or bare `<ver>-slim` for the default trixie variant when we eventually move).

## Slim Rust images ship no `curl` / `ca-certificates`

`rust:1-slim-bookworm` contains cargo + git but nothing to fetch tarballs with. The rtk-builder stage installs them via apt before the amd64 tarball path. If you copy the pattern for another tool (e.g. building a Rust binary from source), replicate the apt install — it doesn't propagate from the base.

## Homebrew

Installed via shallow tag clone (`ARG HOMEBREW_VERSION`) at the **default Linux prefix** `/home/linuxbrew/.linuxbrew` — bottles (pre-built binaries) only work there; any other prefix forces source builds, explicitly unsupported upstream ("pick another prefix at your peril"). The official installer script is unusable in a Dockerfile `RUN`: it refuses root and clones unpinned `main`. The layer reproduces the installer's layout manually (repo at `…/Homebrew` + `bin/brew` symlink) and ships the pre-built `_brew` zsh completion from the clone.

Variable host UID handling follows the `/home/toolbox` pattern: `chmod -R a+rwX /home/linuxbrew` so any runtime UID can write the prefix, plus `git config --system --add safe.directory /home/linuxbrew/.linuxbrew/Homebrew` — the clone is root-owned and the runtime UID is arbitrary, so without it every git-touching brew op dies with "dubious ownership". System gitconfig (not `--global`) so the entry stays container-local and out of the user's host-synced `~/.gitconfig` — see [System git settings](#system-git-settings) for the layer it shares and for why it survives the entrypoint's wildcard registration.

Runtime semantics:

- **Ephemeral installs** — `brew install` writes into the non-mounted prefix; everything vanishes on container exit, exactly like `sudo apt install`. Intentional: no `~/.toolbox/brew` bind (a potentially multi-GB prefix over a macOS bind mount would be slow and defeats the disposable-workspace model).
- **First `brew install` downloads Portable Ruby + formula API JSON** (~30–60 s, network required) — once per container, since containers are AutoRemove-ephemeral.
- `HOMEBREW_NO_ANALYTICS=1` + `HOMEBREW_NO_AUTO_UPDATE=1` baked in image ENV (privacy + pin-everything policy); overridable per-session.
- Debian is Homebrew **Tier 2** (fully functional, just outside upstream's Tier 1 CI matrix, which is Ubuntu). Bottles need a glibc floor the Debian base satisfies.
- The clone is shallow: `brew update` instructs `git fetch --unshallow` first. Acceptable — the version is image-pinned and auto-update is off; the message is self-explanatory for users who insist.

PATH: image `ENV` prepends `…/.linuxbrew/bin:…/.linuxbrew/sbin` (covers non-interactive `docker exec`); interactive zsh additionally evals `brew shellenv` for `HOMEBREW_PREFIX`/`MANPATH`/`INFOPATH` (idempotent w.r.t. the ENV PATH entry). Private GitLab taps authenticate via the glab credential helper — see [GitLab git credential helper (glab)](shell-start.md#gitlab-git-credential-helper-glab).

## System git settings

One layer writes `/etc/gitconfig` at build time, for git settings that are properties of **the git this image ships** rather than of anything a running container knows. The runtime counterpart is `entrypoint.sh`, which registers what only the container can know — `safe.directory` for the bind mounts, the bridge credential helper. System scope in both places, never `--global`: `~/.gitconfig` is a host mount. → [git safe.directory](shell-start.md#git-safedirectory-dubious-ownership)

- `safe.directory` for the root-owned Homebrew clone, described under [Homebrew](#homebrew). The entrypoint's wildcard entry subsumes it; it stays because that registration is deliberately non-fatal, and brew is the one thing in the image that breaks on *every* git call when it does not land.
- `http.version = HTTP/1.1`, without which HTTPS clones and fetches from github.com fail most of the time — pinned here **and** in `fetch-base`, where the build's own clones need it.

That second one deserves its measurement, because the obvious readings of it are all wrong. git's protocol v2 issues a `POST /git-upload-pack`, and the git apt ships here mis-reads github.com's HTTP/2 response to it: the ref listing comes back truncated or as a 401, so the command dies with `fatal: expected flush after ref listing` or `fatal: could not read Username for 'https://github.com'` — the second of which sends you hunting for a credential problem that does not exist.

**It fails per request, not always**, which is the first thing to know before testing anything here: 15 of 20 `ls-remote` runs against github over h2, against 0 of 20 with the pin. So a single green run proves nothing, a clone that issues several requests is nearly certain to die somewhere, and a retry can always get lucky — the shape that makes this look like a network problem. Isolated one axis at a time, from a shell with every `GIT_CONFIG_*` cleared:

| what | result |
|---|---|
| github, git defaults (v2 + h2) | fails |
| github, v2 forced onto HTTP/1.1 | works |
| github, protocol v0 over h2 | works |
| gitlab.com, git defaults | works |
| **bare `debian:bookworm-slim`, no config of ours, github defaults** | fails identically |
| **a much newer git, same Docker network, github defaults** | works |
| **curl, same POST with git's headers, over h2** | 200 |

So it is neither this image's network nor a github outage nor anything the container does to h2 — it is git's own handling of that response, and the base image's git is simply old enough to have it. That also settles the scope: the trigger is on git's side, so github is where it was found rather than the only place it can happen — gitlab.com and codeberg.org negotiate h2 as well and merely happen to survive it. Hence a global pin rather than a `[http "https://github.com"]` section. It costs git nothing measurable, which is why it is not worth narrowing: git opens one or two connections per operation, so h2 multiplexing buys it almost nothing. Once the base image carries a git that reads h2 correctly the entry is dead weight rather than a hazard, and it can go.

### The same pin is needed twice

`fetch-base` carries it too, and that half is the one that bites hardest: `fetch-omz` and `fetch-brew` are the only stages that *clone* rather than curl a release artefact, and between them they fetch six times — at three-in-four per request, **the build effectively cannot complete** without the pin. Both died on `expected flush after ref listing` when it was found. Two things kept it latent: the registry build cache, since the failure surfaces only on a run cold enough to rebuild those stages — so a warm CI and a warm laptop agree that everything is fine right up until neither is warm — and the per-request odds, which leave a re-run able to pass and send you looking for flakiness. It was found by pruning the local build cache for unrelated reasons.

`fetch-base` is also the layer that installs git, which makes it the honest home for the one thing this git gets wrong. Adding to that `RUN` invalidates every fetch stage once; it lowers no [Invalidation Floor](../adr/0002-layer-ordering-by-invalidation-floor.md), since that stage already sits at the bottom of the graph and changes about as rarely as anything in the file.

Both pins are held by `TestGitHTTPVersionPinned` (`internal/build/git_http_version_test.go`), bracketed per stage so that two pins in the final stage cannot stand in for a missing one in `fetch-base`.

### Why the assertions are needle-shaped here

`smoke-test.sh` asserts the value rather than the behaviour, which is the weaker kind of assertion — deliberately. Reproducing the failure needs a real HTTP/2 peer, and that smoke test makes no network calls at all; that property is worth more than one behavioural check. Same for the Go test. Reproduce by hand instead, from a shell with every `GIT_CONFIG_*` cleared: `git -c http.version=HTTP/2 ls-remote https://github.com/git/git`.

## DO_NOT_TRACK + claude wrapper

Image sets `ENV DO_NOT_TRACK=1` ([consoledonottrack.com](https://consoledonottrack.com) convention) — honored by bun, playwright, and most JS toolchains users run inside the container (next, astro, turbo, …). Claude Code honors it too, but as a **telemetry umbrella**: it also shuts down the Statsig channel that doubles as feature-flag delivery, which breaks Remote Control and preview rollouts (`/doctor` reports "Feature-flag evaluation enabled (disabled by DO_NOT_TRACK)"). Same failure mode as `DISABLE_TELEMETRY` — see the "Claude Code env knobs" comment block in the Dockerfile for why that flag is intentionally unset.

Fix: the claude install layer replaces the npm `/usr/local/bin/claude` symlink with a `#!/bin/sh` wrapper that does `exec env -u DO_NOT_TRACK <real-cli> "$@"` — the var is stripped for the claude process only, everything else in the container stays opted out. Don't "simplify" the wrapper back to a plain symlink; the smoke test (`claude DO_NOT_TRACK wrapper`) asserts the `env -u` line is present.

Known cost: children spawned by claude's Bash tool inherit the stripped environment, so JS tooling launched *from inside* a claude session loses the opt-out. Accepted trade-off — the alternative (no exemption) breaks Remote Control entirely.

## Two Docker version streams

`DOCKER_CLI_VERSION` in the Dockerfile pins the CLI binary inside the container (currently 29.x); the SDK the CLI launcher uses lives in `go.mod` as the moby modules `github.com/moby/moby/client` (own v0.x series) + `github.com/moby/moby/api` (versioned after the Engine API, v1.x) — the deprecated `github.com/docker/docker` module is gone. The client negotiates the API version by default, so version drift between CLI binary, SDK, and daemon is expected and handled. Don't try to "align" them numerically.

## Go version single source of truth

Unlike the two Docker streams, the Go version is deliberately **aligned everywhere from one anchor**: the `toolchain` directive in `go.mod`. Renovate bumps `toolchain` by default (the `go` directive stays the lower compat floor and is not auto-bumped). Everything else derives:

- **CI test/lint/release** — `actions/setup-go` with `go-version-file: go.mod` reads the `toolchain` directive (precedence over `go`).
- **Build/test container** — the one exception, see below: `GO_IMAGE_VERSION` in the `Makefile` pins the `golang:` tag independently, under Renovate's `docker` datasource.
- **Runtime image** — the Dockerfile's `ARG GO_VERSION` is a fallback only; every real build path injects the go.mod value: `make build` passes `--build-arg GO_VERSION=$(GO_VERSION)`, `toolbox build` injects it from `runtime.Version()` (the toolchain that compiled the CLI — see `mergeBuildArgs` in `internal/build/build.go`), and the image-build workflows read go.mod and pass it as a build arg.

### The one exception: the `golang:` Docker tag

Everything above derives from go.mod because every consumer resolves the version against a source that exists the instant the release does: the Go release index (`setup-go`) or a `go.dev` tarball (the runtime image). Docker Hub is not such a source — it publishes `golang:<patch>` days later.

Deriving the tag from go.mod therefore coupled an automergeable dependency bump to an artifact Renovate had not checked. That is not hypothetical — it landed in #681: a `toolchain` bump to a patch Docker Hub had not published yet went green (setup-go and the tarball both had it), auto-merged, and broke `make go-test` / `make build` on `main` with `golang:<patch>: not found`. So `GO_IMAGE_VERSION` is pinned in the `Makefile` with its own `docker`-datasource Renovate manager: Renovate can only open that bump once the image is real. A gap between the two literals is harmless — `GOTOOLCHAIN` fetches the newer toolchain inside the container.

The rule that survives: don't re-pin the Go version in the Dockerfile, and don't add a Renovate manager for `GO_VERSION` — bump `toolchain` in go.mod and it flows everywhere except that one tag. Because the image's Go is derived from go.mod (not from a Dockerfile literal), `docker-ci.yml` also triggers on `go.mod` and includes it in the arm64 `arch_coverage` filter — a toolchain bump pulls a per-arch Go tarball (`go${GO_VERSION}.linux-${TARGETARCH}.tar.gz`), so both arches are smoke-tested in the PR.

`golangci-lint` follows the same one-literal rule: `GOLANGCI_VERSION` in the `Makefile` is the source (Renovate-bumped); the lint CI job reads it from the Makefile instead of carrying its own literal.

## Tools removal

The `tools:` block in `.toolbox.yaml` and the `ARG INSTALL_<TOOL>` Dockerfile mechanism are removed (see [#276](https://github.com/filippolmt/toolbox/issues/276)). Every user runs the same canonical image (`ghcr.io/filippolmt/toolbox:latest`) — the per-tool opt-out, the local-hash image build, and the catalog-driven image hash are all gone.

If your config still has a `tools:` block, the loader emits a one-time warning and ignores it. Delete the block to silence the warning.

## Node package weight prune

Each globally-installed npm tool layer (playwright-cli, codex, codegraph, …) ends with a `find … -prune -exec rm -rf {} +` that strips weight from `node_modules`: source maps, `*.md` / `*.markdown`, `CHANGELOG*`, `AUTHORS*`, and `docs/`/`examples/`/`__tests__/`/`.github/` dirs.

**Gotcha — functional `.md` assets are collateral.** Some packages ship runtime-required content as `.md`. `playwright-cli` is the live example: its agent skills ship as `SKILL.md` + `references/*.md` under `playwright-core/lib/tools/skills/<name>/` (plus a `@playwright/cli/skills/` copy). The blanket `*.md` prune empties those, so `playwright-cli install --skills` then copies an **empty** skill — the failure looks like a working install (exit 0, dirs created) but no `SKILL.md`. The playwright-cli prune therefore leads with `-type d \( -name skills -o -name skill \) -prune -o …` to spare every template tree, and the layer ends with a build-time `test -s …/lib/tools/skills/playwright-cli/SKILL.md` so a future prune/layout change that re-empties the source fails the build instead of shipping a silently-broken skill. The smoke test (`playwright-cli skill install`) re-checks it functionally at runtime. When adding a `.md`-pruning layer for a tool that bundles `.md` assets it actually uses, spare the asset dir the same way.

**Why the exclusion matches by name, not by path.** `@playwright/cli` is version-pinned but pulls `playwright-core` on a floating range, so an upstream `playwright-core` release lands in a pinned image without any Dockerfile change. That is exactly what broke the build once already: an alpha `playwright-core` relocated the skill trees from `lib/tools/cli-client/skill/` to `lib/tools/skills/<name>/`, and split the single skill into several. The old path-based exclusion `-path '*/cli-client/skill'` stopped matching anything, and the new tree ends in `/tools/skills`, which `-path '*/cli/skills'` does not match either — so the prune would have emptied all three skills. The build went red at the `test -s` guard instead, which is the guard working as designed. Matching `-name skills -o -name skill` survives the relocation; the `test -s` still pins the one path `cli-client/program.js` resolves via `libPath("tools", "skills", "playwright-cli", "SKILL.md")`, so a further move fails loudly rather than silently.

## Renovate automerge

Updates land in **two grouped PRs**: runtime-image deps from the Dockerfile (`matchFileNames: ["internal/build/assets/Dockerfile"]` → group `dockerfile image`) and everything else (`matchPackageNames: ["*"]` → group `all dependencies`). The Dockerfile rule comes after the catch-all so its `groupName` wins for those deps; schedule/automerge are inherited from the catch-all. Both merge daily in the 06:00–09:59 Europe/Rome window, Renovate-side (`platformAutomerge: false`, `automergeType: pr`). Three deliberate choices:

- **Branch updates are overnight-only** (`schedule: ["after 11pm", "before 5am"]` on the rule + top-level `updateNotScheduled: false`). Without this, daytime bumps rebased the PR right before or inside the merge window; docker-ci is the slowest job in the repo, so it was still pending when Renovate's in-window run checked, and the merge slipped to the next day — a routine occurrence, cleared by hand in the afternoon. Quiet branch by 05:00 → checks green by 06:00 → in-window merge.
- **`platformAutomerge` stays `false`**: GitHub native auto-merge ignores `automergeSchedule` and merges at any hour — tried and reverted in `3b8d5f7`; morning-only merges are wanted.
- **docker-ci is NOT a required status check in the `main-protection` ruleset** (required: `lint`, `test`, `renovate-validate`). docker-ci has a `paths:` filter (`internal/build/assets/**` + `go.mod`) plus a dynamic matrix; a ruleset-required check that never reports deadlocks every PR that doesn't touch those paths, and rulesets have no conditional required checks. The red-PR gate lives in Renovate instead: default `ignoreTests: false` means Renovate only automerges a fully green PR. The `dockerfile image` PR always touches the Dockerfile, and the `all dependencies` PR triggers docker-ci whenever it bumps `go.mod` (the runtime image's Go) — so any change that actually rebuilds the image runs docker-ci before automerge. A non-image bump (npm/github-actions only) correctly skips it. Residual exposure: a human hastily merging a red docker-ci PR by hand — accepted. If that ever bites, the fix is the always-run gate-job pattern (drop the `paths:` filter, add a final `ci-ok` job that succeeds immediately when no relevant path changed, and require that job).
