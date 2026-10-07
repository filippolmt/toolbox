---
paths:
  - "internal/container/**"
  - "internal/sessionplan/**"
  - "internal/runplan/**"
  - "internal/imageplan/**"
  - "internal/imageprefetch/**"
  - "internal/imagereclaim/**"
  - "internal/imageref/**"
  - "internal/dockertest/**"
  - "internal/worktree/**"
  - "internal/reload/**"
  - "internal/teardown/**"
  - "internal/workspace/**"
  - "internal/dockeridentity/**"
  - "internal/version/**"
  - "internal/ui/**"
  - "internal/proximo/**"
  - "internal/bridge/**"
  - "cmd/**"
  - "internal/build/assets/init.d/70-loopback-bridge.sh"
  - "internal/build/assets/entrypoint.sh"
---

# Container runtime guardrails

Meaning and rationale live in [`CONTEXT.md`](../../CONTEXT.md) and the linked
guides. This file carries only edit-time constraints and the tests that pin
them.

## Session composition and container creation

- **One composition root:** `cmd.startSession(sessionIntent)` assembles every
  session. `shell` and `worktree` resolve arguments into an intent; they do not
  duplicate migration, bridge-tip, image-digest, planning or attach sequences.
  Preserve the order handover → client → plan → signal context → attach.
  `TestStartSessionPlansWhatTheIntentDescribes` and
  `TestStartSessionLeavesNoPlanSideEffectsWhenTheClientFails` pin the observable
  half. → [Session Intent](../../CONTEXT.md#session-intent)

- **Session Plan owns design-time inputs:** image, binds, ports, env, command,
  working directory, name, hostname and security options cross
  `sessionplan.Plan`. Host UID/GID and daemon filesystem state stay at the
  Docker edge in `dockeridentity.Resolve`; do not put them into the plan.
  `TestPlanHostnameAliasesContainerName`, `TestPlanRejectsOverlongContainerName`,
  `TestSockPathMatchesMountplanDefault` and
  `TestDockerSockGroupsIncludesRootForDesktopCase` pin the boundary. →
  [Session Plan](../../CONTEXT.md#session-plan), [mount rules](mounts.md)

- **Host platform env:** every session receives `TOOLBOX_HOST_OS` and
  `TOOLBOX_HOST_ARCH` from the host CLI runtime. `uname` inside the container is
  the wrong machine. `TestPlanInjectsHostPlatform` pins the pair.

- **Workspace paths:** validate colon-free absolute host paths but keep symlinks
  unresolved, so mount source and container identity remain the path the user
  chose. Pinned by `TestValidateRejectsColon`, `TestResolveKeepsASymlinkedCWD`
  and `TestResolveExplicit`.

- **Container-create state is immutable:** changed binds, env, ports, hostname,
  tmpfs or peer namespace require recreation. Create-time port conflicts fail
  before image refresh; reattach-only mismatches warn after refresh settles.
  `TestShellPreflightsThePortConflictBeforeItOffersToRefresh` and
  `TestShellStartWarnsAboutAContainerItIsActuallyJoining` pin the split. →
  [container lifecycle](../../docs/internals/container-lifecycle.md)

- **Shell phase order:** preflight before refresh, refresh before overlay build,
  digest restamp before create, reload casualty enumeration before teardown.
  Keep these in `internal/container/ordering_test.go`; ordinary value tests do
  not observe reordering.

- **npx exec trees:** when a bind targets `/home/toolbox/.npm`, overlay `_npx`
  with an executable per-container tmpfs owned by the runtime UID/GID. No
  parent bind means no overlay; an explicit child bind wins. This is Docker-edge
  scratch storage, not a configurable Mount Plan row. Pinned structurally by
  `TestShellKeepsNpxExecTreesOffTheNpmCacheBind` and
  `TestShellDoesNotOverrideNpxMountChoices`, semantically by the real-daemon
  `TestNpxExecTmpfsRunsInstalledBinsAsTheSessionUser`. →
  [Tool Cache](../../CONTEXT.md#tool-cache),
  [ADR 0015](../../docs/adr/0015-tool-caches-persist-as-one-bind-per-tool.md)

- **Other create invariants:** codex always gets `seccomp=unconfined`; container
  hostname equals the validated container name; session containers use
  `AutoRemove`; asynchronous diagnostics use `ui.InfoAsyncf` so raw terminals
  receive CRLF. Pinned by `TestShellSetsCodexSecurityOptByDefault`,
  `TestShellPassesPlanHostname`, teardown tests and
  `TestInfoAsyncfReturnsTheCarriage`.

## Image lifecycle

- **Reference identity:** resolve image overrides through
  `imageref.ResolveImage`; repository-digest lookup has one spelling in
  `imageref.LocalRepoDigest`. `SessionPlan.Image` remains the base ref; a local
  overlay is a separate run-image value and never replaces it. Pinned by
  `TestLocalRepoDigest` and `TestShellPrefetchAndStampTrackTheBaseRefNotTheOverlay`.
  → [Image Ref Identity](../../CONTEXT.md#image-ref-identity),
  [Run Image](../../CONTEXT.md#run-image)

- **One synchronous image entry point:** `imageplan.Sync` takes a Reason. Reload
  is silent and is the only reason that trusts the pull TTL; create/start may
  reach the start-up prompt. Keep `ReasonCreate` the zero value and do not add a
  second silent API. `imageplan.Ensure` guarantees local presence and never
  builds. Pinned by `TestSyncAsksBeforeSpendingTheDevelopersTime`,
  `TestSyncOnAReloadNeverAsks` and `TestSyncPullPolicyOnAReload`. →
  [Image Plan](../../CONTEXT.md#image-plan)

- **Prompt settlements are typed:** only `OutcomeAccepted` recreates a stopped
  container. `ReasonStart` defaults elapsed input to no; interruption neither
  declines nor stamps. Pull, overlay build, port preflight and a fresh inspect
  all happen before removal. Pinned by `TestOutcomeReadsBackAsTheCaseItIs`,
  `TestShellStartKeepsTheContainerWhenTheRecreateCannotSucceed`,
  `TestShellStartKeepsTheContainerWhenTheOverlayCannotBuild` and
  `TestShellStartRereadsTheContainerBeforeReplacingIt`. →
  [Start-up Refresh Prompt](../../CONTEXT.md#start-up-refresh-prompt)

- **Prefetch state is cross-process:** each tick cancels its prior poll; every
  gated pass republishes the session axis from the local store; result updates
  merge under the permanent lock inode. Never delete that lock. A cached probe
  may answer whether to ask but must not claim a new sync or touch the attempt
  stamp. Pinned by `TestStartCancelsAHungPollAtTheNextTick`,
  `TestPollPreservesAConcurrentAxisPublicationAcrossProcesses`,
  `TestResultLockIsReleasedWhenTheHolderExits` and
  `TestAheadOfStoreDoesNotClaimAProbeItReadFromTheCache`. →
  [Image Prefetch](../../CONTEXT.md#image-prefetch)

- **Image reclamation:** start only after the current container references the
  new base image. Nominate tagless images carrying the resolved repo digest,
  excluding the kept digest; never use `dangling=true`, force or prune children.
  Announce only actual removals through the asynchronous writer. Pinned by
  `TestSweepLeavesEverythingElseAlone`,
  `TestSweepNeverForcesAndNeverPrunesChildren` and
  `TestShellReclaimsOnlyOnceTheContainerReferencesTheImage`. →
  [Image Reclamation](../../CONTEXT.md#image-reclamation)

- **Declared Docker surface:** daemon-using leaf packages declare narrow,
  unexported interfaces. `internal/container` and `internal/worktree` keep the
  full client because they hand it onward. Keep container seam vars as wrappers,
  and never make `dockertest.Fake` satisfy the full client accidentally. Pinned
  by `TestFakeIsNotAnAPIClient` and `TestFakeZeroValueRefusesEveryCall`. The
  overlay builder follows [image-build rules](image-build.md).

## Session reload and peer messaging

- **Reload ownership:** the zsh function writes the marker; `cmd` performs the
  re-exec; `container.Shell` returns the typed handover and suppresses ordinary
  teardown. Re-entry carries changed idempotent flags and drops create-only
  arguments. The order is verify image → enumerate casualties → remove and wait
  → create. Pinned by `TestReloadMarkerContract`,
  `TestShellReloadVerifiesBeforeItDestroys`,
  `TestShellReentryClassifiesEveryFlag` and real-daemon
  `TestReloadReplacesTheContainer`. → [Session Reload](../../CONTEXT.md#session-reload)

- **Peer messaging remains opt-in:** both the shared PID namespace and the
  `toolbox-cc-socks` volume must succeed or the session degrades to neither.
  The volume is host-global, ignores profiles, and is initialised as the runtime
  user with private permissions. The peer anchor uses the base image and tini
  as PID 1; stale replacement fails closed while any holder is possible.
  `TestShellPeerRuntimeUsesTheBaseImageNotTheOverlay`,
  `TestShellPeerAnchorReapsOrphans`, `TestShellPeerKeepsHeldStaleAnchor` and
  real-daemon `TestPeerMessagingMechanism` pin the contract. Mount ownership is
  governed by [mount rules](mounts.md). →
  [Peer Anchor](../../CONTEXT.md#peer-anchor),
  [peer lifecycle](../../docs/internals/container-lifecycle.md#peer-anchor-reaping)

- **Real-daemon tag:** `dockergate` means “needs the daemon and built image”, not
  a feature. The workflow runs every tagged gate without `-run`; new gates live
  beside the owning package and use `IMAGE_TAG`. Tests must assert the mechanism
  rather than third-party output.

## Bridge, proximo and shell-start assets

- **Loopback bridge:** `-B` is for IPv4 loopback-bound OAuth listeners only;
  wildcard listeners use plain publish flags. Keep Node's IPv4-first setting
  until the bridge is dual-stack. OAuth presets and the documented survey move
  together. → [loopback bridge](../../docs/commands.md#loopback-bridge)

- **Bridge transports:** Linux prefers the bound Unix socket; TCP fallback runs
  only when curl returns no HTTP status. Editor/proximo shims propagate failure;
  `xdg-open` intentionally does not. Host editor paths are translated in the
  shim, never in the daemon. → [bridge](../../docs/bridge.md)

- **Sound handoff:** `paplay` is the probed shim name. Payloads are bytes, one
  player runs at a time, skipped overlap still returns success, and the reaper
  logs exit status, duration and stderr. Keep `cmd.Stderr` as an `*os.File` so
  `Wait` follows the player rather than a copy goroutine. Pinned by
  `TestBridgeContract_ShimMatchesGo`, `TestPlaySoundSkipsAChimeWhileAPlayerIsStillRunning`
  and `TestPlaySoundLogsWhatThePlayerWroteToStderr`. →
  [Sound Handoff](../../CONTEXT.md#sound-handoff)

- **Bridge install context:** install/uninstall refuse root because both service
  managers are per-user. Uninstall treats failure to remove the currently
  mounted state dir as a warning only after token absence is provable; legacy
  state remains strict. Pinned by `TestCheckNotRoot`, `TestStateDirOutcome` and
  `TestUninstallSummary`.

- **Credential helper lookup:** resolve plain helper names through git's exec
  path before `PATH`, matching git itself. `TestCheckHostCredentialHelper` and
  `TestLookHelperIn` pin the composition.

- **Git on virtiofs:** register system-scope `safe.directory=*` before init,
  idempotently and non-fatally. For a virtiofs workspace only, register
  `core.checkStat=minimal`. Keep both blocks under the shared gitconfig lock.
  Pinned by `TestSafeDirectoryRegistration`,
  `TestVirtiofsCheckStatRegistration` and the image smoke test. → [shell start](../../docs/internals/shell-start.md)

- **Proximo gate:** derive `proximo.Resolve` once per configuration at each
  command edge and thread the answer through planning; do not re-run it for
  mount lookups. Runtime host discovery augments create-time hosts and the
  watcher maintains later changes. Trust setup stays in `entrypoint.sh`, not a
  catalog init script. Pinned by `TestResolveQueriesProximoOnceForEveryReader`,
  `TestPlanNeverRederivesTheProximoGate` and
  `TestStartSessionResolvesTheProximoGate`. →
  [proximo](../../docs/proximo.md)
