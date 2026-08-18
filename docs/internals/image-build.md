# Image build internals

Maintainer notes on how the runtime image is built: Dockerfile layout, layer ordering, version pinning, and the build-time decisions behind individual tools. User-facing image knobs (`image`, `registry_mirror`, `pull`) live in [configuration](../configuration.md#image-selection).

## Build layout: parallel fetch stages + frequency-ordered tail

The Dockerfile is structured for minimal rebuild time, not for linear readability:

- **Every static-binary tool lives in its own `fetch-<tool>` stage** (parent: `fetch-base`, a `debian:bookworm-slim` with curl/CA/git). Artefacts land under `/out` mirroring the final filesystem; the final stage imports them with `COPY --link --from=fetch-<tool> /out/ /`. Consequences: cold builds download all tools in parallel; helper packages a fetch stage installs (unzip, python3, jq) never reach the final image.
- **Those COPYs are declared below the whole RUN tail**, immediately above `USER toolbox`. `--link` builds each layer independently of the filesystem beneath it, so the position changes nothing about their content and everything about what invalidates them — a bump moves that one layer instead of every RUN under it. Until [ADR 0002](../adr/0002-layer-ordering-by-invalidation-floor.md) they sat above the tail, and a one-line version bump moved half the image — that ADR carries the measurements. There are no exceptions: every fetch stage writes at the final path and sets its own permissions, so no tail RUN has to relocate or `chmod` a copied tree — `fetch-omz` clones straight into `/out/home/toolbox` and `chmod -R a+rwX`es it there, which is what let its COPY move down too. **Never add a RUN below the COPY block and never let a tail RUN read a copied file** — both drag the COPY back above the tail for everyone. This is what [*Invalidation Floor*](../../CONTEXT.md#invalidation-floor) names; the moved-layer gate in `docker-publish-reusable.yml` measures it on every publish.
- **Every fetch stage ends with `freeze-mtimes`**, which freezes mtimes under `/out` (`touch -h -d @1`). `COPY --link` folds mtime into the layer digest, so without it a downloaded artefact carries the wall-clock time of the build that fetched it and the digests are stable only while BuildKit reuses the stage — losing the registry cache would republish the entire image with no Dockerfile change. `freeze-mtimes` itself is installed outside `/out`, so it never reaches the final image.
- **Final-stage RUN layers (apt/pip/npm — can't fan out) are ordered rare→frequent** by measured Renovate cadence (≈6-month window: graphifyy 98 bumps, claude-code 93, wrangler/pnpm 36, codex 33, oci 19, playwright 7). Heavy+rare first (azure, oci, playwright install-deps, zsh), frequent npm/pip CLIs last, so the weekly claude-code bump rebuilds only a few cheap npm layers instead of gcloud+go+azure+graphify (~10 min → ~2-3 min). This ordering covers only bumps that land *in* the tail; the majority, which land in a fetch stage, are handled by the point above. Re-measure with `git log --since=… -p -- internal/build/assets/Dockerfile | grep '^+ARG'` before reshuffling.

  The ordering only pays off if **each version ARG is declared immediately above the single RUN that consumes it**. An ARG in scope enters the cache key of every RUN below it — that is the `|N` prefix `docker history` shows per layer — so while all 14 were declared as one block at the top of the stage, all 21 tail RUNs were keyed on all 14 versions: any single bump rebuilt the entire tail and the position of a RUN bought nothing. The gate measured 16 layers above 1 MB and 694 MB on the transition that surfaced it — a transition carrying an `OCI_VERSION` bump plus an unrelated asset change, and billed by a gate that had not compared since the previous publish was cancelled, so read the figure as the scale of the churn rather than the price of one bump (ADR 0002, Follow-up). `TestFinalStageVersionARGsScopedToTheirRUN` holds the placement, and scoping alone is not enough to clear the gate — see the follow-up.
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

Every external binary in the Dockerfile is pinned by version, and Renovate bumps them. SHA256 verification is applied only when upstream publishes a checksums file: the fetch stage downloads it and pipes through `sha256sum -c -` (e.g. `fetch-gh`, `fetch-helm`, `fetch-kubectl`, `fetch-rtk`), which self-heals across bumps and re-tags. Tools whose upstream ships **no** checksums file (bat, fd, eza, zoxide, shellcheck, shfmt, Docker CLI, gcloud) download over HTTPS only. Hand-pinned per-arch SHA256 literals were removed: they broke the build on every version bump and even on an upstream re-tag of the same version (see zoxide 0.10.0), while providing no guarantee a self-authored hash could actually deliver. Adding a new tool touches four files, plus one or two more when it needs a boot script or persists state — the `add-cli` skill drives the whole sequence:

1. Install layer + pinned `ARG <TOOL>_VERSION` in `internal/build/assets/Dockerfile` (own `fetch-<tool>` stage for a static binary, final-stage `RUN` for apt/npm/pip).
2. New row in `internal/catalog/catalog.go` `Entries` — `TestCatalogDockerfilePresence` requires the `Key` to appear as a token in the Dockerfile.
3. `check_optional` line in `internal/build/assets/smoke-test.sh`, plus the derived count literals when the tool adds an `init.d` script or a vendor completion.
4. `customManagers` entry in `renovate.json`, or the pin silently freezes.
5. (optional) `init.d/<NN>-<tool>.sh` matching the row's `InitScript`, when the tool needs a boot step.
6. (optional) A `~/.toolbox/<tool>` bind in `internal/mountplan/defaults.go`, when the CLI stores credentials or state that must survive `toolbox stop`.

There is no per-tool opt-out: every CLI is installed unconditionally. The `ARG INSTALL_<TOOL>` build-arg pattern was removed (see [#276](https://github.com/filippolmt/toolbox/issues/276)). Use `inherit_host_auth:` in `.toolbox.yaml` to share host credentials with the container — see [inherit-host-auth](../configuration.md#inherit-host-auth).

## rtk arm64 is built from source

Dockerfile `rtk-builder` stage + final-stage `COPY --link`. Upstream only ships `aarch64-unknown-linux-gnu` linked against GLIBC 2.39, but the base image (`node:24-bookworm-slim`) ships GLIBC 2.36 — the prebuilt binary aborts with `'GLIBC_2.39' not found`. There is no `aarch64-unknown-linux-musl` release.

Fix: multi-stage build. A `rust:1-slim-bookworm` stage runs `cargo install --git rtk-ai/rtk --tag v${RTK_VERSION} --locked` against the bookworm sysroot (so the resulting binary ABI-matches the runtime), version-checks it in-stage, and the final stage imports it with a single `COPY --link --chmod=0755`. The same stage handles the amd64 tarball download too.

The base image can't move to Debian trixie yet because the Microsoft Azure CLI apt repo currently has no trixie suite.

## Rust base image tag scheme

`<ver>-slim-<distro>`, not `<ver>-<distro>-slim`. Docker Hub publishes `rust:1-slim-bookworm` (correct) but **not** `rust:1-bookworm-slim` (404). PR #89 hit this — the rtk-builder stage failed at image-pull time. When bumping or referencing the rust base, use `<ver>-slim-<distro>` (or bare `<ver>-slim` for the default trixie variant when we eventually move).

## Slim Rust images ship no `curl` / `ca-certificates`

`rust:1-slim-bookworm` contains cargo + git but nothing to fetch tarballs with. The rtk-builder stage installs them via apt before the amd64 tarball path. If you copy the pattern for another tool (e.g. building a Rust binary from source), replicate the apt install — it doesn't propagate from the base.

## Homebrew

Installed via shallow tag clone (`ARG HOMEBREW_VERSION`) at the **default Linux prefix** `/home/linuxbrew/.linuxbrew` — bottles (pre-built binaries) only work there; any other prefix forces source builds, explicitly unsupported upstream ("pick another prefix at your peril"). The official installer script is unusable in a Dockerfile `RUN`: it refuses root and clones unpinned `main`. The layer reproduces the installer's layout manually (repo at `…/Homebrew` + `bin/brew` symlink) and ships the pre-built `_brew` zsh completion from the clone.

Variable host UID handling follows the `/home/toolbox` pattern: `chmod -R a+rwX /home/linuxbrew` so any runtime UID can write the prefix, plus `git config --system --add safe.directory /home/linuxbrew/.linuxbrew/Homebrew` — the clone is root-owned and the runtime UID is arbitrary, so without it every git-touching brew op dies with "dubious ownership". System gitconfig (not `--global`) so the entry stays container-local and out of the user's host-synced `~/.gitconfig`.

Runtime semantics:

- **Ephemeral installs** — `brew install` writes into the non-mounted prefix; everything vanishes on container exit, exactly like `sudo apt install`. Intentional: no `~/.toolbox/brew` bind (a potentially multi-GB prefix over a macOS bind mount would be slow and defeats the disposable-workspace model).
- **First `brew install` downloads Portable Ruby + formula API JSON** (~30–60 s, network required) — once per container, since containers are AutoRemove-ephemeral.
- `HOMEBREW_NO_ANALYTICS=1` + `HOMEBREW_NO_AUTO_UPDATE=1` baked in image ENV (privacy + pin-everything policy); overridable per-session.
- Debian is Homebrew **Tier 2** (fully functional, just outside upstream's Tier 1 CI matrix, which is Ubuntu). Bottles need glibc ≥ 2.35; bookworm ships 2.36.
- The clone is shallow: `brew update` instructs `git fetch --unshallow` first. Acceptable — the version is image-pinned and auto-update is off; the message is self-explanatory for users who insist.

PATH: image `ENV` prepends `…/.linuxbrew/bin:…/.linuxbrew/sbin` (covers non-interactive `docker exec`); interactive zsh additionally evals `brew shellenv` for `HOMEBREW_PREFIX`/`MANPATH`/`INFOPATH` (idempotent w.r.t. the ENV PATH entry). Private GitLab taps authenticate via the glab credential helper — see [GitLab git credential helper (glab)](shell-start.md#gitlab-git-credential-helper-glab).

## DO_NOT_TRACK + claude wrapper

Image sets `ENV DO_NOT_TRACK=1` ([consoledonottrack.com](https://consoledonottrack.com) convention) — honored by bun, playwright, and most JS toolchains users run inside the container (next, astro, turbo, …). Claude Code honors it too, but as a **telemetry umbrella**: it also shuts down the Statsig channel that doubles as feature-flag delivery, which breaks Remote Control and preview rollouts (`/doctor` reports "Feature-flag evaluation enabled (disabled by DO_NOT_TRACK)"). Same failure mode as `DISABLE_TELEMETRY` — see the "Claude Code env knobs" comment block in the Dockerfile for why that flag is intentionally unset.

Fix: the claude install layer replaces the npm `/usr/local/bin/claude` symlink with a `#!/bin/sh` wrapper that does `exec env -u DO_NOT_TRACK <real-cli> "$@"` — the var is stripped for the claude process only, everything else in the container stays opted out. Don't "simplify" the wrapper back to a plain symlink; the smoke test (`claude DO_NOT_TRACK wrapper`) asserts the `env -u` line is present.

Known cost: children spawned by claude's Bash tool inherit the stripped environment, so JS tooling launched *from inside* a claude session loses the opt-out. Accepted trade-off — the alternative (no exemption) breaks Remote Control entirely.

## Two Docker version streams

`DOCKER_CLI_VERSION` in the Dockerfile pins the CLI binary inside the container (currently 29.x); the SDK the CLI launcher uses lives in `go.mod` as the moby modules `github.com/moby/moby/client` (own v0.x series) + `github.com/moby/moby/api` (versioned after the Engine API, v1.x) — the deprecated `github.com/docker/docker` module is gone. The client negotiates the API version by default, so version drift between CLI binary, SDK, and daemon is expected and handled. Don't try to "align" them numerically.

## Go version single source of truth

Unlike the two Docker streams, the Go version is deliberately **aligned everywhere from one anchor**: the `toolchain` directive in `go.mod` (e.g. `toolchain go1.26.4`). Renovate bumps `toolchain` by default (the `go` directive stays the lower compat floor and is not auto-bumped). Everything else derives:

- **CI test/lint/release** — `actions/setup-go` with `go-version-file: go.mod` reads the `toolchain` directive (precedence over `go`).
- **Build/test container** — the one exception, see below: `GO_IMAGE_VERSION` in the `Makefile` pins the `golang:` tag independently, under Renovate's `docker` datasource.
- **Runtime image** — the Dockerfile's `ARG GO_VERSION` is a fallback only; every real build path injects the go.mod value: `make build` passes `--build-arg GO_VERSION=$(GO_VERSION)`, `toolbox build` injects it from `runtime.Version()` (the toolchain that compiled the CLI — see `mergeBuildArgs` in `internal/build/build.go`), and the image-build workflows read go.mod and pass it as a build arg.

### The one exception: the `golang:` Docker tag

Everything above derives from go.mod because every consumer resolves the version against a source that exists the instant the release does: the Go release index (`setup-go`) or a `go.dev` tarball (the runtime image). Docker Hub is not such a source — it publishes `golang:<patch>` days later.

Deriving the tag from go.mod therefore coupled an automergeable dependency bump to an artifact Renovate had not checked: #681 bumped `toolchain` to `go1.26.6`, CI was green (setup-go and the tarball both had it), the PR auto-merged, and `make go-test` / `make build` broke on `main` with `golang:1.26.6: not found`. So `GO_IMAGE_VERSION` is pinned in the `Makefile` with its own `docker`-datasource Renovate manager: Renovate can only open that bump once the image is real. A gap between the two literals is harmless — `GOTOOLCHAIN` fetches the newer toolchain inside the container.

The rule that survives: don't re-pin the Go version in the Dockerfile, and don't add a Renovate manager for `GO_VERSION` — bump `toolchain` in go.mod and it flows everywhere except that one tag. Because the image's Go is derived from go.mod (not from a Dockerfile literal), `docker-ci.yml` also triggers on `go.mod` and includes it in the arm64 `arch_coverage` filter — a toolchain bump pulls a per-arch Go tarball (`go${GO_VERSION}.linux-${TARGETARCH}.tar.gz`), so both arches are smoke-tested in the PR.

`golangci-lint` follows the same one-literal rule: `GOLANGCI_VERSION` in the `Makefile` is the source (Renovate-bumped); the lint CI job reads it from the Makefile instead of carrying its own literal.

## Tools removal

The `tools:` block in `.toolbox.yaml` and the `ARG INSTALL_<TOOL>` Dockerfile mechanism are removed (see [#276](https://github.com/filippolmt/toolbox/issues/276)). Every user runs the same canonical image (`ghcr.io/filippolmt/toolbox:latest`) — the per-tool opt-out, the local-hash image build, and the catalog-driven image hash are all gone.

If your config still has a `tools:` block, the loader emits a one-time warning and ignores it. Delete the block to silence the warning.

## Node package weight prune

Each globally-installed npm tool layer (playwright-cli, codex, codegraph, …) ends with a `find … -prune -exec rm -rf {} +` that strips weight from `node_modules`: source maps, `*.md` / `*.markdown`, `CHANGELOG*`, `AUTHORS*`, and `docs/`/`examples/`/`__tests__/`/`.github/` dirs.

**Gotcha — functional `.md` assets are collateral.** Some packages ship runtime-required content as `.md`. `playwright-cli` is the live example: its agent skills ship as `SKILL.md` + `references/*.md` under `playwright-core/lib/tools/skills/<name>/` (plus a `@playwright/cli/skills/` copy). The blanket `*.md` prune empties those, so `playwright-cli install --skills` then copies an **empty** skill — the failure looks like a working install (exit 0, dirs created) but no `SKILL.md`. The playwright-cli prune therefore leads with `-type d \( -name skills -o -name skill \) -prune -o …` to spare every template tree, and the layer ends with a build-time `test -s …/lib/tools/skills/playwright-cli/SKILL.md` so a future prune/layout change that re-empties the source fails the build instead of shipping a silently-broken skill. The smoke test (`playwright-cli skill install`) re-checks it functionally at runtime. When adding a `.md`-pruning layer for a tool that bundles `.md` assets it actually uses, spare the asset dir the same way.

**Why the exclusion matches by name, not by path.** `@playwright/cli` is version-pinned but pulls `playwright-core` on a floating range, so an upstream `playwright-core` release lands in a pinned image without any Dockerfile change. That is exactly what broke the build on 2026-08-06: `playwright-core 1.63.0-alpha-2026-08-05` relocated the skill trees from `lib/tools/cli-client/skill/` to `lib/tools/skills/<name>/` (three skills now, not one). The old path-based exclusion `-path '*/cli-client/skill'` stopped matching anything, and the new tree ends in `/tools/skills`, which `-path '*/cli/skills'` does not match either — so the prune would have emptied all three skills. The build went red at the `test -s` guard instead, which is the guard working as designed. Matching `-name skills -o -name skill` survives the relocation; the `test -s` still pins the one path `cli-client/program.js` resolves via `libPath("tools", "skills", "playwright-cli", "SKILL.md")`, so a further move fails loudly rather than silently.

## Renovate automerge

Updates land in **two grouped PRs**: runtime-image deps from the Dockerfile (`matchFileNames: ["internal/build/assets/Dockerfile"]` → group `dockerfile image`) and everything else (`matchPackageNames: ["*"]` → group `all dependencies`). The Dockerfile rule comes after the catch-all so its `groupName` wins for those deps; schedule/automerge are inherited from the catch-all. Both merge daily in the 06:00–09:59 Europe/Rome window, Renovate-side (`platformAutomerge: false`, `automergeType: pr`). Three deliberate choices:

- **Branch updates are overnight-only** (`schedule: ["after 11pm", "before 5am"]` on the rule + top-level `updateNotScheduled: false`). Without this, daytime bumps rebased the PR right before/inside the merge window, docker-ci (~20–40 min) was still pending when Renovate's in-window run checked, and the merge slipped to the next day (~20% of grouped PRs missed the window, manual-merged in the afternoon). Quiet branch by 05:00 → checks green by 06:00 → in-window merge.
- **`platformAutomerge` stays `false`**: GitHub native auto-merge ignores `automergeSchedule` and merges at any hour — tried and reverted in `3b8d5f7`; morning-only merges are wanted.
- **docker-ci is NOT a required status check in the `main-protection` ruleset** (required: `lint`, `test`, `renovate-validate`). docker-ci has a `paths:` filter (`internal/build/assets/**` + `go.mod`) plus a dynamic matrix; a ruleset-required check that never reports deadlocks every PR that doesn't touch those paths, and rulesets have no conditional required checks. The red-PR gate lives in Renovate instead: default `ignoreTests: false` means Renovate only automerges a fully green PR. The `dockerfile image` PR always touches the Dockerfile, and the `all dependencies` PR triggers docker-ci whenever it bumps `go.mod` (the runtime image's Go) — so any change that actually rebuilds the image runs docker-ci before automerge. A non-image bump (npm/github-actions only) correctly skips it. Residual exposure: a human hastily merging a red docker-ci PR by hand — accepted. If that ever bites, the fix is the always-run gate-job pattern (drop the `paths:` filter, add a final `ci-ok` job that succeeds immediately when no relevant path changed, and require that job).
