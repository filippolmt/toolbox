# Runtime notes

Deep gotchas pulled out of `CLAUDE.md` to keep the AI-loaded context lean. Reference these from a top-level summary in `CLAUDE.md` instead of inlining.

## Image build

### Host UID mapping

The CLI runs the container with `--user $(id -u):$(id -g)`. Because the runtime UID rarely matches the baked `toolbox` user (UID 1000), `/home/toolbox` is made world-writable in the image. Don't revert to a fixed UID without understanding why — host file ownership would invert and writes inside `~/.toolbox/` would fail for anyone whose host UID isn't 1000.

### Docker CLI checksum

Layer 7 of `internal/build/assets/Dockerfile` installs the static Docker CLI binary without a SHA256 verification step because Docker doesn't publish `.sha256` files for those releases. Version pin + HTTPS is the only guard. Tracked as accepted risk T-01-08.

### Tool version pinning

Every external binary in the Dockerfile is pinned by version + SHA256 (exceptions: Docker CLI — see above — and gcloud, which uses a Google APT repo). Renovate bumps them. Adding a new tool is now a 2-edit (or 3-edit when a runtime init script is needed) operation:

1. New row in `internal/catalog/catalog.go` `Entries`.
2. New install `RUN` block in `internal/build/assets/Dockerfile`.
3. (optional) New `init.d/<NN>-<tool>.sh` if `InitScript` is set on the catalog row.

