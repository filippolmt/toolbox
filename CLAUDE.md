# CLAUDE.md

Guidance for Claude Code on this repo.

## Project

**Toolbox** — containerised Debian-slim dev environment managed by a host-side Go CLI (`toolbox shell` enters a disposable workspace). Two artefacts, separate release pipelines: Go CLI (host) + runtime image (container).

## Dev commands

**Go is not installed on the host.** Commands run in `golang:1.26` (cache volume `toolbox-gomod`):

| Command | What it does |
|---------|--------------|
| `make go-test` | `go test ./... -count=1` |
| `make go-test-verbose` | `go test -v -race ./...` (opt-in; CGO on) |
| `make go-lint` | `golangci-lint run ./...` (CI-matched) |
| `make go-run` | Build CLI + open `toolbox shell` |
| `make go-run-clean` | Like `go-run` + stop existing container (env/mounts are fixed at ContainerCreate) |
| `make build` / `make test` | Build runtime image (tag: `ghcr.io/filippolmt/toolbox:latest`) / build + smoke-test |

Single test: `make go-shell`, then `go test ./internal/mountplan -run TestFoo -count=1`.

`make build` overwrites the local cache of the registry tag, so the next `./toolbox shell` picks up the freshly built image. Run `docker pull ghcr.io/filippolmt/toolbox:latest` to restore the upstream one.

Repo-local SDD: `./toolbox sdd list` shows pinned skill packs; `./toolbox sdd init <name>` wires the current repo (`.toolbox.yaml` opt-in + `.gitignore` fence from `Skill.GitignoreEntries`).

**Pre-push validation: use the `/verify` skill.** Mirrors `.github/workflows/ci.yml`. Never invoke `go test` / `golangci-lint` directly — host has no Go.

## Architecture

Host CLI in `cmd/` (cobra) + internal pipelines `config` → `mountplan` → `sessionplan` → `container` (Docker SDK). Runtime image baked from `internal/build/assets/` (Dockerfile + entrypoint + smoke-test + init.d/), embedded so `toolbox build` runs anywhere. Tool catalog (`internal/catalog`) drives Dockerfile build args + init.d bijection.

Adding a CLI to the image: use the `add-cli` skill — it covers all required edits (Dockerfile layer + ARG + `internal/catalog` `Entries` row + smoke bijection + Renovate + optional `~/.toolbox/<tool>` mount) and finishes with `/verify`. No per-tool opt-out (see Gotchas).

Pipeline seams (config plan, mount plan, session plan, tool catalog, init sequence) — read package code + `docs/runtime-notes.md` before refactoring.

Shared fs primitives live in `internal/fsx`: `Home()` (strict, empty-`$HOME` guard), `ExpandTilde()`, `AtomicWriteFile()`. Don't re-implement these per-package — `configio` re-exports the last two as thin facades. Soft sites that tolerate an empty home keep calling `os.UserHomeDir` directly. → [shared-fs-primitives](docs/runtime-notes.md#shared-fs-primitives)

## Code & language

- **Repo content English; chat with user Italian.**
- `AGENTS.md` is a symlink to this file (Codex CLI). Don't unlink unless dropping Codex.
- Standard `gofmt`; lint config `.golangci.yml`.

## Gotchas — backstory in [`docs/runtime-notes.md`](docs/runtime-notes.md)

