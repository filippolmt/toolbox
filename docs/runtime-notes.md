# Runtime notes

Deep gotchas pulled out of `CLAUDE.md` to keep the AI-loaded context lean. Reference these from a top-level summary in `CLAUDE.md` instead of inlining.

## Image build

### Build layout: parallel fetch stages + frequency-ordered tail

The Dockerfile is structured for minimal rebuild time, not for linear readability:

- **Every static-binary tool lives in its own `fetch-<tool>` stage** (parent: `fetch-base`, a `debian:bookworm-slim` with curl/CA/git). Artefacts land under `/out` mirroring the final filesystem; the final stage imports them with `COPY --link --from=fetch-<tool> /out/ /`. Consequences: cold builds download all tools in parallel; a Renovate bump of one tool re-runs only its stage + COPY (never the tail, `--link` layers are independent of parent changes); helper packages a fetch stage installs (unzip, python3, jq) never reach the final image.
- **Final-stage RUN layers (apt/pip/npm — can't fan out) are ordered rare→frequent** by measured Renovate cadence (≈6-month window: claude-code and graphifyy ~25 bumps each, pnpm 11, codex/gcloud/oci 6, playwright 2). Heavy+rare first (azure, oci, playwright install-deps, zsh), frequent npm/pip CLIs last, so the weekly claude-code bump rebuilds only a few cheap npm layers instead of gcloud+go+azure+graphify (~10 min → ~2-3 min). Re-measure with `git log --since=… --pretty=%s -- internal/build/assets/Dockerfile` before reshuffling.
- **`make build` seeds from the CI registry cache** (`ghcr.io/filippolmt/toolbox:buildcache-main`, written by `docker-publish.yml` with `mode=max`, multi-arch — includes the arm64 rtk cargo build). First build on a fresh machine ≈ a layer pull. Cache-import failures are warnings, so offline builds still work.
- Version checks (`<tool> --version`) run **inside the fetch stage** — they catch wrong-arch / GLIBC-mismatch before the smoke test, same as the old in-layer checks.

### Host UID mapping

The CLI runs the container with `--user $(id -u):$(id -g)`. Because the runtime UID rarely matches the baked `toolbox` user (UID 1000), `/home/toolbox` is made world-writable in the image. Don't revert to a fixed UID without understanding why — host file ownership would invert and writes inside `~/.toolbox/` would fail for anyone whose host UID isn't 1000.

### Passwordless sudo

The base apt layer installs `sudo`, and the user-setup layer drops `/etc/sudoers.d/toolbox` with `ALL ALL=(ALL:ALL) NOPASSWD: ALL` (`!requiretty`, `!fqdn`). The runtime UID is the host's and rarely matches the baked `toolbox` user, so the rule is deliberately UID-agnostic — it matches whatever UID the entrypoint injects into `/etc/passwd`. This lets `sudo apt-get update && sudo apt install …` (or any root op) work inside a running container without baking the tool into the image (apt lists aren't baked, so `update` runs first). Safe because the container is `AutoRemove` (see [Container teardown](#container-teardown)): everything installed at runtime vanishes on exit. **Caveat:** sudo writing into bind-mounted host paths (`/workspace`, `~/.toolbox/*`) produces `root:root` files on the host — escalate for in-container/system state, not for editing mounted project files. `visudo -cf` validates the drop-in at build; the smoke test asserts the `sudo` binary is present and setuid root.

### Docker CLI checksum

The `fetch-docker` stage of `internal/build/assets/Dockerfile` installs the static Docker CLI binary without a SHA256 verification step because Docker doesn't publish `.sha256` files for those releases. Version pin + HTTPS is the only guard. Tracked as accepted risk T-01-08.

### Tool version pinning

Every external binary in the Dockerfile is pinned by version + SHA256 (exceptions: Docker CLI — see above — and gcloud, which uses a Google APT repo). Renovate bumps them. Adding a new tool is now a 2-edit (or 3-edit when a runtime init script is needed) operation:

1. New row in `internal/catalog/catalog.go` `Entries`.
2. New install `RUN` block in `internal/build/assets/Dockerfile`.
3. (optional) New `init.d/<NN>-<tool>.sh` if `InitScript` is set on the catalog row.

There is no per-tool opt-out: every CLI is installed unconditionally. The `ARG INSTALL_<TOOL>` build-arg pattern was removed (see [#276](https://github.com/filippolmt/toolbox/issues/276)). Use `inherit_host_auth:` in `.toolbox.yaml` to share host credentials with the container — see the [inherit-host-auth](#inherit-host-auth) section.

### rtk arm64 is built from source

Dockerfile `rtk-builder` stage + final-stage `COPY --link`. Upstream only ships `aarch64-unknown-linux-gnu` linked against GLIBC 2.39, but the base image (`node:24-bookworm-slim`) ships GLIBC 2.36 — the prebuilt binary aborts with `'GLIBC_2.39' not found`. There is no `aarch64-unknown-linux-musl` release.

Fix: multi-stage build. A `rust:1-slim-bookworm` stage runs `cargo install --git rtk-ai/rtk --tag v${RTK_VERSION} --locked` against the bookworm sysroot (so the resulting binary ABI-matches the runtime), version-checks it in-stage, and the final stage imports it with a single `COPY --link --chmod=0755`. The same stage handles the amd64 tarball download too.

The base image can't move to Debian trixie yet because the Microsoft Azure CLI apt repo currently has no trixie suite.

### Rust base image tag scheme

`<ver>-slim-<distro>`, not `<ver>-<distro>-slim`. Docker Hub publishes `rust:1-slim-bookworm` (correct) but **not** `rust:1-bookworm-slim` (404). PR #89 hit this — the rtk-builder stage failed at image-pull time. When bumping or referencing the rust base, use `<ver>-slim-<distro>` (or bare `<ver>-slim` for the default trixie variant when we eventually move).

### Slim Rust images ship no `curl` / `ca-certificates`

`rust:1-slim-bookworm` contains cargo + git but nothing to fetch tarballs with. The rtk-builder stage installs them via apt before the amd64 tarball path. If you copy the pattern for another tool (e.g. building a Rust binary from source), replicate the apt install — it doesn't propagate from the base.

### Homebrew

Installed via shallow tag clone (`ARG HOMEBREW_VERSION`) at the **default Linux prefix** `/home/linuxbrew/.linuxbrew` — bottles (pre-built binaries) only work there; any other prefix forces source builds, explicitly unsupported upstream ("pick another prefix at your peril"). The official installer script is unusable in a Dockerfile `RUN`: it refuses root and clones unpinned `main`. The layer reproduces the installer's layout manually (repo at `…/Homebrew` + `bin/brew` symlink) and ships the pre-built `_brew` zsh completion from the clone.

Variable host UID handling follows the `/home/toolbox` pattern: `chmod -R a+rwX /home/linuxbrew` so any runtime UID can write the prefix, plus `git config --system --add safe.directory /home/linuxbrew/.linuxbrew/Homebrew` — the clone is root-owned and the runtime UID is arbitrary, so without it every git-touching brew op dies with "dubious ownership". System gitconfig (not `--global`) because `~/.gitconfig` may be a read-only host mount.

Runtime semantics:

- **Ephemeral installs** — `brew install` writes into the non-mounted prefix; everything vanishes on container exit, exactly like `sudo apt install`. Intentional: no `~/.toolbox/brew` bind (a potentially multi-GB prefix over a macOS bind mount would be slow and defeats the disposable-workspace model).
- **First `brew install` downloads Portable Ruby + formula API JSON** (~30–60 s, network required) — once per container, since containers are AutoRemove-ephemeral.
- `HOMEBREW_NO_ANALYTICS=1` + `HOMEBREW_NO_AUTO_UPDATE=1` baked in image ENV (privacy + pin-everything policy); overridable per-session.
- Debian is Homebrew **Tier 2** (fully functional, just outside upstream's Tier 1 CI matrix, which is Ubuntu). Bottles need glibc ≥ 2.35; bookworm ships 2.36.
- The clone is shallow: `brew update` instructs `git fetch --unshallow` first. Acceptable — the version is image-pinned and auto-update is off; the message is self-explanatory for users who insist.

PATH: image `ENV` prepends `…/.linuxbrew/bin:…/.linuxbrew/sbin` (covers non-interactive `docker exec`); interactive zsh additionally evals `brew shellenv` for `HOMEBREW_PREFIX`/`MANPATH`/`INFOPATH` (idempotent w.r.t. the ENV PATH entry). Private GitLab taps authenticate via the glab credential helper — see [GitLab git credential helper (glab)](#gitlab-git-credential-helper-glab).

### DO_NOT_TRACK + claude wrapper

Image sets `ENV DO_NOT_TRACK=1` ([consoledonottrack.com](https://consoledonottrack.com) convention) — honored by bun, playwright, and most JS toolchains users run inside the container (next, astro, turbo, …). Claude Code honors it too, but as a **telemetry umbrella**: it also shuts down the Statsig channel that doubles as feature-flag delivery, which breaks Remote Control and preview rollouts (`/doctor` reports "Feature-flag evaluation enabled (disabled by DO_NOT_TRACK)"). Same failure mode as `DISABLE_TELEMETRY` — see the "Claude Code env knobs" comment block in the Dockerfile for why that flag is intentionally unset.

Fix: the claude install layer replaces the npm `/usr/local/bin/claude` symlink with a `#!/bin/sh` wrapper that does `exec env -u DO_NOT_TRACK <real-cli> "$@"` — the var is stripped for the claude process only, everything else in the container stays opted out. Don't "simplify" the wrapper back to a plain symlink; the smoke test (`claude DO_NOT_TRACK wrapper`) asserts the `env -u` line is present.

Known cost: children spawned by claude's Bash tool inherit the stripped environment, so JS tooling launched *from inside* a claude session loses the opt-out. Accepted trade-off — the alternative (no exemption) breaks Remote Control entirely.

## Mounts & auth isolation

### Auth isolation under `~/.toolbox/`

Every credential path the container sees lives under `~/.toolbox/` on the host (`.claude`, `state`, `gh`, `glab`, `rtk/{config,data}`, `cf/{auth,config}`, …) or is a symlink to the host's real file (`ssh`, `gitconfig`). Canonical list in `internal/mountplan/defaults.go`, exposed as `mountplan.Defaults()`. `~/.secrets` is intentionally NOT mounted.

rtk and cf are the two tools whose state spans two binds because upstream splits config across non-XDG paths and exposes no env override:

- rtk: `~/.config/rtk` (config) + `~/.local/share/rtk` (analytics/tee dumps).
- cf: `~/.cf/config.toml` (OAuth tokens) + `~/.config/cf/config.json` (context defaults, completion marker).

In both cases the bind sources are nested under a single `~/.toolbox/<tool>/` root so the host layout stays flat.

### `mounts:` merge semantics

User-declared mounts in `.toolbox.yaml` patch / replace / append / disable defaults by `name` (see `mergeMounts` in `internal/mountplan/merge.go`).

- Name-only entry → patches the matching default.
- Adding `target` → replaces it.
- Unknown name → appended.
- `disabled: true` → drops a default.
- Patch referencing a name that doesn't exist → fails `Plan()` loudly.

Sources accept absolute, `~/`, and CWD-relative paths (resolved by `resolveAll` against the dir from which `toolbox shell` was invoked).

### `mounts_root` retarget

Setting `mounts_root: /custom/path` rewrites every default mount whose Source starts with `~/.toolbox/` to live under the new root, applied *before* the user merge inside `mountplan.Merge`. Per-mount patches still win, so a global root + a single per-name override coexist. `docker-sock` and `SymlinkFrom` targets are not touched (they reference real host paths, not toolbox-managed mirrors). Relative values rejected at startup by `config.ValidateMountsRoot`.

## Image versions

### Two Docker version streams

`DOCKER_CLI_VERSION` in the Dockerfile pins the CLI binary inside the container (currently 29.x); `github.com/docker/docker` in `go.mod` is the SDK the CLI launcher uses (pinned to the highest v28.x `+incompatible` tag, since upstream publishes no v29 Go module). The client calls `client.WithAPIVersionNegotiation()` so API drift between the two is expected and handled. Don't try to "align" them numerically.

### Tools removal

The `tools:` block in `.toolbox.yaml` and the `ARG INSTALL_<TOOL>` Dockerfile mechanism are removed (see [#276](https://github.com/filippolmt/toolbox/issues/276)). Every user runs the same canonical image (`ghcr.io/filippolmt/toolbox:latest`) — the per-tool opt-out, the local-hash image build, and the catalog-driven image hash are all gone.

If your config still has a `tools:` block, the loader emits a one-time warning and ignores it. Delete the block to silence the warning.

### inherit-host-auth

`inherit_host_auth: [<key>, …]` in `.toolbox.yaml` opts the listed CLIs into reading the host's standard credential path (read-only) instead of the isolated `~/.toolbox/<key>/` default. Default is `[]` — fully isolated, matches the pre-#276 behavior.

Eligible CLIs and their host paths (catalog entries with non-nil `HostAuthMount`):

| Key      | Host path                 | Container path                       |
|----------|---------------------------|--------------------------------------|
| `gh`     | `~/.config/gh`            | `/home/toolbox/.config/gh`           |
| `glab`   | `~/.config/glab-cli`      | `/home/toolbox/.config/glab-cli`     |
| `gcloud` | `~/.config/gcloud`        | `/home/toolbox/.config/gcloud`       |
| `docker` | `~/.docker`               | `/home/toolbox/.docker`              |
| `azure`  | `~/.azure`                | `/home/toolbox/.azure`               |
| `oci`    | `~/.oci`                  | `/home/toolbox/.oci`                 |
| `claude` | `~/.claude`               | `/home/toolbox/.claude`              |
| `codex`  | `~/.codex`                | `/home/toolbox/.codex`               |
| `atuin`  | `~/.local/share/atuin`    | `/home/toolbox/.local/share/atuin`   |

Validation in `config.Plan` rejects unknown keys and keys whose catalog entry lacks `HostAuthMount`. (An earlier revision of this note claimed the mount is read-only — it is not; see the read-write paragraph below for the rationale.)

Mount semantics: when a key is listed in `inherit_host_auth`, the default `~/.toolbox/<key>` mount is dropped (not supplemented) — two mounts at the same container target would shadow unpredictably. User `mounts:` patches keying on the same `name:` still compose on top of the inherited mount.

`mounts_root` interaction: if both `mounts_root: /custom` and `inherit_host_auth: [<key>]` are set, the `mounts_root` retargeting is bypassed for that key — host inheritance pulls from the host's canonical path (e.g. `~/.config/gh`), not from `/custom/gh`. `mounts_root` still applies to every other default mount. If you need the credential dir on an encrypted volume, choose one approach or the other.

Pre-stat check: `inherit_host_auth: [<key>]` requires the host source path to exist at config-load time. If it does not, `toolbox shell` fails with a clear error pointing at the missing path — silent soft-skip would have left the container with no credential mount at all (worse than failing loud).

Read-write inheritance: inherited mounts are read-write. Most listed CLIs refresh tokens or update session state during normal use (atuin appends history, claude/codex write session state, gh/docker rotate OAuth refresh tokens) — RO would EROFS those writes. You opt in explicitly: your host credential dir is now writable by container processes.

macOS keychain caveat: `gh` on macOS stores its OAuth token in the system keychain by default — `~/.config/gh/hosts.yml` carries the account but no `oauth_token`, so inheriting that dir mounts a token-less config and `gh auth status` inside the container reports the token invalid. Workaround: re-login on the host with `gh auth login --insecure-storage` (persists the token into `hosts.yml`), or skip inheritance for gh and log in once inside the container (isolated `~/.toolbox/gh` survives recreates). The same class of issue applies to any CLI that delegates secret storage to an OS keychain.

### Renovate automerge

The grouped deps PR (`matchPackageNames: ["*"]`) merges daily in the 06:00–09:59 Europe/Rome window, Renovate-side (`platformAutomerge: false`, `automergeType: pr`). Three deliberate choices:

- **Branch updates are overnight-only** (`schedule: ["after 11pm", "before 5am"]` on the rule + top-level `updateNotScheduled: false`). Without this, daytime bumps rebased the PR right before/inside the merge window, docker-ci (~20–40 min) was still pending when Renovate's in-window run checked, and the merge slipped to the next day (~20% of grouped PRs missed the window, manual-merged in the afternoon). Quiet branch by 05:00 → checks green by 06:00 → in-window merge.
- **`platformAutomerge` stays `false`**: GitHub native auto-merge ignores `automergeSchedule` and merges at any hour — tried and reverted in `3b8d5f7`; morning-only merges are wanted.
- **docker-ci is NOT a required status check in the `main-protection` ruleset** (required: `lint`, `test`, `renovate-validate`). docker-ci has a `paths:` filter (`internal/build/assets/**`) plus a dynamic matrix; a ruleset-required check that never reports deadlocks every PR that doesn't touch those paths, and rulesets have no conditional required checks. The red-PR gate lives in Renovate instead: default `ignoreTests: false` means Renovate only automerges a fully green PR, and deps PRs always touch the Dockerfile so docker-ci always runs on them. Residual exposure: a human hastily merging a red docker-ci PR by hand — accepted. If that ever bites, the fix is the always-run gate-job pattern (drop the `paths:` filter, add a final `ci-ok` job that succeeds immediately when no relevant path changed, and require that job).

## Container lifecycle

### Image selection

`toolbox shell` always pulls `ghcr.io/filippolmt/toolbox:latest`. There is no per-tool opt-out, no local-hash fallback, no auto-build branch. `toolbox build` is the explicit escape hatch — it overwrites the local cache of the canonical tag so the next `toolbox shell` picks up the freshly built image. Run `docker pull ghcr.io/filippolmt/toolbox:latest` to restore the upstream copy.

### Port bindings are fixed at container creation

`toolbox shell -p <port>` only takes effect when the container is first created — `ContainerCreate` writes the port set, and Docker doesn't accept post-hoc port changes. Run `toolbox stop` before re-invoking `toolbox shell -p …` to add or change bindings. Accepted formats mirror `docker run -p`; host IP defaults to `127.0.0.1` when omitted. Mismatch detection lives in `sessionplan.MissingPublishPorts`. When the listener inside the container binds `127.0.0.1` rather than `0.0.0.0`, see [loopback bridge](#loopback-bridge).

### Loopback bridge

In-container CLIs that bind their OAuth callback to `127.0.0.1:<port>` (shopify, vanilla wrangler) are unreachable from the host browser even with `toolbox shell -p <port>:<port>`. Docker's port forward delivers packets to the container's `eth0` interface; a listener bound to container loopback never sees them. The `-B` / `--bridge-loopback` flag fixes that:

```
host browser  ──  Docker -p  ──▶  eth0:<port>  ──[ socat ]──▶  127.0.0.1:<port>  ──▶  CLI
                                  ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
                                   bridged inside the container by init.d/70
```

When `-B` is set together with at least one `-p`, `sessionplan.Plan` emits `TOOLBOX_LOOPBACK_BRIDGE_PORTS=<comma-joined>` and `internal/build/assets/init.d/70-loopback-bridge.sh` spawns one `socat TCP-LISTEN:<port>,bind=$ETH_IP,fork,reuseaddr TCP:127.0.0.1:<port>` per port. The bridge binds the container's external interface IP (`hostname -i`) explicitly so it can coexist with the CLI's own `127.0.0.1:<port>` listener. It does **not** coexist with a wildcard listener: once socat holds `eth0:<port>`, a later `0.0.0.0:<port>` bind on the same port fails `EADDRINUSE` (Linux refuses wildcard over a specific bind regardless of `SO_REUSEADDR` — verified live with oci). Wildcard-binding CLIs must therefore not use `-B` on their port; the plain `-p` forward already reaches them.

Standard recipes (static-port loopback CLIs). The `--oauth <tool>` preset flag on `toolbox shell` expands a known tool name to its documented recipe (`sessionplan.ExpandOAuth`, map in `internal/sessionplan/oauth.go`) — expansion only ever adds to explicit `-p`/`-B` flags, and an unknown tool errors before container creation listing the supported set:

```
toolbox shell --oauth codex      # = -B -p 1455:1455     codex ChatGPT-OAuth login
toolbox shell --oauth shopify    # = -B -p 13387:13387   shopify store auth
toolbox shell --oauth wrangler   # = -B -p 8976:8976     wrangler login
```

**Wildcard-bind carve-out.** `oci session authenticate` binds `0.0.0.0:8181` (`cli_setup_bootstrap.py`: `server_address = ('', 8181)`), so Docker's plain port-forward reaches it directly and the bridge is not only unnecessary but harmful — socat on `eth0:8181` makes oci's wildcard bind fail with `Could not complete bootstrap process because port 8181 is already in use`:

```
toolbox shell --oauth oci   # = -p 8181:8181 (no -B — oci binds 0.0.0.0)
```

**Dynamic-port carve-out.** `cf` picks its callback port at run time from the range `startPort: 8877, maxPortAttempts: 10`. The bridge needs a known container port to forward, so `cf` cannot use it — the existing build-time `sed` patch (Dockerfile `cf` install layer) that rewrites `127.0.0.1` → `0.0.0.0` is retained for `cf` and similar dynamic-port CLIs (`gcloud`, `gws`, `tofu` — no fixed range at all, so they get no recipe). `cf login` recipe (no `-B` needed):

```
toolbox shell --oauth cf   # = -p 8877-8886:8877-8886 (sed-patched, range syntax via nat.ParsePortSpec)
```

OAuth CLI survey:

| CLI | Listener style | Strategy |
|---|---|---|
| `oci` | `0.0.0.0:8181` (static, wildcard) | plain publish: `--oauth oci` = `-p 8181:8181` (no `-B` — socat would collide with the wildcard bind) |
| `shopify` | `127.0.0.1:13387` (static) | bridge: `--oauth shopify` = `-B -p 13387:13387` |
| `wrangler` | `localhost:8976` (static) | bridge: `--oauth wrangler` = `-B -p 8976:8976` (vanilla wrangler, no sed) |
| `codex` | `localhost:1455` (static, default ChatGPT-OAuth flow) | bridge: `--oauth codex` = `-B -p 1455:1455`; device-code (`codex login --device-auth`) exists but is an opt-in beta, not the default |
| `cf` | `127.0.0.1:8877-8886` (dynamic) | build-time sed → `0.0.0.0` + `--oauth cf` = `-p 8877-8886:8877-8886` |
| `gcloud` | `localhost:8085+` (dynamic) | wrapper / device-code |
| `gws` | `127.0.0.1:0` (ephemeral) | wrapper / device-code |
| `tofu` | random port ≥1024, range from server discovery document (dynamic) | not pre-bindable; device-code/wrapper-style — no bridge recipe |
| `az` | dynamic | device-code (`--use-device-code`) |
| `gh` / `glab` / `claude` | none — device-code | no listener; no port forward needed |

Limitations:

- Bridge env is fixed at `ContainerCreate`. `toolbox shell -B …` on a container created without `-B` is a no-op — same UX as `-p`. Run `toolbox stop` first.
- IPv4 only. Docker port-forward IPv6 support is patchy; not in scope.
- `-B` without `-p` is not an error. The init.d script logs a one-line `loopback bridge: enabled but no -p ports published — skipping` warning so the misconfiguration is visible.
- `-B` bridges **every** published port (`TOOLBOX_LOOPBACK_BRIDGE_PORTS` enumerates the full publish set, init.d/70 spawns socat per port). Combining a bridged preset with a wildcard-bind one (e.g. `--oauth wrangler --oauth oci`) therefore puts socat on `eth0:8181` too and breaks oci's wildcard bind — same for `cf` (sed-patched to `0.0.0.0`, socat on the 8877-8886 range would exhaust its 10 port retries). Authenticate wildcard-bind CLIs in their own session.
- Per-port failure (e.g. `EADDRINUSE` because another in-container process already binds `eth0:<port>`) is logged to `~/.toolbox-state/init/70-loopback-bridge.log` and the loop continues with the remaining ports. The bridge never aborts boot.
- `socat` is part of the always-on base apt-install set (~350KB) — no per-tool opt-out. The bridge feature is system-level, not a catalog tool.

See also: [`port-bindings`](#port-bindings-are-fixed-at-container-creation), [`image-build`](#image-build) (socat install layer), [`browser-bridge`](#browser-bridge) (inverse direction — container→host browser opens).

### Codex nested sandbox

When `tools.codex` is enabled (default), `toolbox shell` creates the container with Docker `seccomp=unconfined` so Codex's built-in bubblewrap sandbox can create nested user namespaces. With `tools.codex: false` the container keeps Docker's default seccomp profile. The flag flip lives in `sessionplan.NestedSandboxSecurityOpt`.

### Container teardown

The container is disposable: when the last attached shell exits it is destroyed (all persistent state lives on the `~/.toolbox/` bind mounts). The cost of *how* it is destroyed used to land on the user's prompt — `teardown.StopOne` ran a synchronous `ContainerStop` + `ContainerRemove`, and on macOS Docker Desktop the remove blocks on unmounting ~25 virtiofs binds inside the LinuxKit VM (~1–2s). The SIGTERM grace is not the cost (PID 1 is `sleep`, dies instantly) and neither is zsh (~90ms); the daemon-side unmount is.

Fix: containers are created with `HostConfig.AutoRemove: true` (`container.createAndStart`). The daemon's auto-remove worker performs the unmount + delete **after the container exits**, asynchronously from any client call. So the exit path only has to make the container *exit*:

- `teardown.OnShellExit` does a single `ContainerInspect` and branches on it:
  - a still-running sibling exec → leave the container running so the other terminal survives;
  - `HostConfig.AutoRemove` true → `ContainerKill` (SIGKILL — nothing to flush) and return immediately; the daemon reaps it off the prompt's critical path;
  - AutoRemove false (legacy container created before this change) → synchronous `StopOne` fallback, dead within one upgrade cycle since containers are recreated each shell.
- `teardown.StopOne` (the explicit `toolbox stop` / `--all` path) stays synchronous — a cleanup command should confirm removal — but now tolerates a `Conflict` ("removal already in progress") alongside `NotFound`, because on an AutoRemove container the stop may have already triggered the daemon's removal.

Consequence: a stopped container is auto-removed, so the `runplan.ActionStart` "reuse a stopped container" path effectively never fires for new containers — every `toolbox shell` recreates from the canonical image plus the mounted state. The latency moves off the blocking exit and onto a startup the user already expects to do work.

Rejected alternatives: a detached client-side `docker rm -f` (orphan process, no error feedback, races a fast re-`shell`); a single synchronous `ContainerRemove(Force)` (still blocks the client on the unmount). AutoRemove lets the daemon serialise the teardown correctly.

## Shell start

### Prompt glyph width

Every symbol in `internal/build/assets/starship.toml` must be ASCII, unambiguous-narrow Unicode, or a Nerd Font PUA glyph — never an East Asian Ambiguous character or an emoji-presentation sequence (U+FE0F). Starship's *defaults* violate this: kubernetes ships `☸ ` (U+2638, EA-Ambiguous) and gcloud ships `☁️ ` (U+2601+FE0F). Ghostty measures them with Unicode grapheme-cluster width (mode 2027) → 2 columns, while zsh ZLE lays out the line with libc `wcwidth()` → 1 column. One column of drift per glyph meant every Backspace left exactly as many ghost characters as ambiguous emoji visible in the prompt — a months-long "intermittent" bug, because the k8s/gcloud modules only render where those contexts are active. Three earlier fixes (autosuggestions rebind, TERM forwarding, terminfo bundling) chased adjacent symptoms; the real confirmation was `PROMPT='> '` killing the residue while plugin/RPROMPT/highlighter toggles did nothing. **Diagnostic heuristic: any "ghost characters on redraw" report → test `PROMPT='> '` first, before suspecting ZLE plugins.** The four module symbols (`kubernetes`, `gcloud`, `terraform`, `docker_context`) are pinned to PUA glyphs with codepoint comments in `starship.toml`; PUA is width-1 under both width systems by construction, and Nerd Font on the host is already a README prerequisite.

### MCP plugin auto-build

`internal/build/assets/init.d/50-mcp-plugins.sh` scans `~/.claude/plugins/cache/**` and runs `npm install && npm run build` for any plugin missing a `dist/`. First shell after a plugin install is therefore slower; subsequent shells cached via `.toolbox-built` marker. On failure stderr is captured to `.toolbox-build-error.log` next to the marker (in the same bind-mounted plugin dir, so it survives container restarts) and the last 5 lines are printed inline; failure stays non-fatal.

### Playwright browser cache sync

`internal/build/assets/init.d/40-playwright-cli.sh` does two jobs. Besides re-installing the playwright-cli skills, it syncs the bundled Chromium to the pinned playwright version. The Dockerfile bakes the `playwright` npm package + `playwright install-deps chromium` (apt deps) only — the **browser binaries** are not baked; they live in the `~/.toolbox/playwright-cache` bind (host-persisted, kept out of the image). Since nothing else downloads them, a `playwright` Renovate bump would otherwise leave the cache on the old Chromium revision and break the default headless launch: playwright resolves `chromium.launch({headless:true})` to a separate `chromium_headless_shell-<rev>` binary that a stale cache never fetched (observed: cache held `chromium-1224` with no headless shell after a bump to 1.60.0, whose pinned rev is 1223). A version sentinel (`<cache>/.toolbox-chromium-version`, compared against the playwright package.json version — read via `node`, not `playwright --version`, to dodge the rtk wrapper) makes the sync a no-op on every shell except the first after a bump, when it runs `playwright install chromium` (full + headless shell) once. Best-effort + non-fatal: an offline shell still starts. This rides the existing `40-` script (no new init.d → no `TestCatalogInitDBijection` / smoke-count edit).

### `cf` Cloudflare CLI skill auto-install

When `tools.cf` is enabled (default) and `~/.claude` exists, `internal/build/assets/init.d/20-cf.sh` writes a Claude Code skill to `~/.claude/skills/cf/SKILL.md` if absent. Skill is hand-written and points Claude to `cf agent-context <product>` for on-demand product context (instead of pre-baking the ~107-product corpus). Idempotent — only re-creates when the file is missing, so user edits persist.

### Skill discovery paths diverge between Claude and Codex

Claude Code reads only `~/.claude/skills/<name>/SKILL.md` (per docs.claude.com); Codex CLI reads only `~/.agents/skills/<name>/SKILL.md` (Agent Skills USER scope per agentskills.io). Despite the shared "Agent Skills" branding, the two locations are NOT mutually compatible. CLI wrappers that ship a SKILL.md need a dual-install pass to be visible in both agents. Reference: `internal/build/assets/init.d/60-glab.sh` runs `glab skills install --path ~/.claude/skills --force` for Claude and `glab skills install --global --force` for Codex, gated on the respective binaries.

### GitLab git credential helper (glab)

When `glab auth status` succeeds, `init.d/60-glab.sh` registers `!glab auth git-credential` as the git credential helper for **every host in glab's config** (`yq '.hosts | keys | .[]' ~/.config/glab-cli/config.yml`) — gitlab.com and any self-hosted instance the user has run `glab auth login --hostname <host>` for; new hosts need zero code changes. Written with `sudo git config --system` into the container's `/etc/gitconfig`: the host's `~/.gitconfig` is a read-only mount and stays byte-identical, while the system file is container-local and dies with the AutoRemove container. Registration is non-fatal — on failure a warning points at the SSH fallback (`git@<host>:…` keeps working via the RO `~/.ssh` mount).

Primary consumer: private Homebrew taps over HTTPS — `brew tap <name> https://<gitlab-host>/<group>/homebrew-tap.git` clones with the glab token, no prompts, no extra setup (the token already persists in `~/.toolbox/glab`). Benefits any in-container git clone/pull of private GitLab repos.

Limitation: the helper covers **git transports only**. Formulas that download release assets / package-registry artifacts over HTTPS go through brew's curl, which does not consult git credential helpers — such formulas need a custom download strategy reading a token. Revisit if a private tap grows that kind of formula.

### SDD `.gitignore` fence

`toolbox sdd init <name>` writes a fenced block into the workspace `.gitignore`:

```
# >>> sdd-managed/<name> (toolbox)
<glob 1>
<glob 2>
…
# <<< sdd-managed/<name> (toolbox)
```

The block content comes verbatim from `Skill.GitignoreEntries` in `internal/sdd/registry.go`. Patterns (not enumerated paths) are the contract: `.claude/get-shit-done/`, `.claude/skills/gsd-*/`, `.codex/agents/gsd-*` — coverage stays stable across upstream version bumps because new files shipped by a future version still land under one of the documented install roots.

Contract:

- `toolbox sdd init <name>` is host-side and idempotent. Re-running on an unchanged registry leaves the block byte-identical.
- An existing `.gitignore` keeps its non-fence lines intact (the upsert splices the block by fence markers).
- Skills whose upstream installer emits purely user-authored content (bmad) leave `GitignoreEntries` nil; the fence is skipped entirely. The host reports `skipped (skill produces user-authored content)`. openspec is the mixed case: `openspec/{specs,changes}` are user-authored and committed, while the per-tool adapter files under `.claude/skills/openspec-*`, `.claude/commands/opsx/`, `.codex/skills/openspec-*` plus the default `openspec/config.yaml` scaffold are regenerated by `openspec init --force` / `openspec update` on every shell and land in the fenced block. Drop the `openspec/config.yaml` entry per-repo once you customise the `context:` / `rules:` blocks — otherwise the scaffold stays generated and ignored.
- Disabling a skill (removing the `.toolbox.yaml` flag) leaves the orphaned fence block in `.gitignore`. There is no `toolbox sdd uninstall` today — clean it up manually.

### SDD install steps

`sdd.<key>` in `.toolbox.yaml` accepts two shapes (#317):

```yaml
sdd:
  gsd: true            # bool shorthand — registry-default InstallSteps
  gsd:                 # object form — overrides the steps wholesale
    steps:
      - ["--claude", "--global", "--config-dir", "./.claude"]
      - ["--codex", "--local"]
```

The object form implies `enabled: true` (writing it at all is the opt-in; an explicit `enabled: false` next to `steps:` disables while keeping the override around). Steps replace the registry defaults **wholesale**, not per-entry: partial merge would leave no way to drop a default step. Token rules are validated at config load (`config.ValidateSDD`): non-empty, whitespace-free, no `;` — the host→container encoding joins args with spaces and steps with `;`, and the bash bootstrap re-splits on exactly those, so a violating token would shift arg boundaries silently instead of erroring. Unknown keys with a `steps:` override are rejected loudly (bool-shorthand typos stay silently dropped as before — see `sessionplan.sddEnv`). `toolbox sdd init <key>` re-runs leave an object-form entry untouched instead of clobbering it back to `true`.

**gsd claude default is the skill-form layout, kept workspace-local.** Since opengsd#2808, gsd-core emits *only* hyphen-form suggestions (`/gsd-<cmd>`, `bin/lib/runtime-slash.cjs`: "The colon form is never emitted"), which Claude Code routes only for `skills/gsd-<cmd>/` installs. The old `--claude --local` layout wrote `commands/gsd/*.md`, routable only as colon-form `/gsd:<cmd>` — every gsd "Next Up" suggestion pointed at a command that didn't exist. The default step is therefore `--claude --global --config-dir ./.claude`: gsd's "global" (skill-form) layout, but materialised inside the workspace's `./.claude/` so nothing leaks into the shared `~/.claude` (a bare `--global` would write into the host-persisted `~/.toolbox/claude` mount, shared across every workspace) and the per-workspace sentinel semantics stay intact. Hook commands in the generated `.claude/settings.json` come out workspace-relative (`./.claude/hooks/…`) because the `--config-dir` is relative — portable across container/host.

**Sentinel fingerprint covers version AND steps.** The entrypoint sentinel (`~/.toolbox-state/sdd.<key>.<workspace-hash>.version`) stores `<version>|<steps>`, so editing a `steps:` override re-runs the bootstrap on the next shell without waiting for a Renovate version bump. Pre-fingerprint sentinels (bare version) mismatch once and trigger a single idempotent reinstall — deliberate: it migrates every existing gsd workspace off the colon-routed layout.

**Migration residue (upstream gap):** re-running the skill-form install over a legacy `--claude --local` layout cleans `.claude/commands/gsd/` but leaves the gsd hooks block in `.claude/settings.local.json` next to the new `.claude/settings.json` copy — Claude Code merges both, so gsd hooks fire twice until the stale block in `settings.local.json` is removed by hand (one-time).

## Browser bridge

### Architecture

The container has no display server. CLIs inside `toolbox shell` that invoke `xdg-open <url>`, set `$BROWSER`, or expect an OAuth redirect to land somewhere clickable have no fallback by default. The browser bridge plumbs URL opens out to the host's real browser:

```
container                                          host
─────────                                          ────
xdg-open <url>                                     toolbox browser-bridge daemon
  └─ wrapper at tail of Dockerfile                   │ listens on 127.0.0.1:<port>
       │  reads /home/toolbox/.toolbox/browser/      │ (port + token read from
       │  {port,token} (RO bind-mount)               │  ~/.toolbox/browser/)
       └─ POST http://host.docker.internal:<port>   ─┘
              Authorization: Bearer <token>
              body: { "url": "..." }
                                                    └─ open / xdg-open <url>
```

The container-side wrapper is installed at the tail of `internal/build/assets/Dockerfile` (around line 1348, after the `COPY init.d/` step), shadowing `/usr/local/bin/xdg-open` and its synonyms (`sensible-browser`, `gnome-open`, `x-www-browser`, `www-browser`, `open`). `ENV BROWSER=xdg-open` lets tools that honour `$BROWSER` route through the same wrapper.

State lives in `~/.toolbox/browser/` (`HostDir` in `internal/browserbridge/paths.go:16`), mounted **read-only** into the container at `/home/toolbox/.toolbox/browser` (`ContainerDir`, line 20). Four files: `token` (bearer secret, generated at install), `port` (chosen at daemon start), `pid`, `log`. The dir is mode `0700`; the bind keeps the container from rotating either secret.

### Install topology

Per-host supervisor differs by platform — implementation in `internal/browserbridge/agent_darwin.go` and `agent_linux.go`:

| Host | Unit | Registered via |
|------|------|----------------|
| macOS | `~/Library/LaunchAgents/com.filippolmt.toolbox.browser.plist` | `launchctl bootstrap gui/<uid>` |
| Linux | `~/.config/systemd/user/toolbox-browser.service` | `systemctl --user daemon-reload && enable --now` |

Both run the same hidden subcommand `toolbox browser-bridge daemon` in the foreground; the supervisor handles restart-on-crash and login-time start. `toolbox browser-bridge install` writes the unit + token then triggers the bootstrap; `uninstall` reverses both steps. Anything else (status reads, log inspection) is read-only on the state dir.

### Security boundary

The daemon refuses anything that isn't:

1. Bound to `127.0.0.1` (no LAN exposure — checked at listen time).
2. Authenticated with the exact bearer token from `~/.toolbox/browser/token` (constant-time compare).
3. Scheme `http` or `https` only — `file://`, `javascript:`, `data:` etc. are rejected with 400.
4. Below the URL length cap.
5. Within the rate limit.

The container side can read `token` because the mount is RO; an attacker who lands shell-equivalent privileges inside the container can therefore *open URLs* on the host browser, but cannot exfiltrate the token to a different network namespace (the daemon only accepts `127.0.0.1`, and the container's `127.0.0.1` is a different namespace).

### Mount gating

`browser_bridge: true` in config (default — `internal/config/plan.go` seeds it) causes `mountplan.Defaults` (`internal/mountplan/defaults.go:134`) to append the RO bind. Setting `browser_bridge: false` drops the mount entirely; the in-container wrapper has nothing to talk to and falls back to the one-line tip emitted by `cmd/shell.go` ("install the host daemon with `toolbox browser-bridge install`"). The toggle lives outside `tools:` because it's host-side — it does not invalidate the image hash.

### Uninstall surface

```bash
toolbox browser-bridge uninstall   # supervisor unit + plist/service file
rm -rf ~/.toolbox/browser          # token + port + pid + log
```

Both steps are independent: `uninstall` removes only what `install` wrote. The state dir is left behind if a user revokes the daemon but wants to keep the token around (rare; documented for completeness). No system-level files, no `sudo`, no Homebrew formula touch.

### Troubleshooting

- `toolbox browser-bridge status` prints state-dir path, token presence, port, supervisor install + run state, and the platform-specific detail line (`launchctl print` excerpt on macOS, `systemctl --user status` excerpt on Linux).
- Daemon log: `~/.toolbox/browser/log` — opened in append mode by the daemon, no built-in rotation; truncate or `logrotate` it yourself if it grows.
- Container-side wrapper failures (no port file, no host route) surface as a single-line tip on shell entry (`cmd/shell.go`'s "browser-bridge tip" path); the wrapper itself exits non-zero so callers can detect the failure.

## Proximo integration

### Why `.test` is unreachable from a sibling container

[proximo](https://github.com/filippolmt/proximo) makes any labelled Docker container reachable at `https://<name>.<tld>` (default `.test`): it runs Traefik publishing `:80/:443` on the **host**, installs a host resolver mapping `*.<tld> → 127.0.0.1`, and trusts a local CA in the host OS + NSS stores. That works for the host browser. It does **not** work from inside a toolbox container: `127.0.0.1` there is the container's own loopback, not the host where Traefik listens, so DNS resolves but the connection refuses. proximo also never injects its CA into arbitrary containers, so even a reachable endpoint fails TLS verification.

### Enablement is auto-detected (tri-state `proximo`)

`config.Config.Proximo` is a `*bool` (`proximo.Enabled`): an explicit `true`/`false` wins; **omitted (nil) auto-detects** — on iff proximo's root CA exists on the host (`proximo install` wrote it under `~/.proximo`). So a host with proximo installed gets `.test` reachability in every shell with zero per-repo opt-in, and a host without it pays nothing. `proximo: false` opts a project out; `proximo: true` forces it on even when the CA is absent (the mount then soft-skips). `*bool` (vs a plain `bool`) is what makes nil mean "auto" rather than "off" — the same tri-state shape as `browser_bridge`, but with an auto rather than always-on default.

### The two host-side ingredients

`internal/proximo` supplies both. The toolbox CLI runs on the host alongside proximo, so it can discover routed hosts directly from Docker labels — no enumeration in `.toolbox.yaml`, no upstream proximo change, no shared Docker network.

| Concern | Mechanism | Seam |
|---------|-----------|------|
| DNS | Every running container's `proximo.hosts` label value is read; each hostname is pinned to the Docker `host-gateway` and appended to `HostConfig.ExtraHosts`. `host-gateway` resolves to the host where Traefik publishes `:443`, bypassing Docker networks entirely. | discovery: `container/lifecycle.go` `augmentProximoHosts` (needs the Docker client); pure parser: `proximo.ExtraHosts` |
| Cert | proximo's root CA (path queried via `proximo config ca-path` — the stable contract from filippolmt/proximo#20; fallback `~/.proximo/tls/ca.pem`, proximo's state home since v0.3.0, filippolmt/proximo#17) is bind-mounted RO at `/etc/ssl/proximo-ca.pem`. `entrypoint.sh` then establishes **seamless** trust for every client (see below). `NODE_EXTRA_CA_CERTS` (Node uses its own bundle) and `TOOLBOX_PROXIMO_CA` (path pointer for the certifi gap) are also exported. | mount: `proximo.CAMount` injected in `mountplan.Merge`; env: `proximo.Env` appended in `sessionplan.Plan`; trust: `entrypoint.sh` proximo block |

### Trust establishment (entrypoint, self-gated on the mount)

`entrypoint.sh` runs a proximo block gated purely on `[ -f /etc/ssl/proximo-ca.pem ]` (the mount is the signal — no extra env). It is idempotent and every step is best-effort (`|| true`) so a trust failure never aborts boot:

| Client | Trusted via | Notes |
|--------|-------------|-------|
| curl / git / wget / python (`ssl`, `urllib`) | system bundle | `sudo cp` into `/usr/local/share/ca-certificates/proximo.crt` + `sudo update-ca-certificates` (passwordless sudo; refreshed only when the cert changes via a `cmp` guard) |
| Chromium / Firefox (incl. Playwright's bundled browsers) | NSS | `certutil -A -t C,, -n proximo` into `$HOME/.pki/nssdb` (`libnss3-tools`, base apt layer). `~/.pki` is a HOME subdir, not a bind-mount → ephemeral, rebuilt from the mounted CA every shell |
| Node / Playwright (node API) | `NODE_EXTRA_CA_CERTS` | additive, set by `proximo.Env` |
| python-requests | — | uses certifi, not the system store; set `REQUESTS_CA_BUNDLE="$TOOLBOX_PROXIMO_CA"` (this is the one non-seamless client) |

### Boundaries and caveats

- **Discovery runs at container create only.** `ExtraHosts` is fixed at `ContainerCreate` (same as port bindings — see [port-bindings](#port-bindings-are-fixed-at-container-creation)). New `.test` hosts that come up after `toolbox shell` are invisible until the next recreate. Stopped proximo stack → `augmentProximoHosts` warns and degrades to "names unreachable" rather than failing the shell.
- **CA mount is pure host-fs.** `CAMount` is added in `Merge` (not `defaults()`), so the canonical default-mount set and the smoke-test init.d/completion bijections are untouched (the proximo trust step lives in `entrypoint.sh`, not an `init.d` script, so it ties to no catalog tool). A missing CA file (proximo not installed) soft-skips the mount with a `resolveAll` warning; `proximo.Env` independently gates on file existence so Node never points at an absent `NODE_EXTRA_CA_CERTS`, and the entrypoint block no-ops when the mount is absent.
- **Image dependency**: the NSS trust step needs `certutil`, shipped via `libnss3-tools` in the base apt layer; `smoke-test.sh` asserts `certutil` is present. This is the one image-side cost of the integration; everything else is host-side and image-hash-neutral.
- **Host-side toggle, no image-hash impact** — same property as the browser bridge: `proximo` lives outside any image concern.

## Runtime privacy

### rtk hook auto-wiring + telemetry/tee lockdown

`internal/build/assets/init.d/10-rtk.sh` runs `rtk init -g` (Claude) and `rtk init -g --codex` (Codex) on every shell so the Bash-tool rewrite hook stays registered even after a settings reset or a fresh `~/.toolbox/.claude` bind-mount. Gated on `command -v claude` / `command -v codex` so opted-out tools never have rtk hooks injected. Idempotent; failures are non-fatal.

Privacy is enforced at the env layer image-wide:

- `RTK_TELEMETRY_DISABLED=1` blocks every telemetry code path regardless of consent state.
- `RTK_TEE=0` blocks the tee feature regardless of `[tee] enabled` in the TOML — so failed-command stdout (which often carries auth tokens from `gh auth status`, `aws sts`, `curl -H Authorization:`) is never written to disk under `~/.local/share/rtk/`.

The entrypoint additionally pre-seeds `~/.config/rtk/config.toml` with `[tee] enabled = false` and `[telemetry] enabled = false` on first launch (belt-and-braces, so `rtk telemetry status` reports a consistent state and unsetting either env var still inherits safe defaults). Seed gated on file absence — env vars are the load-bearing defense for users with a stale config.toml from before the seed existed, and survive `rtk telemetry enable/disable` rewriting the whole TOML.

### Claude Code env-var matrix

The image sets:

- `DISABLE_AUTOUPDATER=1` — block background CLI self-update (`/usr/local/lib/node_modules` is root-only).
- `FORCE_AUTOUPDATE_PLUGINS=1` — documented escape hatch from the [discover-plugins guide](https://code.claude.com/docs/en/discover-plugins#configure-auto-updates) that keeps plugin updates running even when the CLI auto-updater is disabled.
- `DISABLE_FEEDBACK_COMMAND=1`, `DISABLE_ERROR_REPORTING=1` — privacy (`/bug` ships conversation data to Anthropic; error reporting ships stack traces).
- `USE_BUILTIN_RIPGREP=0` — use the apt-pinned system `rg` (base layer) instead of the npm-bundled binary.
- `CLAUDE_BASH_MAINTAIN_PROJECT_WORKING_DIR=1` — return to the project dir after every Bash call; prevents cd-drift across tool invocations.
- `BASH_DEFAULT_TIMEOUT_MS=300000`, `BASH_MAX_TIMEOUT_MS=1200000` — 5m default / 20m ceiling (upstream 2m/10m); in-container image builds and test suites routinely exceed the upstream default.
- `CLAUDE_CODE_ENABLE_TASKS=1` — structured cross-session task tools; state persists in the `~/.claude` bind.
- `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` — opt into agent teams (experimental upstream; in-process mode needs no tmux).

Host-side (not baked — set per-shell by `sessionplan.shellEnv`): `CLAUDE_REMOTE_CONTROL_SESSION_NAME_PREFIX=<workspace basename>`. The upstream default is the machine hostname, which inside the container is the Docker container ID — every Remote Control session on claude.ai web/mobile would be named hex gibberish.

`DISABLE_TELEMETRY` is intentionally NOT set (dropped 2026-06, was part of the original lockdown): the Statsig channel doubles as feature-flag delivery, and blocking it is reported to break Remote Control entitlements and preview-feature rollouts ([anthropics/claude-code#58383](https://github.com/anthropics/claude-code/issues/58383) — community-reported, not formally documented; re-verify before re-adding the flag). It carries product analytics, not training data: the model-improvement opt-out is the claude.ai account-level privacy setting ("Help improve Claude"), and admin usage analytics ([claude.ai/analytics](https://code.claude.com/docs/en/analytics)) are collected server-side regardless of any client flag.

The `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` umbrella flag is likewise NOT set: baking it into the image was correlated with intermittent OAuth re-login prompts on long-lived shells, suggesting the umbrella gates undocumented behaviour beyond its stated sub-flags.

Remote Control (`claude --remote-control`, pairs the session with claude.ai web/mobile) is outbound-HTTPS-only — no inbound ports, so it works inside the container without `-p` bindings or the loopback bridge. The `/config` toggle "Enable Remote Control for all sessions" persists in the `~/.claude` bind and survives container recreates.

When a plugin is refreshed Claude Code prompts for `/reload-plugins`. Bumping the CLI itself is a Dockerfile concern: `CLAUDE_CODE_VERSION` + image rebuild (Renovate-driven).

## Host CLI internals

### Shared fs primitives

Three host-filesystem primitives were copy-pasted across packages as the CLI grew: home-directory resolution (`os.UserHomeDir` + the literal `"resolve home directory: %w"` in six packages — only `configio` guarded the empty-`$HOME` case, the rest would silently `filepath.Join("", …)`), tilde expansion (`mountplan.expandHome`, re-inlined in `inherit_host_auth.go`), and crash-safe atomic writes (`configio.AtomicWriteFile`, not reused by `browserbridge/token.go`'s bare `os.WriteFile`).

`internal/fsx` collapses them into one stdlib-only leaf package (no import-cycle risk, so every package can depend on it):

- `fsx.Home()` — strict resolution, **with** the empty-`$HOME` guard. Adopting it at the five sites that lacked the guard is strictly safer: they already hard-failed on a `UserHomeDir` error and now also fail loud on an empty `$HOME` instead of joining onto `""`.
- `fsx.ExpandTilde(p, home)` — moved verbatim from `mountplan.expandHome`; `resolve.go` and `inherit_host_auth.go` both call it.
- `fsx.AtomicWriteFile(dest, data, mode)` — implementation moved from `configio`; `browserbridge/token.go` now reuses it.

`configio.GlobalConfigDir` / `configio.AtomicWriteFile` are kept as thin facades over `fsx` so `cmd/*` keeps a single config-IO import surface and existing callers/tests are untouched — the implementation lives once, in `fsx`.

Deliberately **not** routed through `fsx.Home`: the best-effort `home, _ := os.UserHomeDir()` sites (`config/plan.go` global-config read, `mountplan.Merge`'s pre-stat, `cmd/shell_named.go`) that must tolerate an empty home rather than hard-fail. `fsx`'s package doc reserves these for direct `os.UserHomeDir` use; routing them through the loud `Home()` would invert their contract. Likewise `config.ValidateMountsRoot`'s `~`/`~/` checks are *validation* (classifying a string), not expansion, so they do not call `ExpandTilde`.

The linux/darwin browser-bridge service supervisors share their template-render and mkdir-then-write skeletons via `renderTemplate` / `writeServiceFile` in the non-tagged `browserbridge/agent.go`; the platform files keep only the genuinely divergent content (systemd unit vs launchd plist, `systemctl` vs `launchctl`).

### `env:` passthrough

Arbitrary env vars injected into the in-container shell via an `env:` map in `.toolbox.yaml`. Motivating case: opt-in env-gated CLI features like `CLAUDE_CODE_WORKFLOWS=1` (Workflow tool), which previously needed a persisted `~/.zshrc` edit.

```yaml
env:                              # top-level — applies to every toolbox shell
  CLAUDE_CODE_WORKFLOWS: "1"
  CLAUDE_CODE_EFFORT_LEVEL: medium

shells:
  infra:
    path: /tmp/infra
    env:                          # per-shell — overlays the top-level map
      AWS_PROFILE: prod
```

Contract:

- **Scope.** Resolved by the normal config load order (`--config` → walked-up `.toolbox.yaml` → `~/.toolbox.yaml` → `TOOLBOX_*` env → defaults), so the top-level `env:` is set globally (`~/.toolbox.yaml`) or per-project (`.toolbox.yaml`). The per-shell `shells.<name>.env` overlays it for that named shell only — per-shell keys win on collision (`config.Config.EffectiveEnv`, keyed by the *raw* `shells:` name, not the sanitized container suffix).
- **Emission order.** `sessionplan` emits the curated `TOOLBOX_*`/`PWD`/SDD entries first, then the loopback-bridge markers, then the user env sorted by key (`sessionplan.userEnv`, deterministic for tests).
- **Reserved keys.** `config.ValidateEnv` rejects empty keys, keys containing `=`, and any key with the `TOOLBOX_` prefix or the literal `PWD` — those are owned by the curated contract. Same rules apply per-entry under `shells.<name>.env` (errors namespaced as `shells.<name>.env: …`). Empty *values* are allowed (`export VAR=`).
- **Hash-neutral.** Lives outside the removed `tools:` block, like `sdd:` / `browser_bridge:` — flipping a key never invalidates the image hash. Takes effect on the next container create (`toolbox stop` first to refresh an existing one).
