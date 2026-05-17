# Runtime notes

Deep gotchas pulled out of `CLAUDE.md` to keep the AI-loaded context lean. Reference these from a top-level summary in `CLAUDE.md` instead of inlining.

## Image build

### Host UID mapping

The CLI runs the container with `--user $(id -u):$(id -g)`. Because the runtime UID rarely matches the baked `toolbox` user (UID 1000), `/home/toolbox` is made world-writable in the image. Don't revert to a fixed UID without understanding why — host file ownership would invert and writes inside `~/.toolbox/` would fail for anyone whose host UID isn't 1000.

### Docker CLI checksum

Layer 7 of `internal/build/assets/Dockerfile` installs the static Docker CLI binary without a SHA256 verification step because Docker doesn't publish `.sha256` files for those releases. Version pin + HTTPS is the only guard. Tracked as accepted risk T-01-08.

### Tool version pinning + `ARG INSTALL_<TOOL>` pattern

Every external binary in the Dockerfile is pinned by version + SHA256 (exceptions: Docker CLI — see above — and gcloud, which uses a Google APT repo). Renovate bumps them. Adding a new tool: download tarball, verify with `sha256sum`, install. Wire opt-out via `ARG INSTALL_<TOOL>=true` (Dockerfile) coupled to the matching `tools.<key>` entry in `.toolbox.yaml` and a row in `internal/catalog/catalog.go` `Entries` (the `BuildArg` field is the literal `INSTALL_<TOOL>`).

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

### Catalog entry → image hash

Adding (or removing) an entry in `internal/catalog/catalog.go` `Entries` invalidates the local image hash for every user with a non-default `tools:` config — the canonical hash encoding is computed over the catalog's `(Key, Default, BuildArg)` tuples, so a new entry shifts the digest even if the user never sets it. Practical effect: the next `toolbox shell` shows "Image not found locally — building toolbox:local-…" once and rebuilds. Document this in release notes when bumping the list. Users on canonical defaults are unaffected (they pull `:latest` from GHCR — `catalog.IsDefault` short-circuits before any hash compute).

## Container lifecycle

### Image selection

`toolbox shell` pulls `ghcr.io/filippolmt/toolbox:latest` only when the merged `tools:` config matches the catalog defaults (`catalog.IsDefault` returns true). Any override auto-builds `toolbox:local-<hash>` from the embedded Dockerfile via `internal/build/tag.go::ResolveImage`. The tag hash is computed by `catalog.WriteCanonical` over `(Key, Default, BuildArg)` tuples. `toolbox build` is the explicit escape hatch (supports `--no-cache`).

### Port bindings are fixed at container creation

`toolbox shell -p <port>` only takes effect when the container is first created — `ContainerCreate` writes the port set, and Docker doesn't accept post-hoc port changes. Run `toolbox stop` before re-invoking `toolbox shell -p …` to add or change bindings. Accepted formats mirror `docker run -p`; host IP defaults to `127.0.0.1` when omitted. Mismatch detection lives in `sessionplan.MissingPublishPorts`.

### Codex nested sandbox

When `tools.codex` is enabled (default), `toolbox shell` creates the container with Docker `seccomp=unconfined` so Codex's built-in bubblewrap sandbox can create nested user namespaces. With `tools.codex: false` the container keeps Docker's default seccomp profile. The flag flip lives in `sessionplan.NestedSandboxSecurityOpt`.

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
- Skills whose upstream installer emits user-authored content (bmad, openspec) leave `GitignoreEntries` nil; the fence is skipped entirely. The host reports `skipped (skill produces user-authored content)`.
- Disabling a skill (removing the `.toolbox.yaml` flag) leaves the orphaned fence block in `.gitignore`. There is no `toolbox sdd uninstall` today — clean it up manually.

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
