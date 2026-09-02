# Configuration

Reference for `.toolbox.yaml`: every supported key, the loading order, and the `TOOLBOX_*` environment overrides. Source of truth: `internal/config/config.go` (schema + validation) and `internal/configexample/render.go` (annotated template).

## Getting started

```bash
toolbox init             # write an annotated .toolbox.yaml in the current directory (--force to overwrite)
toolbox config example   # print the same annotated template to stdout
toolbox config show      # print the fully-resolved configuration (--origin annotates each key's layer)
toolbox config doctor    # validate without modifying
toolbox config ui        # interactively view/edit keys across the global & repo layers (needs a TTY)
```

Prefer not to hand-edit YAML? [`toolbox config ui`](commands.md#config-ui) is an interactive, provenance-first editor for every key below — it shows both the effective value and what each layer (global / repo) sets, and writes through the same validated, comment-preserving path as `config set`. The full `toolbox config` subcommand tree is documented in [commands](commands.md#toolbox-config).

## Loading order

Configuration is loaded from (highest priority first):

1. `TOOLBOX_*` environment variables — **only** the four env-bound keys (`image`, `registry_mirror`, `pull`, `bridge`); for those they override every file layer below. Every other `TOOLBOX_*` var is ignored (viper's `AutomaticEnv` binds only these keys).
2. `--config` flag
3. The nearest `.toolbox.yaml` walking up from the current working directory (search stops at `$HOME` or the filesystem root) — running `toolbox shell` from any subdirectory of a workspace still picks up that workspace's project config
4. `~/.toolbox.yaml` (global)
5. Built-in defaults

## Key reference

| Key | Type | Default | Purpose |
|-----|------|---------|---------|
| [`mounts`](mounts.md#mounts-merge-semantics) | list | built-in defaults | Patch / replace / append / disable the default bind mounts by `name`. |
| [`mounts_root`](mounts.md#mounts_root-retarget) | string | `""` | Retarget every `~/.toolbox/`-managed default mount to a custom root. |
| [`inherit_host_auth`](#inherit-host-auth) | list | `[]` | Opt listed CLIs into the host's real credential path instead of the isolated default. |
| [`shells`](shells.md) | map | – | Named shell shortcuts: `<name>: {path, env}`. |
| [`shell`](#shell) | string | `zsh` | Login shell inside the container (only `zsh` is supported). |
| [`agent`](#agent) | string | `claude` | Default AI agent auto-launched by [`toolbox worktree`](commands.md#toolbox-worktree): `claude` or `codex`. |
| [`image`](#image-selection) | string | `""` | Full image ref override (pull-source concern). |
| [`registry_mirror`](#image-selection) | string | `""` | Swap only the registry host of the canonical ref. |
| [`pull`](#image-selection) | string | `auto` | Registry-sync policy for the shell-start refresh *and* the background prefetch: `auto` / `always` / `never`. |
| [`sdd`](sdd.md) | map | – | Per-repo Spec-Driven-Development skill packs (`gsd`, `bmad`, `openspec`). |
| [`bridge`](bridge.md) | bool | `true` | Mount the host bridge state dir (browser / editor / proximo forwarding). |
| [`browser_bridge`](#browser_bridge-deprecated) | bool | – | **Deprecated** alias of `bridge`. |
| [`proximo`](proximo.md) | bool | auto | `.test` reachability + CA trust; omitted = auto-detect (on iff proximo's CA exists on the host). |
| [`managed_statusline`](#managed_statusline) | bool | `true` | Image-owned Claude Code statusline, re-applied every shell start; `false` keeps your own. |
| [`image_reclaim`](#image_reclaim) | bool | `true` | Remove the runtime images this CLI pulled that a later image update left nameless; `false` keeps every generation. |
| [`peer_messaging`](#peer_messaging) | bool | `true` | Let Claude Code sessions in different toolbox containers see and message each other. |
| [`env`](#env-passthrough) | map | – | Arbitrary env vars injected into the in-container shell. |
| [`worktree`](#worktree) | map | – | Tune `toolbox worktree` sessions; `seed` adds extra gitignored paths to carry into a new worktree. |

## `shell`

Login shell inside the container. Only `zsh` is supported (the default); `bash` is rejected at config load with an explicit migration hint (`config.ValidateShell`).

## `agent`

Default AI agent auto-launched by [`toolbox worktree`](commands.md#toolbox-worktree) sessions. Accepts `claude` or `codex` — the two agents baked into the canonical image (`config.ValidateAgent`). Resolved with precedence `--agent` flag > this key > the default `claude`, so the flag is optional once a default is set. Honouring the standard [loading order](#loading-order), it can be set globally (`~/.toolbox.yaml`) for a per-user default or per-directory (`.toolbox.yaml`) to pin a project to one agent.

Set it via `toolbox config set agent <value>` ([`--where`](commands.md#--where-targeting) selects global vs local). The key has no default written to disk: when unset it resolves to `claude` at launch, and `config show` renders that resolved value (`agent: claude`). A non-canonical `image:` lacking the chosen agent fails at launch, not at validation.

## `managed_statusline`

The runtime image ships a curated Claude Code statusline and applies it to every container by force-setting `~/.claude/settings.json` `statusLine` on each shell start (only that key is rewritten — everything else in your settings is preserved). It is image-owned policy: a local edit to the statusline is overwritten on the next shell, so **change it via a PR to this repo**, not in the container.

Every segment is conditional except the working directory — the line shows only what applies right now:

```
 …/github/toolbox │  toolbox:main*  feat-xyz │  #1234 │  Opus 5 high FAST │  ACCEPT
   │ @reviewer │ vim:NORMAL │ ▰▱▱▱▱ 22% │  1h15m │ 5h 24% 17:00 · 7d 41% 06/02 17:00
```

Left to right: working directory, `repo:branch` with dirty/ahead/behind markers and the linked-worktree name, the open PR for the branch (clickable, coloured by review state), model with reasoning effort and fast mode, permission-mode badge, custom agent, vim mode, output style, behavioural-mode badge, context-window bar, session duration, and 5-hour / 7-day rate-limit usage with reset times.

![The managed statusline rendered in a toolbox shell, in colour with Nerd Font glyphs.](img/statusline.png)

Set `managed_statusline: false` to opt out — the boot hook then leaves your own `statusLine` untouched. Default (omitted or `true`) is managed-on. Mechanics in [shell-start internals](internals/shell-start.md#managed-statusline).

## `image_reclaim`

Every merge to `main` publishes a runtime image, so a developer who keeps up accumulates a local store of images that lost the `latest` tag and nothing else. `image_reclaim` removes them: on every shell start, once the session's own container exists and references the current image, a background sweep deletes each image in the local store that carries a repo digest for the toolbox repo, has no tag left, and is not the digest this session runs.

Three properties are worth knowing before you leave it on, which is the default:

- **Nothing in use is ever removed, and toolbox does not decide what "in use" means.** The sweep calls Docker's own unforced image removal and treats a refusal as the answer. The daemon refuses to delete an image any container references — *including a stopped one* — so a container of another workspace, waiting for you to come back to it tomorrow, keeps its image. The corollary is the common surprise: with many workspaces whose containers still exist, the sweep reclaims little or nothing, and that is correct rather than broken. Same for a `~/.toolbox/Dockerfile` overlay — your `:local` image is built on top of the base, so the base has a child and stays.
- **No generations are kept.** Every untagged generation goes; there is no keep-the-previous-one rule and no grace window. Nothing rolls back onto an old image — [session reload](session-reload.md) moves forward only — so a retained generation has no use, and the current image is excluded by name.
- **It is silent unless it removed something.** One summary line when it did, nothing at all when the daemon refused or there was nothing to sweep.

Set `image_reclaim: false` to keep every image the CLI ever pulled; a disk-space cleanup is then yours to run (`docker image prune`, which sweeps the whole machine and not just toolbox). Reasoning and the two consequences in full: [ADR 0007](adr/0007-daemon-refusal-as-in-use-check.md).

## `peer_messaging`

Claude Code's [cross-session messaging](https://code.claude.com/docs/en/cross-session-messaging) (`ListAgents` / `SendMessage`) delivers a message to another session on the same machine. Between two toolbox containers it does not work out of the box: sessions already share one registry (the `claude` mount binds a single `~/.toolbox/.claude` everywhere), but each container has its own `/tmp` — so the inbox sockets are unreachable — and its own PID namespace, so the registry's pids do not resolve.

Toolbox makes both hold by default. Participating containers join one toolbox-owned anchor container's PID namespace and mount one toolbox-owned Docker volume (`toolbox-cc-socks`) at `/tmp/cc-socks`, so peers become both discoverable and reachable. The typical use: hand a task to a session already open on another repo, without mounting that repo into this one.

**Default on**, with one thing to know before leaving it there: containers sharing a PID namespace can see each other's process table. Workspaces that must not see each other — different clients, say — need `peer_messaging: false`, either globally in `~/.toolbox.yaml` or in the project's own `.toolbox.yaml`, which wins over the global one. Per-session override: `toolbox shell --peer=false` declines it for one run (`--peer` asks for it against a config that turns it off).

The setting is part of the container identity: a participating session runs in its own container, distinct from the isolated one for the same workspace (the name gains a `.peer` suffix — a separator no shell name can produce, so the two can never collide). `HostConfig` is fixed at `ContainerCreate`, so changing the setting for an existing container means stopping that one container first: the shell detects the mismatch — in either direction — and names the `toolbox stop <container>` to run, which is the targeted alternative to `toolbox stop --all`. The same warning covers a container that carries the right namespace but not the `toolbox-cc-socks` volume: one created before the socket directory became a volume, or while that volume was unavailable. The socket directory is a Docker volume rather than a host bind on purpose: Claude Code `chmod`s each inbox socket right after binding it, and Docker Desktop for macOS serves host binds over virtiofs, where `chmod(2)` on a socket inode fails with `EINVAL` — the listener never starts and no session is reachable, its own included. The volume is created on the first participating shell and initialised once by a throwaway root container, which `chown`s it to your UID/GID and tightens it to `0700`; Claude Code answers a looser directory by silently falling back to a private one. `toolbox stop --all` leaves the volume in place — it holds nothing but live sockets, and reusing it skips the init. Removing it with `docker volume rm toolbox-cc-socks` while no participating shell is running is safe: the next shell re-creates and re-initialises it, on the reattach path as well as on a fresh container. Upgrading from a toolbox that predates the volume leaves `~/.toolbox/cc-socks` behind on the host — dead state, nothing reads it, delete it.

The anchor (`toolbox-peer-anchor`) runs the toolbox runtime image with a bare `sleep` entrypoint, is created on the first participating shell — with the default on, effectively the first shell you open — and outlives the sessions referencing it. It is swept up by `toolbox stop --all` and hidden from `toolbox list` — it is infrastructure, not a shell. If it cannot be created the shell starts anyway, without peer messaging, and warns.

This rests on Claude Code implementation detail (a pid-keyed registry and a liveness check), not on a documented contract: an upgrade can end it without notice. The supported alternative is Remote Control on both containers, which routes through Anthropic servers. For fire-and-forget delegation you need none of this — DooD is a default mount, so `docker exec -w <workspace> <other-container> claude -p "…"` already runs in the other repo with the shared credentials. Rationale and rejected options: [ADR 0003](adr/0003-cross-container-peer-messaging.md).

## inherit-host-auth

`inherit_host_auth: [<key>, …]` in `.toolbox.yaml` opts the listed CLIs into reading the host's standard credential path instead of the isolated `~/.toolbox/<key>/` default. Default is `[]` — fully isolated, matches the pre-#276 behavior.

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

Validation in `config.Plan` rejects unknown keys and keys whose catalog entry lacks `HostAuthMount`.

Mount semantics: when a key is listed in `inherit_host_auth`, the default `~/.toolbox/<key>` mount is dropped (not supplemented) — two mounts at the same container target would shadow unpredictably. User `mounts:` patches keying on the same `name:` still compose on top of the inherited mount.

`mounts_root` interaction: if both `mounts_root: /custom` and `inherit_host_auth: [<key>]` are set, the `mounts_root` retargeting is bypassed for that key — host inheritance pulls from the host's canonical path (e.g. `~/.config/gh`), not from `/custom/gh`. `mounts_root` still applies to every other default mount. If you need the credential dir on an encrypted volume, choose one approach or the other.

Pre-stat check: `inherit_host_auth: [<key>]` requires the host source path to exist at config-load time. If it does not, `toolbox shell` fails with a clear error pointing at the missing path — silent soft-skip would have left the container with no credential mount at all (worse than failing loud).

Read-write inheritance: inherited mounts are read-write. Most listed CLIs refresh tokens or update session state during normal use (atuin appends history, claude/codex write session state, gh/docker rotate OAuth refresh tokens) — RO would EROFS those writes. You opt in explicitly: your host credential dir is now writable by container processes.

macOS keychain caveat: `gh` on macOS stores its OAuth token in the system keychain by default — `~/.config/gh/hosts.yml` carries the account but no `oauth_token`, so inheriting that dir mounts a token-less config and `gh auth status` inside the container reports the token invalid. Workaround: re-login on the host with `gh auth login --insecure-storage` (persists the token into `hosts.yml`), or skip inheritance for gh and log in once inside the container (isolated `~/.toolbox/gh` survives recreates). The same class of issue applies to any CLI that delegates secret storage to an OS keychain.

## Image selection

`toolbox shell` defaults to `ghcr.io/filippolmt/toolbox:latest`. There is no per-tool opt-out, no local-hash fallback, no auto-build branch. `toolbox build` is the explicit escape hatch — it overwrites the local cache of the canonical tag so the next `toolbox shell` picks up the freshly built image. Run `docker pull ghcr.io/filippolmt/toolbox:latest` to restore the upstream copy.

**Source relocation (opt-in).** The ref and pull behaviour are configurable — globally (`~/.toolbox.yaml`), per-repo (`.toolbox.yaml`), or via `TOOLBOX_*` env — for users who serve the image from a proxy hub / pull-through cache (Harbor, Artifactory, Nexus, ECR pull-through). `internal/build.ResolveImage(image, registryMirror)` owns the precedence, highest first:

- `image` — full ref override, used verbatim. Highest. Caveat: a local `toolbox build` tags the *canonical* ref, so with a full override `imageplan.Ensure` looks for the override ref and won't find the local build — `image` is a pull-source concern, not a build target.
- `registry_mirror` — swaps only the registry host of the canonical ref, preserving `filippolmt/toolbox:latest` (host split via `build.SplitRegistryHost`, shared with `imagepull.registryOf`). The relocated image is byte-identical, so a `registry_mirror` *does* satisfy `Ensure`. **The mirror is also authoritative for the update probe**: detection goes through the daemon (`DistributionInspect`), not to canonical GHCR, because the only probe worth making is the one that leads to a pull — announcing an image the mirror cannot serve would be noise. Perceived latency for a new image is therefore the mirror's. Caveat: a pull-through cache that hasn't ingested the image yet fails the first shell with `manifest unknown` — warm it (or pre-seed locally with `pull: never`), see [troubleshooting](troubleshooting.md#manifest-unknown-with-a-registry-mirror).
- neither — the canonical default.

The `pull` policy (`auto` default | `always` | `never`) governs **two acts**: the registry refresh at shell start, and the [background update prefetch](session-reload.md) that runs for as long as the shell is attached.

| `pull` | shell start | background prefetch | banner |
|---|---|---|---|
| `auto` (default) | asks (see below), then `imagepull.ForcePull` | on, one probe per 30 min shared across your sessions | yes |
| `always` | `imagepull.ForcePull`, no question | on, **same cadence as `auto`** | yes |
| `never` | no registry round-trip (air-gapped — `Ensure` still hard-requires the image locally) | off | silent |

`always` therefore differs from `auto` at shell start and nowhere else: forcing a pull on every background tick would spend real bandwidth for the whole session, and adoption is a fresh container either way. Cross-cutting: under any policy the prefetch abstains while the resolved ref carries no repo digest — the fingerprint of a local `toolbox build`, so an automatic download never overwrites one you asked for.

Env override requires the keys to be viper-seeded (`SetDefault` in `config.Merge`) — `AutomaticEnv` only resolves `TOOLBOX_*` for keys it already knows, and `TOOLBOX_PULL=never toolbox shell` silences refresh and prefetch together for one run with nothing written to disk. Edit via `toolbox config set --where global|local [--image|--registry-mirror|--pull]` (empty value resets the key).

### The start-up refresh prompt

Under `auto`, when the registry is ahead of your local image store, `toolbox shell` **asks** before spending your time on the download, with a visible countdown (`promptWindow` in `internal/imageplan`) so a few seconds of silence cannot be mistaken for a hang:

```
  A newer runtime image is available. Download it now? [Y/n] (Ns)
```

`N` is the seconds left, redrawn in place until it runs out.

One keypress is the whole answer — the terminal is in raw mode for the length of the question, so nothing waits on a Return behind it, and `ctrl+c` still stops `toolbox shell` outright rather than only declining the download. `y`, a bare Return, or letting the countdown run out all download it and start the session on the new image. **`n` is "later", not "no"**: the session starts on the image already in your store while the [background prefetch](session-reload.md) fetches the new one anyway, so `toolbox-reload` moves you onto it whenever you want it. Nothing extra downloads on the "later" path — the prefetch's own first pass is what advances the store — and the moment you declined is stamped on the state mount, so a postponement is legible to the session it postponed rather than lost.

Five cases never reach the question, because the answer is already settled:

| Case | What happens |
|---|---|
| The image is missing from your store entirely | Pulled synchronously, no question — there is no session to start otherwise. |
| `pull: always` | Pulls. A policy that already said yes on every shell cannot coherently be asked again. |
| `pull: never` | Neither probes nor asks — not talking to the registry is that policy's whole promise. |
| No tty (a script, a pipeline, CI) | Neither asks nor probes. The default inverts: start now, fetch behind. The interactive default is justified by the work that follows the wait; a script has no work that follows, so the same wait is pure latency times every invocation. |
| The container already exists (you are attaching a second terminal, or restarting a stopped container) | Nothing is asked. Docker cannot swap a running container's image, so a download offered here is one this session could not adopt; the prefetch fetches it behind you and the banner then offers `toolbox-reload`. |

Knowing whether to ask is itself a registry round-trip, so the question is answered from the [prefetch's shared probe cache](session-reload.md#cache-and-ttl) whenever its stamp is still warm — a sibling session that probed a moment ago has already established the fact. Only a cold stamp probes, and the probe is a `DistributionInspect`: metadata, not a download. Rationale and the options weighed: [ADR 0005](adr/0005-prompted-image-refresh-on-shell-start.md).


### Local overlay Dockerfile

To layer your own tools onto the standard image without a repo change or a per-shell `init.d/` script, drop a `Dockerfile` at `~/.toolbox/Dockerfile` (retargeted with [`mounts_root`](mounts.md#mounts_root-retarget) / a profile root, same as every `~/.toolbox/`-managed path). Its presence is the sole opt-in — **global only**, no per-repo or config-key activation. When present, `toolbox shell` builds a derived image tagged `ghcr.io/filippolmt/toolbox:local` on top of the resolved base and runs from it; when absent, the shell runs from the base image unchanged.

**Append-only, RUN-only contract.** The overlay is a bare fragment — it MUST NOT contain a `FROM` line and builds with an **empty context** (no `COPY`/`ADD` from host files, so nothing under `~/.toolbox/` is ever tarred into the build). Toolbox injects `FROM <resolved base image ID>` ahead of your fragment, so entrypoint, `init.d/`, and host-UID mapping are inherited unchanged. Use it for `RUN sudo apt-get install …` / `RUN pip install …`-style additions:

```dockerfile
# ~/.toolbox/Dockerfile — no FROM line; RUN only.
RUN sudo apt-get update && sudo apt-get install -y --no-install-recommends \
      httpie \
    && sudo rm -rf /var/lib/apt/lists/*
```

**Rebuild triggers.** A marker (base image ID + `sha256` of the Dockerfile bytes) is stored under the toolbox state dir (`~/.toolbox/toolbox/state/local-overlay.marker`, mounts_root-aware — alongside the image-pull cache, so toolbox-managed state stays out of your config dir). The build is skipped when the marker matches **and** `:local` is present locally; otherwise it rebuilds. So a rebuild happens when you edit the Dockerfile, when the shell-start refresh updates the base (its image ID changes), or when the `:local` image is missing. Base freshness stays governed by [`pull`](#image-selection); `:local` carries pull policy `never`, so `Refresh`/`Ensure` never reach a registry for it. The first build streams its output and is unavoidably slower; later shells skip via the marker.

**Fail-loud.** A failing overlay build (e.g. a broken `RUN`) aborts the shell and surfaces the build log — Toolbox never silently falls back to the base image.

**Next fresh container.** A rebuilt `:local` takes effect for the next freshly-created container under the existing [`AutoRemove`](internals/container-lifecycle.md#container-teardown) lifecycle. A running/stopped container is reused as-is and adopts the new image only when a fresh container is next created — `toolbox stop` (or exiting the shell) forces that. Rollback: delete `~/.toolbox/Dockerfile` (revert to base) or `docker rmi ghcr.io/filippolmt/toolbox:local`.

## `browser_bridge` (deprecated)

`browser_bridge:` is the pre-rename spelling of [`bridge:`](bridge.md). When `bridge:` is absent, the loader folds `browser_bridge` into it (`fillDefaultsBackstop` in `internal/config/config.go`); when both are set, `bridge:` wins. Migrate to `bridge:` — the alias survives only for config files written before the rename.

## `env:` passthrough

Arbitrary env vars injected into the in-container shell via an `env:` map in `.toolbox.yaml`. Motivating case: opt-in env-gated CLI features like `CLAUDE_CODE_WORKFLOWS=1` (Workflow tool), which previously needed a persisted `~/.zshrc` edit.

```yaml
env:                              # top-level — applies to every toolbox shell
  CLAUDE_CODE_WORKFLOWS: "1"
  CLAUDE_CODE_EFFORT_LEVEL: medium
  PROMPT_HIDE_KUBE: "1"           # hide the starship kubernetes segment

shells:
  infra:
    path: /tmp/infra
    env:                          # per-shell — overlays the top-level map
      AWS_PROFILE: prod
```

The `kubernetes` and `gcloud` prompt segments are opt-out via `PROMPT_HIDE_KUBE` / `PROMPT_HIDE_GCLOUD` (set to any value to hide). Details and why `terraform`/`docker_context` aren't env-toggleable: [prompt module toggles](internals/shell-start.md#prompt-module-toggles).

`GIT_CREDENTIAL_BRIDGE=0` disables the [bridge git credential helper](bridge.md#git-credentials) without uninstalling the bridge (default on when the bridge is installed). It is un-prefixed by necessity — the `TOOLBOX_` prefix is reserved (see below), so this is the supported way to gate it.

Contract:

- **Scope.** Resolved by the normal config load order (`--config` → walked-up `.toolbox.yaml` → `~/.toolbox.yaml` → `TOOLBOX_*` env → defaults), so the top-level `env:` is set globally (`~/.toolbox.yaml`) or per-project (`.toolbox.yaml`). The per-shell `shells.<name>.env` overlays it for that named shell only — per-shell keys win on collision (`config.Config.EffectiveEnv`, resolved inside `sessionplan.Plan` from the name you typed, matched case-insensitively and space-trimmed).
- **Emission order.** `sessionplan` emits the curated `TOOLBOX_*`/`PWD`/SDD entries first, then the loopback-bridge markers, then the self-identity pair (`TOOLBOX_CLI_VERSION`, `TOOLBOX_IMAGE_DIGEST`), then the host platform (`TOOLBOX_HOST_OS`, `TOOLBOX_HOST_ARCH`), then the managed-statusline opt-out marker, then the proximo entries, then the [reload marker](../CONTEXT.md#reload-marker) path (`TOOLBOX_RELOAD_MARKER`, omitted when the session mounts no state directory), and last the user env sorted by key (`sessionplan.userEnv`, deterministic for tests).
- **Reserved keys.** `config.ValidateEnv` rejects empty keys, keys containing `=`, and any key with the `TOOLBOX_` prefix or the literal `PWD` — those are owned by the curated contract. Same rules apply per-entry under `shells.<name>.env` (errors namespaced as `shells.<name>.env: …`). Empty *values* are allowed (`export VAR=`). Keys are injected verbatim: environment-variable names are case-sensitive, so `FOO` and `foo` are distinct vars (both the top-level and per-shell maps preserve the case you write).
- **Hash-neutral.** Lives outside the removed `tools:` block, like `sdd:` / `bridge:` — flipping a key never invalidates the image hash. Takes effect on the next container create (`toolbox stop` first to refresh an existing one).

## worktree

Tunes [`toolbox worktree`](commands.md#toolbox-worktree) sessions. A worktree is a checkout of git-tracked files only, so `create`/`open` seed a curated set of gitignored per-repo working state from the main repo into the new worktree (`.claude/settings.local.json`, `.env`/`.env.*`, `openspec/`, gsd's `.planning/`). `worktree.seed` adds extra repo-relative paths to that set:

```yaml
worktree:
  seed:
    - .secrets.local
    - config/local.yaml
```

Contract:

- **Gitignore-gated.** Every candidate — built-in defaults and `seed` entries alike — is copied **only if `git check-ignore` reports it ignored** in the main repo. A tracked path already arrives with the checkout; a non-ignored untracked path is left alone. So a `seed` entry that isn't gitignored is a silent no-op, and the built-in defaults self-correct in a repo that tracks one of them.
- **Additive.** `seed` is unioned with the built-in defaults (not a replacement); directories are copied recursively. Copies never clobber a worktree-local edit (an existing destination is kept).
- **Validation.** `config.ValidateWorktreeSeed` rejects absolute paths, entries containing `..`, and empty strings — the paths drive filesystem reads under the repo root and writes under the worktree, so traversal must not escape either tree.

## `TOOLBOX_*` environment variables

Viper is configured with `SetEnvPrefix("TOOLBOX")` + `AutomaticEnv`, with the image-selection keys explicitly seeded so the override always resolves:

| Variable | Overrides |
|----------|-----------|
| `TOOLBOX_IMAGE` | `image` |
| `TOOLBOX_REGISTRY_MIRROR` | `registry_mirror` |
| `TOOLBOX_PULL` | `pull` |
| `TOOLBOX_BRIDGE` | `bridge` |

For these four keys the `TOOLBOX_*` env var sits at the top of the [loading order](#loading-order): it overrides every file layer (`--config`, project, global), and only the built-in default is below it.
