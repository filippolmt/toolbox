# Documentation index

Canonical section-level map of every guide. **Renaming a heading breaks inbound
links — update this map (and run `make check-links`) when adding or renaming
sections.**

| Guide | Type | Covers |
|-------|------|--------|
| [commands.md](commands.md) | reference | Every CLI command, flag, and subcommand |
| [configuration.md](configuration.md) | reference | All `.toolbox.yaml` keys, loading order, `TOOLBOX_*` env |
| [mounts.md](mounts.md) | reference | Credential isolation, mount merge, `mounts_root`, startup hooks |
| [shells.md](shells.md) | how-to | Named shells / multiple workspaces |
| [bridge.md](bridge.md) | explanation | Host daemon for browser / editor / proximo forwarding |
| [proximo.md](proximo.md) | explanation | `.test` apps + CA trust inside the container |
| [sdd.md](sdd.md) | how-to | Spec-Driven-Development skill packs |
| [troubleshooting.md](troubleshooting.md) | how-to | Failure modes: symptom → fix |
| [internals/](#internals) | explanation | Maintainer-only material |

## commands.md

- [Global flag: `--config`](commands.md#global-flag---config)
- [toolbox shell](commands.md#toolbox-shell) — [Publishing ports](commands.md#publishing-ports) · [Loopback bridge](commands.md#loopback-bridge) · [`--oauth` presets](commands.md#--oauth-presets)
- [toolbox stop](commands.md#toolbox-stop)
- [toolbox build](commands.md#toolbox-build)
- [toolbox version](commands.md#toolbox-version)
- [toolbox init](commands.md#toolbox-init)
- [toolbox config](commands.md#toolbox-config) — [config provenance & doctor](commands.md#config-provenance--doctor)
- [toolbox mounts](commands.md#toolbox-mounts)
- [toolbox shells](commands.md#toolbox-shells)
- [toolbox bridge](commands.md#toolbox-bridge)
- [toolbox sdd](commands.md#toolbox-sdd)
- [toolbox completion](commands.md#toolbox-completion)
- [--where targeting](commands.md#--where-targeting)

## configuration.md

- [Getting started](configuration.md#getting-started)
- [Loading order](configuration.md#loading-order)
- [Key reference](configuration.md#key-reference)
- [shell](configuration.md#shell)
- [inherit-host-auth](configuration.md#inherit-host-auth)
- [Image selection](configuration.md#image-selection)
- [browser_bridge (deprecated)](configuration.md#browser_bridge-deprecated)
- [env: passthrough](configuration.md#env-passthrough)
- [TOOLBOX_* environment variables](configuration.md#toolbox_-environment-variables)

## mounts.md

- [Auth isolation under ~/.toolbox/](mounts.md#auth-isolation-under-toolbox)
- [mounts: merge semantics](mounts.md#mounts-merge-semantics) — [Source paths](mounts.md#source-paths)
- [mounts_root retarget](mounts.md#mounts_root-retarget)
- [Startup hooks](mounts.md#startup-hooks) — [Per-repo startup hooks](mounts.md#per-repo-startup-hooks)
- [mounts CLI](mounts.md#mounts-cli)

## shells.md

- [Targeting: toolbox shell [name|dir]](shells.md#targeting-toolbox-shell-namedir)
- [The shells: block](shells.md#the-shells-block)
- [Env overlays](shells.md#env-overlays)
- [Managing entries: toolbox shells](shells.md#managing-entries-toolbox-shells)
- [Bootstrap shorthand: shell --create](shells.md#bootstrap-shorthand-shell---create)

## bridge.md

- [Quick start](bridge.md#quick-start)
- [Architecture](bridge.md#architecture) — [Transport](bridge.md#transport) · [State directory](bridge.md#state-directory)
- [Install topology](bridge.md#install-topology)
- [Security boundary](bridge.md#security-boundary)
- [Editor shims](bridge.md#editor-shims)
- [Mount gating](bridge.md#mount-gating)
- [Uninstall surface](bridge.md#uninstall-surface)
- [Troubleshooting](bridge.md#troubleshooting)

## proximo.md

- [Why .test is unreachable from a sibling container](proximo.md#why-test-is-unreachable-from-a-sibling-container)
- [Enablement is auto-detected (tri-state proximo)](proximo.md#enablement-is-auto-detected-tri-state-proximo)
- [The two host-side ingredients](proximo.md#the-two-host-side-ingredients)
- [Trust establishment (entrypoint, self-gated on the mount)](proximo.md#trust-establishment-entrypoint-self-gated-on-the-mount)
- [Lifecycle from inside the container (bridge shim)](proximo.md#lifecycle-from-inside-the-container-bridge-shim)
- [Boundaries and caveats](proximo.md#boundaries-and-caveats)

## sdd.md

- [Supported integrations](sdd.md#supported-integrations)
- [CLI usage](sdd.md#cli-usage)
- [SDD install steps](sdd.md#sdd-install-steps)
- [SDD .gitignore fence](sdd.md#sdd-gitignore-fence)

## troubleshooting.md

- [Config or port edits don't take effect](troubleshooting.md#config-or-port-edits-dont-take-effect)
- [Port already in use on the host](troubleshooting.md#port-already-in-use-on-the-host)
- ["unknown mount name" at startup](troubleshooting.md#unknown-mount-name-at-startup)
- [Nerd Font placeholders in the prompt](troubleshooting.md#nerd-font-placeholders-in-the-prompt)
- [xdg-open, code, or OAuth login does nothing](troubleshooting.md#xdg-open-code-or-oauth-login-does-nothing)
- [A new .test app is unreachable from the container](troubleshooting.md#a-new-test-app-is-unreachable-from-the-container)
- ["manifest unknown" with a registry mirror](troubleshooting.md#manifest-unknown-with-a-registry-mirror)

## Internals

Maintainer-only material — build mechanics, boot plumbing, privacy lockdown,
host-CLI primitives.

### [internals/image-build.md](internals/image-build.md)

- [Build layout: parallel fetch stages + frequency-ordered tail](internals/image-build.md#build-layout-parallel-fetch-stages--frequency-ordered-tail)
- [Host UID mapping](internals/image-build.md#host-uid-mapping)
- [Passwordless sudo](internals/image-build.md#passwordless-sudo)
- [Docker CLI checksum](internals/image-build.md#docker-cli-checksum)
- [Tool version pinning](internals/image-build.md#tool-version-pinning)
- [rtk arm64 is built from source](internals/image-build.md#rtk-arm64-is-built-from-source)
- [Rust base image tag scheme](internals/image-build.md#rust-base-image-tag-scheme)
- [Slim Rust images ship no curl / ca-certificates](internals/image-build.md#slim-rust-images-ship-no-curl--ca-certificates)
- [Homebrew](internals/image-build.md#homebrew)
- [DO_NOT_TRACK + claude wrapper](internals/image-build.md#do_not_track--claude-wrapper)
- [Two Docker version streams](internals/image-build.md#two-docker-version-streams)
- [Tools removal](internals/image-build.md#tools-removal)
- [Renovate automerge](internals/image-build.md#renovate-automerge)

### [internals/container-lifecycle.md](internals/container-lifecycle.md)

- [Image selection mechanics](internals/container-lifecycle.md#image-selection-mechanics)
- [Codex nested sandbox](internals/container-lifecycle.md#codex-nested-sandbox)
- [Container teardown](internals/container-lifecycle.md#container-teardown)

### [internals/shell-start.md](internals/shell-start.md)

- [Prompt glyph width](internals/shell-start.md#prompt-glyph-width)
- [UTF-8 locale](internals/shell-start.md#utf-8-locale)
- [MCP plugin auto-build](internals/shell-start.md#mcp-plugin-auto-build)
- [Playwright browser cache sync](internals/shell-start.md#playwright-browser-cache-sync)
- [cf Cloudflare CLI skill auto-install](internals/shell-start.md#cf-cloudflare-cli-skill-auto-install)
- [Skill discovery paths diverge between Claude and Codex](internals/shell-start.md#skill-discovery-paths-diverge-between-claude-and-codex)
- [GitLab git credential helper (glab)](internals/shell-start.md#gitlab-git-credential-helper-glab)

### [internals/privacy.md](internals/privacy.md)

- [rtk hook auto-wiring + telemetry/tee lockdown](internals/privacy.md#rtk-hook-auto-wiring--telemetrytee-lockdown)
- [Claude Code env-var matrix](internals/privacy.md#claude-code-env-var-matrix)

### [internals/host-cli.md](internals/host-cli.md)

- [Shared fs primitives](internals/host-cli.md#shared-fs-primitives)
