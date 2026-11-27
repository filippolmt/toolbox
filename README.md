# Toolbox

A containerized development environment (Debian slim) bundling all the tools you need for daily development — Claude Code with skills/plugins, Git, cloud CLIs, Node.js and Python runtimes, Playwright, Docker. Controlled by a Go CLI (`toolbox`) that manages the container lifecycle with a single command.

## What's inside

Exact pinned versions live in [`internal/build/assets/Dockerfile`](internal/build/assets/Dockerfile) (Renovate-bumped; Go in `go.mod`, `golangci-lint` in the `Makefile`). The baked-in tooling:

- **Runtimes / package managers** — Node.js (24 LTS), pnpm, bun (JS runtime + package manager + bundler), Python 3, uv, Go toolchain (+ gopls, goimports)
- **AI agents** — Claude Code, OpenAI Codex CLI, pi (`@earendil-works/pi-coding-agent`), herdr (agent multiplexer TUI — detachable panes running AI agents)
- **Language servers / formatters** — Pyright (`pyright-langserver`), TypeScript language server (+ `@vue/typescript-plugin`, active once a project lists it in its tsconfig `compilerOptions.plugins`), TypeScript (`tsc`), shellcheck, shfmt
- **Browser automation** — Playwright CLI (+ `playwright-cli` SKILLS build)
- **Kubernetes / infra** — kubectl, kubectx + kubens, Helm, OpenTofu
- **Cloud CLIs** — Google Cloud SDK (gcloud), Azure CLI (az), Oracle OCI CLI, Google Workspace CLI (`gws`), Cloudflare CLI (`cf`, Wrangler vNext preview) + Wrangler (`wrangler`)
- **Git / forge** — GitHub CLI (gh), GitLab CLI (glab), Worktrunk (`wt`, git worktrees for parallel agents — [guide](docs/worktrunk.md)), Docker CLI + Docker Compose
- **Code intelligence** — graphify (PyPI `graphifyy`), codegraph (npm `@colbymchenry/codegraph`), SonarQube CLI (`sonar`)
- **Shell / CLI ergonomics** — Zsh bundle (Oh-My-Zsh + fzf + zoxide), atuin (SQLite shell history, Ctrl-R fuzzy search), tmux, fd, eza, jq, yq, starship, bat, git
- **Runtime package sources** — Homebrew (Linuxbrew — `brew install` anything else at runtime)

Every tool ships unconditionally — there is no per-tool opt-out. Need something that isn't baked in? `brew install <tool>` or `sudo apt install <tool>` inside the shell; both are ephemeral and vanish when the container exits.

## Install

### macOS (Homebrew)

```bash
brew install filippolmt/tap/toolbox
```

> Migrating from an older install that used the Homebrew *formula*? GoReleaser deprecated formulas in favour of casks, so the tap now ships a cask — run `brew uninstall --force toolbox && brew install --cask filippolmt/tap/toolbox` once, then regular `brew upgrade` keeps it current.

### From source

Requires the Go version named by the `toolchain` directive in `go.mod` (or newer):

```bash
go install github.com/filippolmt/toolbox@latest
```

### Verify installation

```bash
toolbox version
```

