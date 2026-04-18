# Toolbox

A containerized development environment (Debian slim) bundling all the tools you need for daily development — Claude Code with GSD/skills/plugins, Git, cloud CLIs, Node.js and Python runtimes, Playwright, Docker. Controlled by a Go CLI (`toolbox`) that manages the container lifecycle with a single command.

## What's inside

| Tool | Version |
|------|---------|
| Node.js | 22 LTS |
| Claude Code | latest |
| Python 3 | 3.11 |
| uv | 0.11.x |
| kubectl | 1.35.x |
| Helm | 4.1.x |
| OpenTofu | 1.11.x |
| GitHub CLI (gh) | 2.90.x |
| GitLab CLI (glab) | 1.92.x |
| Docker CLI | 29.4.x |
| jq, yq, starship, git | latest stable |

## Install

### macOS (Homebrew)

```bash
brew tap filippolmt/tap
brew install --cask toolbox
```

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

Create `~/.toolbox.yaml` to customize the image and mounts:

```yaml
image:
  name: ghcr.io/filippolmt/toolbox
  tag: latest

mounts:
  - source: ~/.claude
    target: /home/toolbox/.claude
    readonly: false
  - source: ~/.gitconfig
    target: /home/toolbox/.gitconfig
    readonly: true
  - source: ~/.ssh
    target: /home/toolbox/.ssh
    readonly: true
  - source: /var/run/docker.sock
    target: /var/run/docker.sock
    readonly: false
```

Configuration is loaded from (in order of priority):

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
