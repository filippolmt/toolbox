# Toolbox

A containerized development environment (Debian slim) bundling all the tools you need for daily development — Claude Code with GSD/skills/plugins, Git, cloud CLIs, Node.js and Python runtimes, Playwright, Docker. Controlled by a Go CLI (`toolbox`) that manages the container lifecycle with a single command.

## What's inside

| Tool | Version |
|------|---------|
| Node.js | 24 LTS |
| pnpm | 10.33.x |
| bun (JavaScript runtime + package manager + bundler) | 1.3.x |
| Claude Code | 2.1.x |
| OpenAI Codex CLI | 0.125.x |
| Pyright language server (`pyright-langserver`) | 1.1.x |
| rtk (LLM token-saving CLI proxy) | 0.37.x |
| Playwright CLI | 1.59.x |
| Playwright CLI with SKILLS (`playwright-cli`) | 0.1.x |
| Python 3 | 3.11 |
| uv | 0.11.x |
| Go toolchain + gopls + goimports | 1.26.x |
| kubectl | 1.36.x |
| kubectx + kubens (kubectl context/namespace switchers) | 0.11.x |
| Helm | 4.1.x |
| OpenTofu | 1.11.x |
| GitHub CLI (gh) | 2.91.x |
| GitLab CLI (glab) | 1.93.x |
| Google Workspace CLI (`gws`) | 0.22.x |
| Docker CLI | 29.4.x |
| Docker Compose | 5.1.x |
| Google Cloud SDK (gcloud) | 565.x |
| Azure CLI (az) | 2.85.x |
| Oracle OCI CLI | 3.80.x |
| Cloudflare CLI (`cf`, Wrangler vNext preview) | 0.0.x |
| Zsh shell bundle (Oh-My-Zsh + fzf + zoxide) | bundled |
| jq, yq, starship, bat, git | latest stable |

Every optional tool above can be disabled per-project — see [Configuration](#configuration).

## Install

### macOS (Homebrew)

```bash
brew install filippolmt/tap/toolbox
```

> Migrating from an older install that used `brew install --cask toolbox`? The cask has been replaced by a formula — run `brew uninstall --cask --force toolbox && brew link --overwrite toolbox` once, then regular `brew upgrade` will keep the CLI symlink in place.

### From source

Requires Go 1.26+:

```bash
go install github.com/filippolmt/toolbox@latest
```

### Verify installation

```bash
toolbox version
```

## Prerequisites

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) must be installed and running

## Quick start

```bash
# Pull the toolbox image from GHCR
docker pull ghcr.io/filippolmt/toolbox:latest

# Enter the toolbox
toolbox shell
```

On first run, `toolbox shell` creates and starts the container with default volume mounts. If the container already exists, it reattaches to it.

## Configuration

Two optional knobs: `tools:` (which CLIs to bake into the image) and `mounts:` (which host paths to expose inside the container). Both live in `~/.toolbox.yaml` (global) or `.toolbox.yaml` in the project directory (overrides).

### Opting out of tools

Every tool in the table above is enabled by default. Disable any of them per-project:

```yaml
tools:
  gcloud: false
  azure: false
  oci: false
  playwright: false
```

When the `tools:` map matches the defaults, `toolbox shell` pulls the prebuilt `ghcr.io/filippolmt/toolbox:latest` image. Any opt-out triggers a local rebuild tagged `toolbox:local-<hash>` — the hash is derived from the selected tool set, so the same config always resolves to the same image.

Because Codex is enabled by default, newly-created toolbox containers run with Docker `seccomp=unconfined` so Codex's built-in bubblewrap sandbox can create nested user namespaces. Set `tools.codex: false` if you want to keep Docker's default seccomp profile and do not need Codex inside toolbox.

### Overriding mounts

The defaults isolate every credential path under `~/.toolbox/` on the host, so the container never sees the real `~/.ssh`, `~/.gitconfig`, `~/.claude`, etc. directly. The `mounts:` list is merged on top of the defaults — you only declare what changes.

Each user entry is interpreted by `name`:

| Form | Behavior |
|------|----------|
| `name` matches a default, `target` omitted | **Patch**: only the fields you set override the default (typically `source`). |
| `name` matches a default, `target` set | **Replace**: the entire default entry is swapped for yours. |
| `name` not a default (or omitted) | **Append**: added after the default set. |
| `name` matches a default, `disabled: true` | **Remove**: the default is dropped from the resolved set. |

Default mount names: `claude`, `codex`, `state`, `ssh`, `gitconfig`, `gh`, `glab`, `gcloud`, `gws`, `azure`, `oci`, `kube`, `playwright-cache`, `startup.d`, `npm-global`, `go`, `docker-sock`. A patch referencing an unknown name fails at startup so typos surface immediately.

Examples:

```yaml
mounts:
  # Retarget the gws auth dir to a custom host path.
  - name: gws
    source: /Volumes/work/creds/gws

  # Drop the Docker socket bind for a project that shouldn't see it.
  - name: docker-sock
    disabled: true

  # Add an extra project-specific mount.
  - name: project-data
    source: /opt/data
    target: /data
    readonly: true
```