There is no per-tool opt-out: every CLI is installed unconditionally. The `ARG INSTALL_<TOOL>` build-arg pattern was removed (see [#276](https://github.com/filippolmt/toolbox/issues/276)). Use `inherit_host_auth:` in `.toolbox.yaml` to share host credentials with the container — see the [inherit-host-auth](#inherit-host-auth) section.

### rtk arm64 is built from source

Dockerfile `rtk-builder` stage + Layer 13c. Upstream only ships `aarch64-unknown-linux-gnu` linked against GLIBC 2.39, but the base image (`node:24-bookworm-slim`) ships GLIBC 2.36 — the prebuilt binary aborts with `'GLIBC_2.39' not found`. There is no `aarch64-unknown-linux-musl` release.

Fix: multi-stage build. A `rust:1-slim-bookworm` stage runs `cargo install --git rtk-ai/rtk --tag v${RTK_VERSION} --locked` against the bookworm sysroot (so the resulting binary ABI-matches the runtime), and Layer 13c COPYs it into place. The same stage handles the amd64 tarball download too, so Layer 13c is a single COPY + version check.

The base image can't move to Debian trixie yet because the Microsoft Azure CLI apt repo currently has no trixie suite.

### Rust base image tag scheme

`<ver>-slim-<distro>`, not `<ver>-<distro>-slim`. Docker Hub publishes `rust:1-slim-bookworm` (correct) but **not** `rust:1-bookworm-slim` (404). PR #89 hit this — the rtk-builder stage failed at image-pull time. When bumping or referencing the rust base, use `<ver>-slim-<distro>` (or bare `<ver>-slim` for the default trixie variant when we eventually move).

### Slim Rust images ship no `curl` / `ca-certificates`

`rust:1-slim-bookworm` contains cargo + git but nothing to fetch tarballs with. The rtk-builder stage installs them via apt before the amd64 tarball path. If you copy the pattern for another tool (e.g. building a Rust binary from source), replicate the apt install — it doesn't propagate from the base.

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

Validation in `config.Plan` rejects unknown keys and keys whose catalog entry lacks `HostAuthMount`. The mount is always read-only — the container can read host credentials but cannot mutate them; a misbehaving CLI inside the container cannot corrupt the host's `~/.config/gh/hosts.yml`. Login flows that need to write (e.g. `gh auth login`) must run on the host.

Mount semantics: when a key is listed in `inherit_host_auth`, the default `~/.toolbox/<key>` mount is dropped (not supplemented) — two mounts at the same container target would shadow unpredictably. User `mounts:` patches keying on the same `name:` still compose on top of the inherited mount.

`mounts_root` interaction: if both `mounts_root: /custom` and `inherit_host_auth: [<key>]` are set, the `mounts_root` retargeting is bypassed for that key — host inheritance pulls from the host's canonical path (e.g. `~/.config/gh`), not from `/custom/gh`. `mounts_root` still applies to every other default mount. If you need the credential dir on an encrypted volume, choose one approach or the other.

Pre-stat check: `inherit_host_auth: [<key>]` requires the host source path to exist at config-load time. If it does not, `toolbox shell` fails with a clear error pointing at the missing path — silent soft-skip would have left the container with no credential mount at all (worse than failing loud).

Read-write inheritance: inherited mounts are read-write. Most listed CLIs refresh tokens or update session state during normal use (atuin appends history, claude/codex write session state, gh/docker rotate OAuth refresh tokens) — RO would EROFS those writes. You opt in explicitly: your host credential dir is now writable by container processes.

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

When `-B` is set together with at least one `-p`, `sessionplan.Plan` emits `TOOLBOX_LOOPBACK_BRIDGE_PORTS=<comma-joined>` and `internal/build/assets/init.d/70-loopback-bridge.sh` spawns one `socat TCP-LISTEN:<port>,bind=$ETH_IP,fork,reuseaddr TCP:127.0.0.1:<port>` per port. The bridge binds the container's external interface IP (`hostname -i`) explicitly so a legitimate in-container `0.0.0.0:<port>` listener does not collide with it.

Standard recipes (static-port loopback CLIs):

```
toolbox shell -B -p 13387:13387   # shopify store auth
toolbox shell -B -p 8976:8976     # wrangler login
```

**Dynamic-port carve-out.** `cf` picks its callback port at run time from the range `startPort: 8877, maxPortAttempts: 10`. The bridge needs a known container port to forward, so `cf` cannot use it — the existing build-time `sed` patch (Dockerfile Layer 13c) that rewrites `127.0.0.1` → `0.0.0.0` is retained for `cf` and similar dynamic-port CLIs. `cf login` recipe (no `-B` needed):

```
toolbox shell -p 8877-8886:8877-8886   # cf login (sed-patched, range syntax via nat.ParsePortSpec)
```

OAuth CLI survey:

| CLI | Listener style | Strategy |
|---|---|---|
| `shopify` | `127.0.0.1:13387` (static) | bridge: `-B -p 13387:13387` |
| `wrangler` | `localhost:8976` (static) | bridge: `-B -p 8976:8976` (vanilla wrangler, no sed) |
| `cf` | `127.0.0.1:8877-8886` (dynamic) | build-time sed → `0.0.0.0` + `-p 8877-8886:8877-8886` |
| `gcloud` | `localhost:8085+` (dynamic) | wrapper / device-code |
| `gws` | `127.0.0.1:0` (ephemeral) | wrapper / device-code |
| `az` | dynamic | device-code (`--use-device-code`) |
| `gh` / `glab` / `claude` / `codex` | none — device-code | no listener; no port forward needed |

Limitations:

- Bridge env is fixed at `ContainerCreate`. `toolbox shell -B …` on a container created without `-B` is a no-op — same UX as `-p`. Run `toolbox stop` first.
- IPv4 only. Docker port-forward IPv6 support is patchy; not in scope.
- `-B` without `-p` is not an error. The init.d script logs a one-line `loopback bridge: enabled but no -p ports published — skipping` warning so the misconfiguration is visible.
- Per-port failure (e.g. `EADDRINUSE` because another in-container process already binds `eth0:<port>`) is logged to `~/.toolbox-state/init/70-loopback-bridge.log` and the loop continues with the remaining ports. The bridge never aborts boot.
- `socat` is part of the always-on Layer 1 apt-install set (~350KB) — no per-tool opt-out. The bridge feature is system-level, not a catalog tool.

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

### MCP plugin auto-build

`internal/build/assets/init.d/50-mcp-plugins.sh` scans `~/.claude/plugins/cache/**` and runs `npm install && npm run build` for any plugin missing a `dist/`. First shell after a plugin install is therefore slower; subsequent shells cached via `.toolbox-built` marker. On failure stderr is captured to `.toolbox-build-error.log` next to the marker (in the same bind-mounted plugin dir, so it survives container restarts) and the last 5 lines are printed inline; failure stays non-fatal.

### `cf` Cloudflare CLI skill auto-install

When `tools.cf` is enabled (default) and `~/.claude` exists, `internal/build/assets/init.d/20-cf.sh` writes a Claude Code skill to `~/.claude/skills/cf/SKILL.md` if absent. Skill is hand-written and points Claude to `cf agent-context <product>` for on-demand product context (instead of pre-baking the ~107-product corpus). Idempotent — only re-creates when the file is missing, so user edits persist.

### Skill discovery paths diverge between Claude and Codex

Claude Code reads only `~/.claude/skills/<name>/SKILL.md` (per docs.claude.com); Codex CLI reads only `~/.agents/skills/<name>/SKILL.md` (Agent Skills USER scope per agentskills.io). Despite the shared "Agent Skills" branding, the two locations are NOT mutually compatible. CLI wrappers that ship a SKILL.md need a dual-install pass to be visible in both agents. Reference: `internal/build/assets/init.d/60-glab.sh` runs `glab skills install --path ~/.claude/skills --force` for Claude and `glab skills install --global --force` for Codex, gated on the respective binaries.

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
- `DISABLE_TELEMETRY=1`, `DISABLE_FEEDBACK_COMMAND=1`, `DISABLE_ERROR_REPORTING=1` — privacy.

Together those four `DISABLE_*` flags cover what the [env vars](https://code.claude.com/docs/en/env-vars) page calls the expansion of `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`. The umbrella flag itself is intentionally NOT set: baking it into the image was correlated with intermittent OAuth re-login prompts on long-lived shells, suggesting the umbrella gates undocumented behaviour beyond its four stated sub-flags.

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
