---
paths:
  - "internal/build/**"
  - "internal/catalog/**"
  - "internal/localimage/**"
  - "go.mod"
  - ".github/workflows/docker-publish-reusable.yml"
  - ".github/scripts/invalidation-floor.sh"
---

# Image build guardrails — the why lives in [`docs/internals/image-build.md`](../../docs/internals/image-build.md)

## Dockerfile layout

- **Fetch-stage COPYs sit below the whole RUN tail**, just above `USER toolbox`; every RUN stays above them and no tail RUN reads a copied file. → [build-layout](../../docs/internals/image-build.md#build-layout-parallel-fetch-stages--an-empty-tail), [Invalidation Floor](../../CONTEXT.md#invalidation-floor)
- **The final stage installs nothing from npm or pip** (`pnpm` exempt): `TestFinalStageInstallsNothingFromAPackageRegistry`. A new tool gets its own `fetch-*` stage, copied from `fetch-codex` (npm) or `fetch-graphify` (pip). → [moving a tool](../../docs/internals/image-build.md#moving-a-tool-into-a-fetch-stage)
- **Each version ARG sits immediately above the one RUN that consumes it**: `TestFinalStageARGsScopedToTheirRUN`.
- **`node-base` alone names the node image**: `TestNodeStagesShareOneBaseImage`.
- **An apt package a fetch stage cannot carry goes in the base apt layer**; a version-derived set is frozen there only behind a smoke-test guard like `playwright deps`.
- **`MAX_LAYERS` is one literal in `.github/scripts/invalidation-floor.sh`**; the gate's baseline is the `sha-<commit>` tag, not `:latest`. → [the gate](../../docs/internals/image-build.md#orderings-the-gate-and-its-baseline)

## Reproducible layers

- **Every fetch stage ends with `freeze-mtimes`**; one shipping a git checkout first runs `freeze-git` over each, before its `chmod -R a+rwX` — repack flag `-F`, addressing by `--git-dir`/`--work-tree`, never `-C`. `TestFreezeGitMakesACheckoutAFunctionOfItsCommit` runs the helper; `TestGitCheckoutStagesCallFreezeGit` holds the call sites. → [Fetch Nondeterminism](../../CONTEXT.md#fetch-nondeterminism)
- **No `FROM` is pinned by digest.**
- **A build-stage COPY copies `/out/ /`**, never a single file to a path; set modes in the stage, not on the COPY: `TestBuildStageCOPYsCopyWholeTree`.

## Image weight

- **Node package weight prune spares functional `.md`**: `prune-node-weight` excludes `skills`/`skill` dirs by name; a stage whose tool reads bundled `.md` ends with a `test -s` on it, as `fetch-playwright-cli` does. `TestPruneNodeWeightSparesSkillTrees`. → [node-prune](../../docs/internals/image-build.md#node-package-weight-prune)
- **A vendored runtime is deduplicated by symlink, after asserting its major matches the base image's**: `TestVendoredRuntimeAssertsMajorBeforeSymlink`. → [ADR 0016](../../docs/adr/0016-image-size-two-denominators.md)
- **A size claim names its denominator** (pull vs disk). The push step's `outputs:` and its `cache-to:` carry the same compression attributes. → [Size Denominator](../../CONTEXT.md#size-denominator)
- **Delete only from the layer that created the bytes**; the `__pycache__` purge follows the last run of the CLI it purges: `TestPycachePurgeFollowsTheLastCLIRun`. → [Creating Layer](../../CONTEXT.md#creating-layer)

## Docker and Go versions

- **Two Docker version streams** — `DOCKER_CLI_VERSION` and the `go.mod` moby modules move independently; `client.New(client.FromEnv)` negotiates. → [docker-streams](../../docs/internals/image-build.md#two-docker-version-streams)
- **`build`'s Docker surface is two unexported interfaces**, `localStore` (`ImageInspect`) and `imageBuilder` (`ImageBuild`) — no `client.APIClient`. `localimage.overlayBuilder` pairs them, and its `buildOverlay` seam var stays a wrapper, not an assignment. `dockertest.Fake` ([container-runtime.md](container-runtime.md)) never embeds `client.APIClient`. `TestLocalRepoDigest`, `TestBuildOverlayComposesFromBaseImageID`, `TestFakeIsNotAnAPIClient`. → [Declared Docker Surface](../../CONTEXT.md#declared-docker-surface)
- **Go's single source is the `go.mod` `toolchain` directive**; `ARG GO_VERSION` is a fallback every build path overrides — no Dockerfile pin, no Renovate manager for it. `golangci-lint`: one `GOLANGCI_VERSION` in the Makefile. → [go-version-sot](../../docs/internals/image-build.md#go-version-single-source-of-truth)
- **Tool versions are pinned, Renovate-bumped, installed unconditionally.** → [tool-pinning](../../docs/internals/image-build.md#tool-version-pinning)
- **Docker CLI checksum**: `fetch-docker` relies on pin + HTTPS (accepted risk T-01-08). → [docker-checksum](../../docs/internals/image-build.md#docker-cli-checksum)

## System setup

- **Passwordless sudo** via `/etc/sudoers.d/toolbox`, UID-agnostic; the smoke test asserts setuid `sudo`. → [sudo](../../docs/internals/image-build.md#passwordless-sudo)
- **Homebrew stays at `/home/linuxbrew/.linuxbrew`**, world-writable, with a system `safe.directory`, analytics and auto-update off. → [homebrew](../../docs/internals/image-build.md#homebrew)
- **System gitconfig, never `--global`**, written by the Dockerfile and by `entrypoint.sh`. `http.version = HTTP/1.1` stays global and pinned in both `fetch-base` and the final stage: `TestGitHTTPVersionPinned`. → [system-git-settings](../../docs/internals/image-build.md#system-git-settings)
- **`/usr/local/bin/glab` is a flock shim** over `/usr/local/libexec/glab`; it never fails closed and never runs glab twice (no `flock -E` sentinel) — `internal/build/glab_flock_shim_test.go`. Everything calls the shim, never `libexec`. → [glab-flock-shim](../../docs/internals/shell-start.md#gitlab-git-credential-helper-glab)
- **SSH host-key trust** is the baked `10-toolbox.conf`; the ssh mount stays read-only, no boot `ssh-keyscan`. → [ssh-host-key-trust](../../docs/internals/image-build.md#ssh-host-key-trust)
- **A new top-level embedded asset is 3 synced edits**: `//go:embed` in `internal/build/embed.go`, the Dockerfile `COPY`, and the expected list in `TestTarEmbeddedContext`.
- **`/usr/local/bin/claude` is the `env -u DO_NOT_TRACK` shim**, not a symlink; the smoke test asserts it. → [do-not-track](../../docs/internals/image-build.md#do_not_track--claude-wrapper)
- **Foreign-Arch Runtime links are arm64-only** (the `TARGETARCH=arm64` branch of the `ln` RUN), into the `android-sdk` mount's Target: `TestForeignArchRuntimeSymlinksAreArm64OnlyAndPointIntoTheSdkBind`, `TestAndroidEnvMigratesTheLegacyForeignArchRuntime`, `TestAndroidSdkInstallOnArm64RefreshesAnExistingForeignArchRuntime`, `TestAndroidSdkInstallOnArm64KeepsTheExistingRuntimeAfterARefreshFailure`, `TestAndroidSdkInstallSerializesConcurrentRuntimeRefreshes`, and `foreign-arch links arm64-only` in `smoke-test.sh`. → [Foreign-Arch Runtime](../../CONTEXT.md#foreign-arch-runtime)
- **Android installer locks stay non-nested and fatal**: runtime lock as narrow as it is, [Mutation Lock](../../CONTEXT.md#android-installer-mutation-lock) over every shared mutation, no unlocked fallback: `TestAndroidSdkInstallSerializesConcurrentMutationsAfterTheRuntime` and `TestAndroidSdkInstallRuntimeRefreshDoesNotWaitForConcurrentMutations`.

## Synced edits

- **`init.d/<NN>-<tool>.sh` is 3 synced edits**: the script, `InitScript` on its `internal/catalog/catalog.go` row, the `count -ne N` gate and message in `smoke-test.sh`. `TestCatalogInitDBijection`; `TestSmokeTestInitDCountLiteral` derives the numbers — don't count by hand.
- **Shell completion is 2 synced edits**: write `_<tool>` to `/out/usr/share/zsh/vendor-completions/` in the tool's own fetch stage, before `freeze-mtimes`, path literal; bump the `-ge N` floor in `_zsh_vendor_completions_check`. `TestSmokeTestVendorCompletionsFloor` names N; loop-written or apt-shipped ones go in `vendorCompletionsUnparseable`. → [shell-completions](../../docs/internals/image-build.md#shell-completions)

## Workspace Install Refresh

The gate is `toolbox_install_refresh` in `bin/install-refresh-lib.sh`; members source it and never re-spell the stamp path, `-n` guard or failure message (`TestWorkspaceInstallRefreshGateLivesInOneplace`). Every member (`30-graphify.sh`, `31-codegraph.sh`, `40-playwright-cli.sh`, …) holds:

- the emptiness guard scopes to the version half alone;
- `graphify hook install` stays outside the gate;
- `30-graphify.sh`'s matcher narrowing stays rename-aware, dropping the graphify-owned entries already parked at the narrowed matcher;
- each agent pass gets its own stamp suffix and its own artefact, and the repo-level opt-in marker stays one per member; pi's agent gate is the `~/.pi` bind mount, as in [container-runtime.md](container-runtime.md);
- each agent's graphify skill directory is gitignored, one entry per agent root.

A new member joins `TestWorkspaceInstallRefreshGate`, `TestGraphifyHookInstallOutsideGate`, `TestPerRepoInstallersRefreshWhatPiReads` and the `Workspace Install Refresh` block in `smoke-test.sh`. → [Workspace Install Refresh](../../CONTEXT.md#workspace-install-refresh)

## Shell and agent assets

- **Starship symbols are width-deterministic**: PUA, ASCII or unambiguous-narrow. → [prompt-glyph-width](../../docs/internals/shell-start.md#prompt-glyph-width)
- **`ENV SHELL=/bin/zsh` is load-bearing** and tracks the passwd shell `entrypoint.sh` writes. → [shell-env](../../docs/internals/shell-start.md#shell-in-the-image-env)
- **`ENV LANG=C.UTF-8`** in the final stage — `LANG` only, never `LC_ALL`. → [utf-8-locale](../../docs/internals/shell-start.md#utf-8-locale)
- **User config is `~/.toolbox-state/zshrc.d/*.zsh`**, sourced last with alias expansion off (`_zsh_user_rc_check`). → [user-config](../../docs/internals/shell-start.md#user-config-in-zshrcd)
- **Never write a Private Use Area glyph as a literal character in an image asset**, nor in an assertion about one — escape it. Held by the `managed statusline` and `baked starship symbols` blocks of `smoke-test.sh`. → [Blanked Glyph](../../CONTEXT.md#blanked-glyph), [glyph-escapes](../../docs/internals/shell-start.md#glyph-escapes-in-image-assets)
- **The statusline renders nothing another surface shows at the same moment**: `pr.*`, `permission_mode`, `workspace.repo.name`, `cwd` stay unrendered; the branch stays because no other surface shows it then. Removing a segment names the surface that took over. Held by the `managed statusline` block of `smoke-test.sh`. → [managed-statusline](../../docs/internals/shell-start.md#managed-statusline)
- **Agent roots have one owner**: scripts derive agent homes, gates, skill roots and the settings lock through `bin/agent-topology-lib.sh` (`TestAgentTopology*`). → [Agent Topology](../../CONTEXT.md#agent-topology)
- **A bundled agent is not a wired agent**: every `config.SupportedAgents` entry has both an Atuin and a Herdr adapter (`TestAgentWiringDeclaresEverySupportedAgent`); the agent list belongs to [config.md](config.md). Persistent pi hooks gate on the `~/.pi` bind mount, and Herdr creates the child agent directory before installing. → [Agent Wiring](../../CONTEXT.md#agent-wiring)
- **Claude `settings.json` writers go through `toolbox_with_claude_settings_lock`**; skill files and Codex/pi files stay outside it. Held by `TestAgentTopologyOwnsClaudeSettingsLock` and `TestHerdrInitLocksClaudeSettings`.

## Local overlay

- **`~/.toolbox/Dockerfile` is a host-side overlay** (`internal/localimage` + `build.BuildOverlay`), RUN-only over the resolved base; baked tools go through `add-cli`. Its marker lives under the declared `plan.StateDir`, the sibling default only as no-state-mount fallback: `TestEnsureKeepsTheMarkerUnderTheGivenStateDir`, `TestEnsureRebuildsForASiblingsOverlay`, `TestEnsureStillGatesRebuildsWithNoStateDir`, `TestFallbackTracksTheDefaultStateMount`. → [local-overlay](../../docs/configuration.md#local-overlay-dockerfile), [overlay marker](../../docs/internals/image-build.md#local-overlay-marker)
