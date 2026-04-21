# Toolbox

A containerized development environment (Debian slim) bundling all the tools you need for daily development — Claude Code with GSD/skills/plugins, Git, cloud CLIs, Node.js and Python runtimes, Playwright, Docker. Controlled by a Go CLI (`toolbox`) that manages the container lifecycle with a single command.

## What's inside

| Tool | Version |
|------|---------|
| Node.js | 24 LTS |
| pnpm | 10.33.x |
| Claude Code | 2.1.x |
| Playwright CLI | 1.59.x |
| Playwright CLI with SKILLS (`playwright-cli`) | 0.1.x |
| Python 3 | 3.11 |
| uv | 0.11.x |
| kubectl | 1.35.x |
| Helm | 4.1.x |
| OpenTofu | 1.11.x |
| GitHub CLI (gh) | 2.90.x |
| GitLab CLI (glab) | 1.92.x |
| Docker CLI | 29.4.x |
| Docker Compose | 5.1.x |
| Google Cloud SDK (gcloud) | 565.x |
| Azure CLI (az) | 2.85.x |
| Oracle OCI CLI | 3.80.x |
| jq, yq, starship, git | latest stable |

Every optional tool above can be disabled per-project — see [Configuration](#configuration).

## Install

### macOS (Homebrew)

```bash
brew install filippolmt/tap/toolbox
```

> Migrating from an older install that used `brew install --cask toolbox`? The cask has been replaced by a formula — run `brew uninstall --cask --force toolbox && brew link --overwrite toolbox` once, then regular `brew upgrade` will keep the CLI symlink in place.

### From source

Requires Go 1.22+:

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

### Overriding mounts

The defaults isolate every credential path under `~/.toolbox/` on the host, so the container never sees the real `~/.ssh`, `~/.gitconfig`, `~/.claude`, etc. directly. To add or replace a mount:

```yaml
mounts:
  - source: ~/.toolbox/.claude
    target: /home/toolbox/.claude
    readonly: false
    create_if_missing: true
  - source: ~/.toolbox/ssh
    target: /home/toolbox/.ssh
    readonly: true
    symlink_from: ~/.ssh
  - source: /var/run/docker.sock
    target: /var/run/docker.sock
    readonly: false
```

Declaring `mounts:` replaces the default set — copy the defaults from `internal/config/config.go` (`DefaultMounts`) before adding entries.

### Startup hooks

Drop any `*.sh` file into `~/.toolbox/startup.d/` on the host and it will be executed by the entrypoint on every `toolbox shell`, before your bash prompt. Use this for per-user bootstrap that should not live in the image (installing Claude Code skill packs, `direnv` shims, custom env bootstrap, etc.). Hooks run as the toolbox user, share the mounted credentials, and can write to the per-user npm prefix at `~/.toolbox/npm-global/` without needing root.

See [`examples/startup.d/`](examples/startup.d/) for a ready-to-copy example that installs and self-updates [Get-Shit-Done](https://github.com/gsd-build/get-shit-done).

### Loading order

Configuration is loaded from (highest priority first):

1. `--config` flag
2. `.toolbox.yaml` in the current directory
3. `~/.toolbox.yaml`
4. `TOOLBOX_*` environment variables
5. Built-in defaults

## CLI commands

| Command | Description |
|---------|-------------|
| `toolbox shell` | Start or attach to the toolbox container |
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