- **Host UID mapping**: container runs `--user $(id -u):$(id -g)`; `/home/toolbox` world-writable. Don't revert to fixed UID. → [host-uid](docs/runtime-notes.md#host-uid-mapping)
- **Passwordless sudo**: base apt layer ships `sudo` + `/etc/sudoers.d/toolbox` (`ALL …NOPASSWD: ALL`, UID-agnostic by design) so runtime `sudo apt install …` works; container is `AutoRemove` so it's ephemeral. Caveat: sudo writes into bind mounts (`/workspace`, `~/.toolbox/*`) land `root:root` on host. Smoke test asserts setuid `sudo`. → [sudo](docs/runtime-notes.md#passwordless-sudo)
- **Auth isolation**: every credential under `~/.toolbox/` (canonical list `mountplan.Defaults()`); `~/.secrets` NOT mounted. `mounts:` patches/replaces/appends/disables defaults by `name`; `mounts_root` retargets pre-merge. → [auth-isolation](docs/runtime-notes.md#auth-isolation-under-toolbox), [mounts](docs/runtime-notes.md#mounts--auth-isolation)
- **Docker CLI checksum**: Layer 7 has no upstream `.sha256` — pin + HTTPS only (T-01-08). → [docker-checksum](docs/runtime-notes.md#docker-cli-checksum)
- **Two Docker version streams**: `DOCKER_CLI_VERSION` (29.x) and `go.mod` SDK (28.x `+incompatible`) move independently; `client.WithAPIVersionNegotiation()` handles drift. Don't align numerically. → [docker-streams](docs/runtime-notes.md#two-docker-version-streams)
- **Tool versions pinned**, Renovate-bumped. No per-tool opt-out — every CLI is installed unconditionally. → [tool-pinning](docs/runtime-notes.md#tool-version-pinning)
- **rtk arm64 / Rust base traps**: GLIBC mismatch, tag scheme `<ver>-slim-<distro>`, slim ships no curl/ca-certs. → [image-build](docs/runtime-notes.md#image-build)
- **Homebrew in image**: default prefix `/home/linuxbrew/.linuxbrew` (bottles work only there), world-writable + system `safe.directory` (variable UID), runtime `brew install` ephemeral like apt, analytics/auto-update off. Private GitLab taps via glab credential helper in system gitconfig. → [homebrew](docs/runtime-notes.md#homebrew), [gitlab-credential](docs/runtime-notes.md#gitlab-git-credential-helper-glab)
- **Image selection**: always `:latest` from GHCR. `toolbox build` overwrites the local cache when you need a custom build. No `local-<hash>` fallback, no catalog-driven image hash. → [image-selection](docs/runtime-notes.md#image-selection), [tools-removal](docs/runtime-notes.md#tools-removal)
- **`inherit_host_auth: [<key>, …]`**: opt CLI into reading host credential path (RO) instead of isolated `~/.toolbox/<key>/`. Whitelist on `catalog.Entry.HostAuthMount`. Default `[]` keeps full isolation. → [inherit-host-auth](docs/runtime-notes.md#inherit-host-auth)
- **Port bindings fixed at container creation**: `toolbox stop` before re-`shell -p …`. → [port-bindings](docs/runtime-notes.md#port-bindings-are-fixed-at-container-creation)
- **Loopback bridge `-B`**: static-port OAuth CLIs that bind `127.0.0.1` (shopify, vanilla wrangler) need `toolbox shell -B -p <port>:<port>`; init.d/70 spawns socat per port. Dynamic-port CLIs (cf) keep their build-time sed patch — bridge cannot pre-bind an unknown port. **Breaking UX**: `wrangler login` previously worked with `-p 8976:8976` alone; now requires `-B`. → [loopback-bridge](docs/runtime-notes.md#loopback-bridge)
- **Adding `init.d/<NN>-<tool>.sh` requires 3 synced edits**: (1) write script, (2) `InitScript` field on the matching `internal/catalog/catalog.go` `Entries` row, (3) `count -ne N` literal in `internal/build/assets/smoke-test.sh` bijection block. `TestCatalogInitDBijection` (Go) catches (1)+(2); (3) drifts silently — count by hand.
- **Adding shell completion for a CLI requires 2 synced edits**: (1) generate `_<tool>` into `/usr/share/zsh/vendor-completions/_<tool>` — either via `<tool> completion zsh` in the precompute Layer 20d, OR by extracting a pre-built `_<tool>` shipped in the CLI's release tarball within its own install layer (precedent: bat, fd, eza); (2) `count >= N` literal + adjacent comment in `internal/build/assets/smoke-test.sh` `_zsh_vendor_completions_check`. No test catches drift — count by hand on every add. (Pre-bash-removal there was a third edit in `bashrc.sh`; with bash gone as an interactive shell, the runtime loader is removed and zsh-only vendor completions are pre-baked at build time.)
- **rtk hook auto-wiring + privacy lockdown**: `RTK_TELEMETRY_DISABLED=1`, `RTK_TEE=0` load-bearing. → [rtk-hooks](docs/runtime-notes.md#rtk-hook-auto-wiring--telemetrytee-lockdown)
- **Codex nested sandbox**: codex always installed → Docker `seccomp=unconfined` always applied. → [codex-sandbox](docs/runtime-notes.md#codex-nested-sandbox)
- **Container teardown = AutoRemove**: containers created with `HostConfig.AutoRemove` (`container/lifecycle.go`). Shell exit `ContainerKill`s and returns — daemon removes async (fast prompt; macOS unmount of many binds is the slow part). Consequence: a stopped container is gone, so `runplan.ActionStart` (reuse-stopped) effectively never fires — every `toolbox shell` recreates. `teardown.OnShellExit` does one inspect → sibling-exec→leave / AutoRemove→kill / legacy→`StopOne`; `StopOne` tolerates the remove `Conflict` race. → [container-teardown](docs/runtime-notes.md#container-teardown)
- **Browser bridge**: opt-in host daemon (`toolbox browser-bridge install`) forwards in-container `xdg-open` to host browser. State `~/.toolbox/browser/` RO-mounted; `browser_bridge: false` skips the mount. Host-side toggle → no image-hash impact. → [browser-bridge](docs/runtime-notes.md#browser-bridge)
- **Proximo integration**: makes [proximo](https://github.com/filippolmt/proximo)-routed `https://<name>.test` apps reachable from inside the container, for ANY client. Tri-state `proximo` (`*bool`, `proximo.Enabled`): omitted → **auto** (on iff proximo's CA exists on host — installed = works everywhere, zero opt-in); `true`/`false` force. Host-side `toolbox shell` reads `proximo.hosts` labels off running containers → pins each to `host-gateway` in `ExtraHosts` (DNS), and RO-mounts `<config-dir>/proximo/tls/ca.pem`. `entrypoint.sh` then makes trust seamless: `sudo update-ca-certificates` (curl/git/wget/python-ssl) + `certutil` into `~/.pki/nssdb` (chromium, incl. Playwright — `libnss3-tools` in base apt layer) + `NODE_EXTRA_CA_CERTS` (node). Lone gap: python-requests/certifi → set `REQUESTS_CA_BUNDLE=$TOOLBOX_PROXIMO_CA`. Discovery at create-time only (re-`shell` for new hosts). Trust lives in `entrypoint.sh` (NOT `init.d` → ties to no catalog tool, no bijection edit). `internal/proximo` (pure) + `container/lifecycle.go` (discovery). → [proximo-integration](docs/runtime-notes.md#proximo-integration)
- **Skill discovery paths diverge**: Claude reads `~/.claude/skills/`, Codex reads `~/.agents/skills/` — wrappers shipping a SKILL.md need dual-install (see `init.d/60-glab.sh`). → [skill-paths](docs/runtime-notes.md#skill-discovery-paths-diverge-between-claude-and-codex)
- **SDD `.gitignore` fence**: `toolbox sdd init <key>` writes a fenced block under `# >>> sdd-managed/<key> (toolbox)` from `Skill.GitignoreEntries` globs (patterns, not enumerated paths — survive upstream bumps). Nil entries → fence skipped. → [sdd-gitignore](docs/runtime-notes.md#sdd-gitignore-fence)
- **Config load order** (highest first): `--config` → walked-up `.toolbox.yaml` → `~/.toolbox.yaml` → `TOOLBOX_*` env → defaults. Source of truth: `config.Plan` in `internal/config/plan.go`. Legacy `tools:` block → one-time stderr warning + ignore.

Releases: `v*` tag → GoReleaser + Homebrew. Merge to `main` → image push to GHCR. See [`CONTRIBUTING.md`](CONTRIBUTING.md).
