# Commands

Reference for every `toolbox` command and subcommand. Source of truth: the cobra help text in `cmd/*.go` (`toolbox <cmd> --help`); this file adds the semantics the one-line help can't carry.

## Global flag: `--config`

Every command accepts `--config <file>` to bypass the normal config discovery and load exactly one file (highest precedence in the [loading order](configuration.md#loading-order)).

## toolbox shell

Start an interactive shell session in the toolbox container. With no argument it targets the current working directory; `toolbox shell <name>` opens a [named shell](shells.md), `toolbox shell <abs-dir>` opens a one-shot session on that directory.

| Flag | Description |
|------|-------------|
| `-p, --publish <spec>` | Publish a host port (repeatable, docker-style syntax — see below). |
| `-B, --bridge-loopback` | Forward published ports to container loopback (see below). |
| `--oauth <tool>` | Expand a known tool's OAuth port recipe (repeatable: `cf`, `codex`, `glab`, `oci`, `sonar`, `wrangler`). |
| `--create` | Auto-bootstrap a missing named shell in `~/.toolbox.yaml`. |
| `--path <dir>` | Directory to use with `--create` (default `$HOME/toolbox-shells/<name>`). |

### Publishing ports

By default the container has no host port bindings — it talks to the outside world through bind-mounted sockets, not TCP. When a tool inside the container needs to receive a connection from the host (typical case: an OAuth callback from `glab`, `gh`, or `gcloud` that listens on `http://localhost:<port>`), pass `--publish`/`-p` to `toolbox shell`:

```bash
# Forward host port 7171 to the same port in the container.
toolbox shell -p 7171

# Repeatable, full docker-style syntax supported.
toolbox shell -p 3000:3000 -p 127.0.0.1:9229:9229 -p 5353/udp
```

Accepted formats mirror `docker run -p` (`<port>`, `<host>:<container>`, `<ip>:<host>:<container>`, optional `/tcp|/udp` suffix). When the host IP is omitted it defaults to `127.0.0.1` — the port is reachable from your machine only, not the LAN.

**Port bindings are fixed at container creation.** `toolbox shell -p <port>` only takes effect when the container is first created — `ContainerCreate` writes the port set, and Docker doesn't accept post-hoc port changes. Run `toolbox stop` before re-invoking `toolbox shell -p …` to add or change bindings. Mismatch detection lives in `sessionplan.MissingPublishPorts`. When the listener inside the container binds `127.0.0.1` rather than `0.0.0.0`, see the [loopback bridge](#loopback-bridge).

### Loopback bridge

In-container CLIs that bind their OAuth callback to `127.0.0.1:<port>` (codex, vanilla wrangler) are unreachable from the host browser even with `toolbox shell -p <port>:<port>` — the host browser hits `ERR_EMPTY_RESPONSE` and no token is written. Docker's port forward delivers packets to the container's `eth0` interface; a listener bound to container loopback never sees them. The `-B` / `--bridge-loopback` flag fixes that:

```
host browser  ──  Docker -p  ──▶  eth0:<port>  ──[ socat ]──▶  127.0.0.1:<port>  ──▶  CLI
                                  ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
                                   bridged inside the container by init.d/70
```

When `-B` is set together with at least one `-p`, `sessionplan.Plan` emits `TOOLBOX_LOOPBACK_BRIDGE_PORTS=<comma-joined>` and `internal/build/assets/init.d/70-loopback-bridge.sh` spawns one `socat TCP-LISTEN:<port>,bind=$ETH_IP,fork,reuseaddr TCP:127.0.0.1:<port>` per port. The bridge binds the container's external interface IP (`hostname -i`) explicitly so it can coexist with the CLI's own `127.0.0.1:<port>` listener. It does **not** coexist with a wildcard listener: once socat holds `eth0:<port>`, a later `0.0.0.0:<port>` bind on the same port fails `EADDRINUSE` (Linux refuses wildcard over a specific bind regardless of `SO_REUSEADDR` — verified live with oci). Wildcard-binding CLIs must therefore not use `-B` on their port; the plain `-p` forward already reaches them.

#### `--oauth` presets

Standard recipes (static-port loopback CLIs). The `--oauth <tool>` preset flag on `toolbox shell` expands a known tool name to its documented recipe (`sessionplan.ExpandOAuth`, map in `internal/sessionplan/oauth.go`) — expansion only ever adds to explicit `-p`/`-B` flags, and an unknown tool errors before container creation listing the supported set:

```
toolbox shell --oauth codex      # = -B -p 1455:1455     codex ChatGPT-OAuth login
toolbox shell --oauth wrangler   # = -B -p 8976:8976     wrangler login
toolbox shell --oauth sonar      # = -B -p 64120-64130:64120-64130   sonar auth login
```

The explicit spelling stays available for ports not in the preset map:

```bash
toolbox shell -B -p 9999:9999
some-cli auth login ...   # CLI binds 127.0.0.1:9999
```

A static **range** also works (`sonar`): the CLI binds `127.0.0.1` on the first free port in 64120-64130 (SonarLint Core's EmbeddedServer range — SonarQube rejects callback ports outside it, so a random OS-assigned port is not an option upstream). `nat.ParsePortSpec` expands the range and init.d/70 spawns one socat per port; the bridge binds eth0, so it never steals the loopback port the CLI itself wants.

**Wildcard-bind carve-out.** `oci session authenticate` binds `0.0.0.0:8181` (`cli_setup_bootstrap.py`: `server_address = ('', 8181)`), so Docker's plain port-forward reaches it directly and the bridge is not only unnecessary but harmful — socat on `eth0:8181` makes oci's wildcard bind fail with `Could not complete bootstrap process because port 8181 is already in use`. `glab auth login` is the same style: it binds `0.0.0.0:7171`, so plain `-p 7171:7171` completes the callback and `-B` would break it (`EADDRINUSE` on `eth0:7171`):

```
toolbox shell --oauth oci    # = -p 8181:8181 (no -B — oci binds 0.0.0.0)
toolbox shell --oauth glab   # = -p 7171:7171 (no -B — glab binds 0.0.0.0)
```

`cf` is a loopback-bind range too: it picks its callback port from `startPort: 8877, maxPortAttempts: 10`, binds `127.0.0.1`, and advertises a `localhost` redirect_uri — the same shape as `sonar`, so the whole range is published and bridged. It carries **no** dist patch: cf 0.1.0 binds loopback natively, and the historical build-time `sed` that rewrote the callback host `localhost` → `0.0.0.0` was dropped — post-0.1.0 that literal **is** the redirect_uri, so the rewrite made Cloudflare reject the `0.0.0.0` callback as an unregistered redirect url. `cf login` recipe:

```
toolbox shell --oauth cf   # = -B -p 8877-8886:8877-8886 (loopback-bind range, range syntax via nat.ParsePortSpec)
```

**Dynamic-port CLIs with no fixed range** (`gcloud`, `gws`, `tofu`) cannot be pre-bound — the bridge needs a known container port to forward, so they get no `--oauth` recipe (use a wrapper / device-code flow).

OAuth CLI survey:

| CLI | Listener style | Strategy |
|---|---|---|
| `oci` | `0.0.0.0:8181` (static, wildcard) | plain publish: `--oauth oci` = `-p 8181:8181` (no `-B` — socat would collide with the wildcard bind) |
| `glab` | `0.0.0.0:7171` (static, wildcard) | plain publish: `--oauth glab` = `-p 7171:7171` (no `-B` — socat would collide with the wildcard bind) |
| `wrangler` | `localhost:8976` (static) | bridge: `--oauth wrangler` = `-B -p 8976:8976` (vanilla wrangler, no sed) |
| `codex` | `localhost:1455` (static, default ChatGPT-OAuth flow) | bridge: `--oauth codex` = `-B -p 1455:1455`; device-code (`codex login --device-auth`) exists but is an opt-in beta, not the default |
| `sonar` | `127.0.0.1:64120-64130` (static range, first free; server rejects out-of-range ports) | bridge: `--oauth sonar` = `-B -p 64120-64130:64120-64130`; token persisted via `SONARQUBE_CLI_KEYCHAIN_FILE` (libsecret absent in container) |
| `cf` | `127.0.0.1:8877-8886` (range, first free; localhost redirect_uri) | bridge: `--oauth cf` = `-B -p 8877-8886:8877-8886` (no sed — binds loopback natively post-0.1.0) |
| `gcloud` | `localhost:8085+` (dynamic) | wrapper / device-code |
| `gws` | `127.0.0.1:0` (ephemeral) | wrapper / device-code |
| `tofu` | random port ≥1024, range from server discovery document (dynamic) | not pre-bindable; device-code/wrapper-style — no bridge recipe |
| `az` | dynamic | device-code (`--use-device-code`) |
| `gh` / `claude` | none — device-code | no listener; no port forward needed |

Limitations:

- Bridge env is fixed at `ContainerCreate`. `toolbox shell -B …` on a container created without `-B` is a no-op — same UX as `-p`. Run `toolbox stop` first.
- IPv4 only. Docker port-forward IPv6 support is patchy; not in scope. The bridge socat forwards to `127.0.0.1:<port>` (IPv4). Node CLIs that `listen('localhost')` would bind `::1` (IPv6) on Node 17+ and never receive the callback (browser shows `ERR_EMPTY_RESPONSE`, the CLI times out), so the image sets `ENV NODE_OPTIONS=--dns-result-order=ipv4first` to force `localhost` to resolve IPv4-first — cf, wrangler and codex all rely on this.
- `-B` without `-p` is not an error. The init.d script logs a one-line `loopback bridge: enabled but no -p ports published — skipping` warning so the misconfiguration is visible.
- `-B` bridges **every** published port (`TOOLBOX_LOOPBACK_BRIDGE_PORTS` enumerates the full publish set, init.d/70 spawns socat per port). Combining a bridged preset with a wildcard-bind one (e.g. `--oauth wrangler --oauth oci`) therefore puts socat on `eth0:8181` too and breaks oci's wildcard bind — same for `glab` (socat on `eth0:7171` would break its `0.0.0.0:7171` bind). `cf` is itself bridged now (loopback-bind range, like `wrangler`/`sonar`), so it composes safely with other bridged presets but conflicts with the wildcard-bind ones — authenticate `cf` alongside `oci`/`glab` in separate sessions.
- Per-port failure (e.g. `EADDRINUSE` because another in-container process already binds `eth0:<port>`) is logged to `~/.toolbox-state/init/70-loopback-bridge.log` and the loop continues with the remaining ports. The bridge never aborts boot.
- `socat` is part of the always-on base apt-install set (~350KB) — no per-tool opt-out. The bridge feature is system-level, not a catalog tool.

See also: [publishing ports](#publishing-ports), [image build internals](internals/image-build.md) (socat ships in the always-on base apt layer), [bridge](bridge.md) (inverse direction — container→host browser opens).

## toolbox worktree

Map one branch to one git worktree to one path-scoped container running an AI agent (alias `wt`). Each worktree lives under `<repo-root>/.worktrees/tbx-<branch>`; because toolbox derives a [deterministic container per absolute path](#toolbox-shell), several worktrees run as several isolated dev environments at once. The git branch name stays unprefixed (only the directory carries `tbx-`), so pull requests are unaffected.

| Subcommand | Description |
|------------|-------------|
| `create <branch> [--agent] [--from <base>] [--no-fetch] [-- <task>]` | Fetch the base, add a worktree branched from `origin/<base>`, launch the container, auto-start the agent — optionally already working on `<task>`. |
| `open <branch> [--agent]` (alias `attach`) | Re-attach to an existing toolbox worktree — recreates the [auto-removed](internals/container-lifecycle.md#container-teardown) container scoped to the same path and relaunches the agent. |
| `list` (alias `ls`) | Print toolbox worktrees only (branch · container status · path); worktrees an agent creates outside the `tbx-` convention are excluded. |
| `rm <branch> [--force] [--delete-remote]` | Best-effort stop the worktree's container, `git worktree remove [--force]`, then delete the now-orphaned local branch (safe `-d`; `--force` also escalates the branch to `-D`). `--delete-remote` additionally deletes it on `origin`. |
| `prune [--dry-run] [--delete-remote]` | Fetch the base and remove toolbox worktrees whose branch is merged into `origin/<base>`, deleting each merged branch too; `--delete-remote` also deletes them on `origin`. `--dry-run` lists the worktree and branch (and remote) deletions without performing them. |
| `sync [<branch>] [--no-fetch] [--no-push]` (alias `rebase`) | Rebase a worktree branch onto its recorded base and push with `--force-with-lease`; stops safely on conflict. |

The base branch for `create` is `--from` when given, else the repository default branch (`origin/HEAD`). `create` always branches from the freshly fetched `origin/<base>` so the new branch is remote-aligned by construction; `--no-fetch` branches from the local base ref for offline work. The branch is created `--no-track` (so it does not adopt the base as its upstream and read as "ahead of origin/<base>"), and `create` sets `push.autoSetupRemote=true` once when unset so the first `git push` from the worktree creates the branch's own upstream instead of erroring. The base is recorded in `branch.<branch>.base` for `prune` to reuse. The agent is resolved with precedence `--agent` flag > the [`agent`](configuration.md#agent) config key > the default `claude`, restricted to `claude` or `codex`. When the agent exits, the session drops to an interactive shell in the worktree rather than tearing down.

Anything after a `--` separator on `create` is passed to the agent as its initial task prompt, so the worktree spins up already working — `toolbox worktree create feat-auth -- 'add an auth module'`. This makes several issues fire-and-forget: kick off one worktree per task without waiting to type the prompt in each. The prompt is shell-quoted, so metacharacters in the task (`$(...)`, backticks, `;`) reach the agent literally and cannot inject commands into the session wrapper. With no `--`, the agent launches bare (unchanged). Only `create` takes a prompt; `open` re-attaches without one.

Git resolves inside the container because the session mounts both the worktree (at its own absolute host path, via the workspace mirror) and the main repo's `<repo-root>/.git` at the same absolute path the worktree's gitdir pointer references — so `status`/`commit`/`push` work unchanged.

A worktree is a clean checkout of git-tracked files, so per-repo state that is gitignored does not reach it. `create`/`open` seed a curated set of that state from the main repo into the worktree — `.claude/settings.local.json` (the permission allowlist, so the agent doesn't re-prompt in every worktree), `.env` and `.env.*` dotenv files, the `openspec/` working tree, and gsd's `.planning/` — copying each path **only when git actually ignores it** (a tracked path already arrives with the checkout; a non-ignored untracked path is left alone) and never clobbering a worktree-local edit. Add more paths with the [`worktree.seed`](configuration.md#worktree) config key; they pass through the same `git check-ignore` gate. Knowledge-graph indexes are deliberately left out of the seed set: `graphify-out/` and the codegraph database are per-branch, so sharing them would make one branch read another's graph — regenerate them inside the worktree (`graphify update .`; the codegraph watcher rebuilds from its tracked config).

`prune` tests each worktree against the base recorded at create (`branch.<branch>.base`, the default branch for worktrees made before bases were tracked), fetching each distinct base once. Detection is local merge ancestry (`git branch --merged origin/<base>`), so squash-merged branches (no merge commit) are not detected — use `rm <branch>` for those. It removes without `--force`, so a worktree with uncommitted or untracked changes is preserved (git refuses) even if its branch reads as merged.

Because `git worktree remove` never deletes the branch itself, `rm` and `prune` complete the cleanup by deleting the now-orphaned local branch. The delete is **safe by default** (`git branch -d`, which refuses a branch not merged into its upstream/HEAD): `prune` only ever targets merged branches, so it always succeeds, while `rm` on an unmerged branch warns and leaves the branch intact unless `--force` is given — the same flag that forced the worktree removal then escalates the branch to `git branch -D`, so one flag means "I accept losing unmerged work". `--delete-remote` (off by default on both, since touching the remote is the most destructive step) additionally runs `git push origin --delete <branch>`. Branch and remote deletion are best-effort: a failure warns but does not fail the command (the worktree removal has already succeeded) and does not abort `prune`'s sweep of the remaining worktrees.

`sync` (alias `rebase`) encodes the "rebase before PR" step: it reuses the base recorded at create (`branch.<branch>.base`, falling back to the default branch), fetches it, rebases the branch onto `origin/<base>`, and pushes with `--force-with-lease` — never plain `--force`, so it refuses to clobber remote commits the local ref hasn't seen. With no `<branch>` it acts on the worktree it is invoked from — only a toolbox worktree, never the primary checkout, so an accidental `sync` on `main` cannot force-push the default branch (it errors instead); given a branch it targets that toolbox worktree. `--no-push` rebases without pushing; `--no-fetch` skips the fetch and rebases onto the `origin/<base>` ref as last fetched, for offline work. It always rebases onto the remote-tracking ref, not a bare local `<base>` branch, because a `create`d worktree is branched `--no-track` and has no local base branch. It is pure host-side git — no container. On a rebase **conflict** it stops with the rebase in progress, never auto-resolves and never pushes, and prints how to `git rebase --continue` or `--abort`. There is no dirty-worktree pre-check: git itself refuses to rebase with uncommitted changes (or a missing ref), and that error is surfaced as-is rather than dressed up as a conflict.

`create`/`open` resolve a worktree by its exact git branch, and `open` fails if the worktree directory was deleted by hand (run `prune` to clear the stale registration). Like every toolbox container, mounts are fixed at creation: to change a running worktree session's mounts, `rm` and recreate it.

## toolbox stop

Stop and remove toolbox containers. Mirrors `toolbox shell`'s targeting: no argument stops the container bound to the current directory; `toolbox stop <name>` stops a [named shell](shells.md)'s container; `toolbox stop <abs-dir>` stops the one-shot session for that directory.

| Flag | Description |
|------|-------------|
| `--all` | Stop every toolbox container on the host (cannot be combined with a positional argument). |

## toolbox build

Build the toolbox Docker image from the embedded context (Dockerfile + assets are compiled into the binary, so it runs anywhere). The build overwrites the local cache of the canonical tag, so the next `toolbox shell` picks it up — see [image selection](configuration.md#image-selection) for how it interacts with `image` / `registry_mirror` / `pull` and how to restore the upstream copy.

| Flag | Description |
|------|-------------|
| `--no-cache` | Disable the Docker build cache. |

## toolbox version

Print the toolbox version.

## toolbox init

Write an annotated `.toolbox.yaml` (same content as `toolbox config example`) in the current directory, mode 0600. Refuses to overwrite an existing file unless `--force` is given.

## toolbox config

Inspect and scaffold `.toolbox.yaml`. Key semantics live in [configuration](configuration.md).

| Subcommand | Description |
|------------|-------------|
| `show [--origin]` | Print the fully-resolved configuration. |
| `example` | Print an annotated `.toolbox.yaml` template (generated from the live catalog and default mount set). |
| `path` | Show the config layers in precedence order with found/none markers. |
| `edit` | Open the highest-precedence config file in `$EDITOR`. |
| `set [--image] [--registry-mirror] [--pull] [--where]` | Set the image-selection keys (empty value resets the key). |
| `doctor` | Validate the configuration without modifying it. |

### config provenance & doctor

`toolbox config show --origin` annotates each rendered key git-config-style with the layer that set it: `(default)`, `(~/.toolbox.yaml)`, `(./.toolbox.yaml)`, or `(--config <path>)`. Provenance is computed by re-running the pure `config.Merge` once per layer (defaults / +global / +project) and crediting each key to the highest layer whose value differs from the layer below (`configedit.Compute`); `--config` short-circuits to a defaults-vs-explicit diff, matching `Plan`. Granularity: per entry name for `shells`/`mounts`, container-level for everything else. The extra viper passes only run for `--origin`/`doctor`, never on the `toolbox shell` hot path. **Without the flag, `config show` output is byte-for-byte unchanged** (golden-tested) — users pipe it.

`toolbox config doctor` is a strict read-only superset of the load path's validation tail (`configedit.Doctor`): it surfaces `Plan`/`Merge` errors, flags unknown top-level keys per layer (key set derived from `Config`'s mapstructure tags, with Levenshtein suggestions), warns on the legacy `tools:` block (→ [tools-removal](internals/image-build.md#tools-removal)), checks `shells.<name>.path` (empty = error, missing dir = warning — it may be created by `--create`), and runs `mountplan.Merge` (failure = error, duplicate resolved targets = warning; mountplan doesn't dedupe, the last bind wins silently at the Docker layer). Exit 1 iff at least one error-severity finding.

`config path` lists the layers in precedence order with found/none markers; `config edit` opens `$EDITOR` (fallback `vi`) on the highest-precedence existing file — explicit `--config` > walked-up project > global — creating `~/.toolbox.yaml` with the documentation header when none exists. Both reuse the `config.LoadLayers` / `config.WalkUpProjectConfig` exports extracted from `Plan` (a pure refactor: `Plan ≡ Merge(LoadLayers(...))`, regression-tested).

## toolbox mounts

Edit the `mounts:` list / `mounts_root:` key without hand-editing YAML. Subcommands: `list [--defaults-only]`, `add <name> --source --target [--readonly]`, `disable <name>`, `remove <name>`, `root <path>` — all accept [`--where`](#--where-targeting). Full semantics: [mounts](mounts.md#mounts-cli).

## toolbox shells

Manage named shell shortcuts (the `shells:` block). Subcommands: `list`, `get <name>`, `add <name> --path [--create-dir] [--env K=V]`, `set <name> --env K=V`, `remove <name> [--purge-dir]` — all accept [`--where`](#--where-targeting). Full semantics: [shells](shells.md).

## toolbox bridge

Manage the host-side bridge daemon (browser / editor / proximo forwarder). Subcommands: `install`, `uninstall`, `status` (plus the hidden `daemon` the supervisor runs). `browser-bridge` survives as a deprecated alias. Full semantics: [bridge](bridge.md).

## toolbox sdd

Manage repo-local Spec-Driven-Development integrations. Subcommands: `list`, `init <name>`. Full semantics: [sdd](sdd.md).

## toolbox completion

Generate shell completion scripts: `toolbox completion [bash|zsh|fish]`.

## --where targeting

Every config-writing subcommand (`toolbox shells add|set|remove`, `toolbox mounts add|disable|remove|root`, `toolbox config set`) accepts `--where global|local` (default `global`), mirroring the per-user/per-repo duality of the load order:

- `global` → `configio.GlobalConfigPath()` (`~/.toolbox.yaml`). Default because shells/mounts are naturally per-user.
- `local` → the **walked-up** project `.toolbox.yaml` (`config.WalkUpProjectConfig`, the same walk-up `Plan` uses), so the existing project file is patched in place rather than shadowed by a stray `./.toolbox.yaml`. Only when the walk-up finds nothing is `./.toolbox.yaml` created in the CWD.

Resolution lives in `configedit.Resolve` (`internal/configedit/where.go`). Every writer reports the touched file as `<path>: created|updated|unchanged` (idempotent re-runs render byte-identically and report `unchanged`). Files created by a writer start with a header comment pointing at `toolbox config example` (`configedit.Upsert`, D7: the header policy lives in `configedit`, keeping `configio` a policy-free leaf).