## Prerequisites

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) must be installed and running
- A [Nerd Font](https://www.nerdfonts.com/) in your **host** terminal (Ghostty, iTerm2, etc.). The bundled [starship](https://starship.rs/) prompt emits Nerd Font glyphs (git branch, kubernetes, gcloud, cmd duration); without one you'll see `?`/`▢` placeholders. Install on the host — fonts are rendered by the host terminal, not the container. Example: `brew install --cask font-jetbrains-mono-nerd-font`.

## Quick start

```bash
# Pull the toolbox image from GHCR
docker pull ghcr.io/filippolmt/toolbox:latest

# Enter the toolbox
toolbox shell
```

On first run, `toolbox shell` creates and starts the container with default volume mounts. If the container is already running, it reattaches to it (multiple terminals can share one container).

### Disposable lifecycle

The container is disposable: when the last attached shell exits it is destroyed. All persistent state lives on the bind mounts under `~/.toolbox/` (credentials, shell history, caches), so nothing is lost — the next `toolbox shell` creates a fresh container and re-mounts the same state.

Teardown is offloaded to the Docker daemon (the container is created with `AutoRemove`): on `exit` the CLI sends a kill and returns immediately, while the daemon unmounts and deletes the container in the background. This keeps the prompt from blocking on the mount teardown, which is otherwise slow on macOS Docker Desktop with many bind mounts. A consequence is that exiting always removes the container (a detached shell, marked in `toolbox list`, is the one exception and waits for `toolbox stop`) — there is no stopped container to reuse, so each `toolbox shell` rebuilds the session from the canonical image plus your mounted state. Full mechanics: [container lifecycle internals](docs/internals/container-lifecycle.md#container-teardown).

## CLI commands

| Command | Description |
|---------|-------------|
| [`toolbox shell [name\|dir]`](docs/commands.md#toolbox-shell) | Start or attach to a toolbox container (`-p` publishes ports, `-B`/`--oauth` handle OAuth callbacks, `--profile` isolates a second account, named shells via [`shells:`](docs/shells.md)) |
| [`toolbox stop [name\|dir]`](docs/commands.md#toolbox-stop) | Stop and remove toolbox containers (`--all` for every one on the host) |
| [`toolbox list`](docs/commands.md#toolbox-list) | List toolbox containers running on the host (alias `ls`) |
| [`toolbox orca [dir]`](docs/commands.md#toolbox-orca) | Run [Orca](docs/orca.md)'s runtime in a detached Orca Shell and pair the desktop app (`shell`, `pair`, `stop`; `--profile`) |
| [`toolbox herdr [dir]`](docs/commands.md#toolbox-herdr) | Host herdr-core for the [Herdr desktop app](docs/herdr.md) in a detached Herdr Core Shell and register it as a machine (`shell`, `stop`; `--profile`, `--reregister`). Not the Herdr TUI, which runs as `herdr` inside any toolbox shell |
| [`toolbox build`](docs/commands.md#toolbox-build) | Build the Docker image locally from the embedded context |
| [`toolbox version`](docs/commands.md#toolbox-version) | Show version info |
| [`toolbox init`](docs/commands.md#toolbox-init) | Write an annotated `.toolbox.yaml` in the current directory |
| [`toolbox config`](docs/commands.md#toolbox-config) | Inspect and scaffold configuration (`show`, `example`, `path`, `edit`, `set`, `doctor`, `ui`) |
| [`toolbox mounts`](docs/mounts.md#mounts-cli) | Manage bind-mount entries (`list`, `add`, `disable`, `remove`, `root`) |
| [`toolbox shells`](docs/shells.md) | Manage named shell shortcuts (`list`, `get`, `add`, `set`, `remove`) |
| [`toolbox bridge`](docs/bridge.md) | Manage the host-side daemon forwarding browser/editor/proximo/sound/host-terminal calls (`install`, `uninstall`, `status`) |
| [`toolbox completion [bash\|zsh\|fish]`](docs/commands.md#toolbox-completion) | Generate shell completions |
| [`--config <file>`](docs/commands.md#global-flag---config) | Global flag on every command: load exactly this config file |

Full flag-level reference: [docs/commands.md](docs/commands.md).

## Configuration

Optional, via `~/.toolbox.yaml` (global) or `.toolbox.yaml` in the project directory (overrides; found by walking up from the CWD). `toolbox init` scaffolds an annotated file; full key semantics in [docs/configuration.md](docs/configuration.md).

| Key | One-liner |
|-----|-----------|
| [`mounts`](docs/mounts.md#mounts-merge-semantics) | Patch / replace / append / disable the default bind mounts by `name`. |
| [`mounts_root`](docs/mounts.md#mounts_root-retarget) | Retarget the `~/.toolbox/`-managed default mounts, all but the bridge, to a custom root (encrypted volume, work drive). |
| [`inherit_host_auth`](docs/configuration.md#inherit-host-auth) | Opt listed CLIs (`gh`, `gcloud`, …) into the host's real credential path instead of the isolated default. |
| [`shells`](docs/shells.md) | Named shell shortcuts: `<name>: {path, env}` → `toolbox shell <name>`. |
| [`shell`](docs/configuration.md#shell) | Login shell inside the container (only `zsh` is supported). |
| [`image`](docs/configuration.md#image-selection) | Full image ref override (proxy hub / pull-through cache). |
| [`registry_mirror`](docs/configuration.md#image-selection) | Swap only the registry host of the canonical image ref. |
| [`pull`](docs/configuration.md#image-selection) | Registry-sync policy: `auto` (default) / `always` / `never`. |
| [`bridge`](docs/bridge.md) | Toggle the host bridge mounts (browser / editor / proximo forwarding); default on. |
| [`browser_bridge`](docs/configuration.md#browser_bridge-deprecated) | **Deprecated** alias of `bridge`. |
| [`proximo`](docs/proximo.md) | `.test` apps + CA trust inside the container; omitted = auto-detect. |
| [`managed_statusline`](docs/configuration.md#managed_statusline) | Image-owned Claude Code statusline re-applied each shell start; `false` to keep your own. |
| [`image_reclaim`](docs/configuration.md#image_reclaim) | Reclaim runtime images an image update left nameless; default on, set `false` to keep every generation. |
| [`peer_messaging`](docs/configuration.md#peer_messaging) | Let Claude Code sessions in different toolbox containers see and message each other; default off, set `true` to opt in. |
| [`env`](docs/configuration.md#env-passthrough) | Arbitrary env vars injected into the in-container shell (global or per-shell). |

### Highlights

**Credential isolation & mounts.** By default the container never sees your real `~/.ssh`, `~/.gitconfig`, `~/.claude`, etc. — every credential path is isolated under `~/.toolbox/` on the host. The `mounts:` list patches the defaults by name, `mounts_root` relocates them wholesale, and `~/.toolbox/startup.d/*.sh` hooks run on every shell start. Shell config goes in `~/.toolbox-state/zshrc.d/*.zsh`, not `~/.zshrc` — the image rebuild truncates that one. → [docs/mounts.md](docs/mounts.md)

**Publishing ports & OAuth callbacks.** `toolbox shell -p <port>` forwards host ports docker-style; `--oauth <tool>` expands the documented recipe for CLIs with OAuth callback listeners (`-B` bridges container-loopback binds). → [docs/commands.md](docs/commands.md#publishing-ports)

**Bridge (browser / editor / proximo / sound / host terminals).** An opt-in per-user host daemon (`toolbox bridge install`) that lets in-container `xdg-open`, `code`/`codium`, and an allowlisted set of `proximo` subcommands reach the host's browser, editor, and proximo binary, herdr's agent chimes reach the host's audio output, and `hc-host` in the Herdr Core Shell message the agents herdr.app runs on the host — token-authenticated, `127.0.0.1`-only. → [docs/bridge.md](docs/bridge.md)

**Agent skills.** Bundled tools that publish an agent skill (gh, glab, herdr, Worktrunk, …) have it installed for Claude Code, Codex and pi at every container start, from the copy matching the bundled version. graphify, codegraph and playwright-cli are per-repo instead: opt a repo in once with `graphify-init`, `codegraph-init` or `pwcli-init` from inside it, and run `toolbox-refresh` there after an image update to bring those installs up to date — nothing writes into a repo on its own. → [shell-start internals](docs/internals/shell-start.md#per-repo-code-graph-skills-graphify-and-codegraph)

**Agent guardrails.** Claude Code, Codex and pi run a hook before every shell command that blocks these mistakes: a Docker bind mount from a container-local path (it would reach the host daemon empty), `git commit -a` and a `git stash` without a pathspec, which sweep up work that is not the agent's, a `git stash pop`/`apply` without a reference, which can apply another session's, and a discard of every uncommitted change in the tree (`git reset --hard`, `git checkout .`, `git restore .`, a forced `git clean` or switch). The agent gets the reason and the way out. → [troubleshooting](docs/troubleshooting.md#an-agent-command-is-blocked-by-the-agent-guardrails)

**Orca runtime.** `toolbox orca` runs [Orca](https://github.com/stablyai/orca)'s server in a detached Orca Shell at the desktop app's version, on host `127.0.0.1` only, and pairs the desktop app with it, so the agents Orca starts use the bundled CLIs and the isolated credentials. It keeps running until `toolbox orca stop`. → [docs/orca.md](docs/orca.md)

**Herdr desktop app.** `toolbox herdr` hosts herdr-core, the runtime the Herdr desktop app (herdr.app) talks to, in a detached Herdr Core Shell, and registers it with the host's herdr-core as the machine `toolbox`, reached through `docker exec`: no sshd, no published port. Spaces on that machine run their agents in the container until `toolbox herdr stop`, and those agents and the ones herdr.app runs on the host can message each other, locally (`hc send toolbox/t2` one way, `hc-host send` the other; `host_terminal_relay: false` turns the second off) — step by step in [Agents on the host](docs/herdr.md#agents-on-the-host). This is separate from the Herdr TUI listed under AI agents, which you run as `herdr` in a toolbox shell; the two coexist. → [docs/herdr.md](docs/herdr.md)

**Proximo (`.test` apps).** With [proximo](https://github.com/filippolmt/proximo) on the host, `https://<name>.test` URLs work from inside the container with the CA trusted — auto-detected, no per-repo opt-in. → [docs/proximo.md](docs/proximo.md)

## Documentation

Section-level index of every guide (commands, configuration, mounts, shells, bridge, proximo, troubleshooting, maintainer internals): **[docs/README.md](docs/README.md)**. Something broke? Start from [troubleshooting](docs/troubleshooting.md). Link integrity is CI-enforced (`make check-links` locally).

## Updating

```bash
brew upgrade toolbox
```

Or pull the latest image:

```bash
docker pull ghcr.io/filippolmt/toolbox:latest
```

After a new image, run `toolbox-refresh` inside each repo you opted in to graphify, codegraph or playwright-cli: their per-repo installs follow the bundled versions only when asked.

## License

MIT
