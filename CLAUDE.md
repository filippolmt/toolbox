<!-- GSD:project-start source:PROJECT.md -->
## Project

**Toolbox**

Un ambiente di sviluppo containerizzato (Debian slim) che racchiude tutti i tool necessari per lo sviluppo quotidiano — Claude Code con GSD/skill/plugin, Git, CLI cloud, runtime Node.js e Python, playwright, Docker. Controllato da una CLI Go (`toolbox`) che gestisce il ciclo di vita del container con un singolo comando. Pensato per Filippo come ambiente primario di lavoro e sandbox sicuro per Claude Code.

**Core Value:** Un singolo comando (`toolbox shell`) ti mette dentro un ambiente completo, isolato e riproducibile con tutti i tool configurati e pronti all'uso.

### Constraints

- **Base image**: Debian slim — compatibilità massima con i tool, dimensione contenuta
- **Versioni**: Tutte pinnate via ENV nel Dockerfile con verifica checksum SHA256
- **Persistenza**: Nessun dato critico dentro l'immagine — tutto montato via volumi configurabili
- **Security**: Container gira come utente non-root (user mapping dall'host)
- **Registry**: GHCR (ghcr.io) — integrato con il repo GitHub
<!-- GSD:project-end -->

<!-- GSD:stack-start source:research/STACK.md -->
## Technology Stack

## Recommended Stack
### Core Technologies
| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Go | 1.26.2 (latest stable) | CLI launcher binary | Single static binary output, trivial distribution via `go install`, excellent Docker SDK support. Version confirmed via go.dev/dl API. |
| debian:12-slim (bookworm-slim) | bookworm-slim | Container base | `apt` compatibility for all target tools (glab, gh, oci-cli, gcloud, kubectl). ~40MB base. Bookworm is stable until ~2028. Preferred over Ubuntu (less bloat), Alpine (musl libc breaks npm native modules), and Chainguard (too locked-down for a dev toolbox). |
| Node.js 22 LTS | 22-bookworm-slim | Runtime for Claude Code | Claude Code installs via npm; requires Node. Node 22 is Active LTS through April 2027. Use `node:22-bookworm-slim` as the explicit base tag to avoid musl/Alpine issues — npm native packages require glibc. |
| github.com/spf13/cobra | v1.10.2 (2025-12-04) | CLI framework for Go launcher | Industry standard for Go CLIs. Native bash/zsh/fish completion via `RegisterFlagCompletionFunc`. Deep Viper integration for YAML config. Used by kubectl, Hugo, GitHub CLI. v1.10.2 is the latest stable release. |
| github.com/spf13/viper | v1.21.0 (2025-09-08) | YAML config (~/.toolbox.yaml) | Natural companion to Cobra; reads `~/.toolbox.yaml` with zero boilerplate. Supports env var override (`TOOLBOX_*`), multiple config paths, and type-safe unmarshalling. v2 is not yet stable — stay on v1. |
| github.com/docker/docker (Moby client) | v29.4.0 / client pkg | Container lifecycle management | Official Docker Engine API Go client. Typed methods for ContainerCreate, ContainerStart, ContainerExecCreate, ContainerList. Use `client.WithAPIVersionNegotiation()` to handle daemon version skew. |
### Supporting Libraries
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/docker/docker/api/types/container | (bundled with moby) | Typed container config structs | Always — needed for HostConfig (volume mounts, socket bind), ContainerConfig |
| github.com/mitchellh/go-homedir | v1.1.0 | Resolve `~` in config paths | In Viper `initConfig()` to find `~/.toolbox.yaml` portably |
| github.com/fatih/color | v1.18.x | Colored terminal output | Status messages, error highlighting in CLI output |
| golang.org/x/term | stdlib | Detect TTY for interactive vs piped | Required before attaching stdio to `docker exec` — prevents broken output in non-TTY contexts |
### Development Tools
| Tool | Purpose | Notes |
|------|---------|-------|
| cobra-cli | Scaffold new cobra commands | `go install github.com/spf13/cobra-cli@latest` — generates boilerplate cmd/ structure |
| ko | Build and push Go container images | Alternative to Dockerfile for the Go binary itself; not needed here since the toolbox image is Debian-based, not distroless |
| docker/setup-buildx-action | Multi-platform builds in CI | Use v3 in GitHub Actions; pair with `docker/build-push-action@v6` |
| docker/login-action | Authenticate to GHCR in CI | Use v3; pass `${{ secrets.GITHUB_TOKEN }}` — no PAT needed for same-repo packages |
| goreleaser | Cross-compile and release Go CLI binary | Optional: produces `go install`-compatible releases and GitHub release assets |
## Installation
# Initialize Go module
# Core CLI dependencies
# Scaffold CLI structure
## Alternatives Considered
| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| cobra + viper | kong (alecthomas/kong) | Prefer kong when config comes from struct tags rather than YAML; simpler for smaller CLIs with no YAML config requirement. For this project, Viper YAML integration tips the balance to cobra. |
| cobra + viper | urfave/cli v2 | Simpler for flat flag-only CLIs. No good YAML config story. Avoid: historical issues with global flag scoping and subcommand flag conflicts. |
| github.com/docker/docker client | shell exec (`docker` binary) | Only if Docker SDK dependency is too heavy OR the CLI needs to work without Go toolchain (e.g., shell script). For a Go binary, the typed SDK is far safer than string-interpolated shell. |
| github.com/docker/docker client | github.com/docker/go-sdk | go-sdk is a higher-level wrapper (published Nov 2025) but is still immature. The core moby client (v29.x) is battle-tested. Revisit go-sdk in 6–12 months. |
| node:22-bookworm-slim (base for toolbox) | node:20-bookworm-slim | Node 20 enters Maintenance LTS and reaches EOL April 2026. Use 22 now. |
| debian:12-slim | alpine:3.21 | Use Alpine only if image size is the top priority AND no npm-installed native packages are needed. Claude Code's npm install fails on Alpine musl libc — ruled out. |
| Multi-stage Dockerfile (Go builder + debian:12-slim runtime) | Single-stage | Multi-stage keeps Go toolchain out of final image; the runtime image only needs the compiled binary. Always prefer multi-stage. |
## What NOT to Use
| Avoid | Why | Use Instead |
|-------|-----|-------------|
| urfave/cli v1 | Unmaintained; archived. | cobra or urfave/cli v2 |
| fsouza/go-dockerclient | Pre-dates official SDK; lags behind API versions. | github.com/docker/docker client |
| alpine base image | musl libc breaks `npm install @anthropic-ai/claude-code` — native bindings fail silently or crash. Confirmed by community Claude Code Docker setups recommending Debian slim. | debian:12-slim or node:22-bookworm-slim |
| Docker-in-Docker (DinD) | Requires privileged mode, complex setup, nested daemon overhead. PROJECT.md explicitly rules this out. | Docker socket mount (`/var/run/docker.sock`) |
| viper v2 (beta) | Not yet stable as of 2025-09. Backwards-incompatible API changes still in flux. | viper v1.21.0 |
| github.com/docker/go-sdk | Too immature (published Nov 2025). API unstable. | github.com/docker/docker v29 client |
| Hardcoded `latest` tag for base images | Non-reproducible builds; silent regressions when upstream changes. | Pin to `debian:12-slim` + SHA256 digest via `ADD --checksum` or Renovate/Dependabot for updates |
## Stack Patterns by Variant
- Multi-stage Dockerfile: `FROM golang:1.26-bookworm AS builder` → compile → `FROM debian:12-slim AS runtime` → copy binary
- The CLI binary itself does NOT live inside the toolbox container; it runs on the host and manages the container
- Base: `FROM node:22-bookworm-slim` — includes Node 22 + npm on Debian Bookworm slim
- Install Claude Code: `RUN npm install -g @anthropic-ai/claude-code` (no version pin on first pass; pin after validating specific version)
- Install remaining tools via `apt-get` + direct binary downloads with `ADD --checksum=sha256:<hash>` for version-pinned binaries (gh, glab, kubectl, helm, tofu)
- Non-root user: create `toolbox` user, run as that user; map to host UID via `--user $(id -u):$(id -g)`
- Auth: `docker/login-action@v3` with `registry: ghcr.io`, `username: ${{ github.actor }}`, `password: ${{ secrets.GITHUB_TOKEN }}`
- Tag strategy: `ghcr.io/${{ github.repository }}:latest` + `ghcr.io/${{ github.repository }}:${{ github.sha }}`
- Trigger: on push to `main` + manual `workflow_dispatch`
- Metadata: `docker/metadata-action@v5` to generate OCI labels automatically
- Mount `~/.claude` read-write — settings, memory, agents, GSD plugins
- Mount `~/.gitconfig` and `~/.gitconfig-dbm` read-only
- Mount `~/.ssh` read-only
- Mount `~/.secrets` read-only (sourced in container bashrc)
- Mount `/var/run/docker.sock` for Docker CLI access from inside container
- All mount paths are configurable in `~/.toolbox.yaml`; Go CLI reads them via Viper
- Cobra generates completion scripts natively: `toolbox completion bash`, `toolbox completion zsh`, `toolbox completion fish`
- Add `toolbox completion zsh > "${fpath[1]}/_toolbox"` to install docs
- No external libraries needed
## Version Compatibility
| Package | Compatible With | Notes |
|---------|-----------------|-------|
| cobra v1.10.2 | viper v1.21.0 | Both use pflag v1.0.5; no conflicts |
| github.com/docker/docker v29.4.0 | Go 1.26 | Uses `+incompatible` module path; import as `github.com/docker/docker/client` |
| node:22-bookworm-slim | @anthropic-ai/claude-code latest | Claude Code requires Node >= 18; 22 is the recommended active LTS |
| debian:12-slim (bookworm) | kubectl, helm, tofu, gh, glab | All publish `linux/amd64` Debian-compatible binaries; amd64 focus per PROJECT.md constraints |
## Claude Code Installation in Container
- Claude Code is distributed **only via npm** (`@anthropic-ai/claude-code`) — no binary releases, no apt package
- Requires glibc (Debian/Ubuntu base) — Alpine/musl breaks native npm dependencies
- Config lives in `~/.claude/` — must be volume-mounted for persistence; nothing critical baked into image
- Headless operation: `claude --no-interactive` or via MCP/sub-agent patterns works without TTY
## Dockerfile Structure (Recommended)
# Stage 1: build Go CLI binary
# Stage 2: toolbox runtime image (published to GHCR)
# ... tool installations ...
# The Go CLI binary is distributed separately via `go install`
# and runs on the HOST, not inside the container
## Sources
- `https://go.dev/dl/?mode=json` — Go 1.26.2 confirmed as latest stable (April 2026)
- `https://api.github.com/repos/spf13/cobra/releases/latest` — cobra v1.10.2 (2025-12-04)
- `https://api.github.com/repos/spf13/viper/releases/latest` — viper v1.21.0 (2025-09-08)
- `https://api.github.com/repos/moby/moby/releases/latest` — docker-v29.4.0 (2026-04-07)
- `https://pkg.go.dev/github.com/docker/docker/client` — ContainerCreate/Start/Exec API surface, HIGH confidence
- `https://github.com/anthropics/claude-code/blob/main/.devcontainer/Dockerfile` — Official Claude Code Docker reference (node:20 base, npm install pattern), HIGH confidence
- Context7 `/spf13/cobra` — Cobra v1.9.x completion, persistent flags, Viper integration, HIGH confidence
- `https://endoflife.date/nodejs` — Node.js 22 Active LTS through April 2027, HIGH confidence
- WebSearch "Debian slim Docker best practices 2025" — bookworm-slim ~40MB, stable until ~2028, MEDIUM confidence (multiple sources agree)
- WebSearch "GHCR GitHub Actions publish Docker image" — docker/login-action@v3 + GITHUB_TOKEN pattern, HIGH confidence (GitHub Docs primary source)
- WebSearch "Alpine musl libc npm native packages" — Alpine breaks Claude Code install, MEDIUM confidence (community consensus, devcontainer docs confirm Debian)
<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->
## Conventions

Conventions not yet established. Will populate as patterns emerge during development.
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->
## Architecture

Architecture not yet mapped. Follow existing patterns found in the codebase.
<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->
## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, or `.github/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->
## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:
- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->



<!-- GSD:profile-start -->
## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