Bool fields in a *patch* can flip `false → true` but not `true → false` (mapstructure can't tell "not set" from `false`). For that case, use the replace form by also setting `target`.

#### Retargeting every default at once

When you want every toolbox-managed credential / state dir to live somewhere other than `~/.toolbox/` (encrypted volume, shared work drive, alternate user home), set `mounts_root` instead of patching each entry individually:

```yaml
# Move ~/.toolbox/.claude → /Volumes/work/toolbox/.claude,
# ~/.toolbox/gh → /Volumes/work/toolbox/gh, and so on for every default.
mounts_root: /Volumes/work/toolbox

mounts:
  # Per-mount patches still win — gws stays on a different drive.
  - name: gws
    source: /Volumes/secure/gws
```

`mounts_root` accepts absolute paths (`/Volumes/work/toolbox`) and home-relative paths (`~/work/toolbox-state`). Relative paths are rejected at startup. Mounts whose source already lives outside `~/.toolbox/` (e.g. `docker-sock` → `/var/run/docker.sock`, `ssh`/`gitconfig` symlink targets) are not touched — only the toolbox-managed mirrors are retargeted.

**Scope: global vs per-project.** `mounts_root` follows the same precedence as every other config field:

| Where you set it | Effect |
|------------------|--------|
| `~/.toolbox.yaml` only | Applied to every `toolbox shell`, in every project. |
| `./.toolbox.yaml` only | Applied only when `toolbox shell` runs in that project directory. |
| Both | Project file replaces the global value for that project; other projects keep the global one. (Scalar field — no concatenation.) |

`mounts_root` is applied first to retarget every default; per-name `mounts:` patches are then layered on top, so a single mount can still escape the global root in any one project. Other projects keep using the global root unchanged.

**Migration note.** `mounts_root` only changes where the container *binds* its state — it does not move data. If you already have credentials and history in `~/.toolbox/` and switch to a new root, the new directories are auto-created empty (per default `create_if_missing: true`) and the original `~/.toolbox/` data is left untouched. Move it yourself before the next `toolbox shell` if you want continuity:

```bash
# Stop any running toolbox, then carry your state over.
toolbox stop
mv ~/.toolbox /Volumes/work/toolbox
```

Bare `~` is rejected — it would rewrite `~/.toolbox/.claude` to `~/.claude`, exposing toolbox state on the real host home and defeating credential isolation. Use a sub-path (`~/toolbox-state`) or an absolute path.

Validation: any entry that sets `target` (replace or anonymous append) must also set a non-empty `source`. An empty source would silently bind the current working directory, so it's rejected at startup.

`source` accepts:
- absolute paths (`/Volumes/work/creds/gws`),
- home-relative paths (`~/credentials/github` — `~` expands to the host user's home),
- CWD-relative paths (`./test`, `../shared/data`, or plain `data`) — resolved against the directory you invoked `toolbox shell` from, which is normally the project root.

CWD resolution lets per-project `.toolbox.yaml` reference paths inside the project without hardcoding absolute prefixes:

```yaml
mounts:
  - name: project-data
    source: ./fixtures
    target: /workspace/fixtures
    create_if_missing: true
```

### Startup hooks

Drop any `*.sh` file into `~/.toolbox/startup.d/` on the host and it will be executed by the entrypoint on every `toolbox shell`, before your bash prompt. Use this for per-user bootstrap that should not live in the image (installing Claude Code skill packs, `direnv` shims, custom env bootstrap, etc.). Hooks run as the toolbox user, share the mounted credentials, and can write to the per-user npm prefix at `~/.toolbox/npm-global/` without needing root.

See [`examples/startup.d/`](examples/startup.d/) for a ready-to-copy example that installs and self-updates [Get-Shit-Done](https://github.com/gsd-build/get-shit-done).

### Publishing ports

By default the container has no host port bindings — it talks to the outside world through bind-mounted sockets, not TCP. When a tool inside the container needs to receive a connection from the host (typical case: an OAuth callback from `glab`, `gh`, or `gcloud` that listens on `http://localhost:<port>`), pass `--publish`/`-p` to `toolbox shell`:

```bash
# Forward host port 7171 to the same port in the container.
toolbox shell -p 7171

# Repeatable, full docker-style syntax supported.
toolbox shell -p 3000:3000 -p 127.0.0.1:9229:9229 -p 5353/udp
```

Accepted formats mirror `docker run -p` (`<port>`, `<host>:<container>`, `<ip>:<host>:<container>`, optional `/tcp|/udp` suffix). When the host IP is omitted it defaults to `127.0.0.1` — the port is reachable from your machine only, not the LAN.

Port bindings are fixed when the container is created. If a container already exists for the current workspace, run `toolbox stop` before `toolbox shell -p …` so the new container picks up the flag.

### Loading order

Configuration is loaded from (highest priority first):

1. `--config` flag
2. The nearest `.toolbox.yaml` walking up from the current working directory (search stops at `$HOME` or the filesystem root) — running `toolbox shell` from any subdirectory of a workspace still picks up that workspace's project config
3. `~/.toolbox.yaml` (global)
4. `TOOLBOX_*` environment variables
5. Built-in defaults

## CLI commands

| Command | Description |
|---------|-------------|
| `toolbox shell` | Start or attach to the toolbox container (use `-p <port>` to publish ports for OAuth callbacks / dev servers) |
| `toolbox stop` | Stop and remove the container |
| `toolbox build` | Build the Docker image locally |
| `toolbox version` | Show version info |
| `toolbox completion [bash\|zsh\|fish]` | Generate shell completions |

## Updating

```bash
brew upgrade toolbox
```

Or pull the latest image:

```bash
docker pull ghcr.io/filippolmt/toolbox:latest
```

## License

MIT
