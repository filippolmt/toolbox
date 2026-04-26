---
name: add-cli
description: Add a new CLI binary (or wire missing auth/persistence for an existing one) to the toolbox image — Dockerfile layer + version ARG + opt-out flag + `internal/config/tools.go` entry + `smoke-test.sh` check + Renovate `customManager` + (when the CLI persists state) `~/.toolbox/<tool>` bind-mount in `DefaultMounts()`. Use this whenever the user says things like "aggiungi <X> al toolbox", "install <X> in the container", "metti <X> nell'immagine", "add <X> CLI", "wire auth for <X>", "salva l'autenticazione di <X>", or names a binary they want available inside `toolbox shell`. Also use it when an audit shows a CLI is in the Dockerfile but its credentials don't survive `toolbox stop` — that's the gws-style half-installed case this skill explicitly handles. Always perform the edits autonomously and finish with `/verify`; don't hand the user a checklist to apply themselves.
---

# /add-cli

Wire a new CLI (or fix a half-wired one) into the toolbox image. The work is mechanical but spread across **six** files, and a missing entry in any of them silently regresses something — Renovate stops bumping the version, `IsDefaultTools` flips to false, smoke-test passes a broken binary, or `gh auth login` writes to a tmpfs that vanishes on `toolbox stop`. Doing it all in one shot is faster and safer than triaging the gap later.

The pattern crystallised in commit `[gws auth mount]`: gws was already in the Dockerfile but `gws auth login` lost credentials on every container recreate because no `~/.toolbox/gws` mount existed and the OS keyring backend isn't available inside the container. The fix touched `config.go` + `config_test.go` + Dockerfile (one ENV) and `/verify` came back green. That's the template.

## When to branch

Before touching anything, classify the CLI into one of three states. Grep, don't guess:

```bash
grep -n "<TOOL>_VERSION\|INSTALL_<TOOL>" internal/build/assets/Dockerfile
grep -n "\"<tool>\"" internal/config/tools.go
grep -n "<tool>" internal/build/assets/smoke-test.sh
grep -n "\"~/.toolbox/<tool>\"" internal/config/config.go
grep -n "<TOOL>_VERSION" renovate.json
```

| State | What's there | What to do |
|-------|-------------|------------|
| **Brand-new** | nothing | Full pipeline: research → install layer → ARG → tools.go → smoke-test → Renovate → (optional) auth mount + ENV |
| **Half-installed** (gws-style) | Dockerfile + tools.go + smoke-test + Renovate, but no auth mount | Auth-only path: `DefaultMounts()` + test count + assertion + ENV override if needed |
| **Fully wired** | everything above | Stop. Tell the user it's already complete and ask what they actually want changed |

The half-installed case is real and common — a contributor adds a binary without realising the tool persists state under `~/.config/<tool>` or `~/.<tool>`. Auth that survives a single shell but disappears on `toolbox stop` is worse than no auth, because users blame their own setup.

## Brand-new CLI: the full pipeline

### 1. Research upstream

Pick the install method by matching the closest analog already in the Dockerfile. Don't invent a new pattern — every existing layer encodes a hard-won fix (GLIBC mismatches, missing checksum files, pip vs apt vs npm).

| Source | Closest analog | Pattern |
|--------|----------------|---------|
| GitHub release, single static binary, sha256sums file published | Layer 5 (`gh`) | `curl tarball` + `curl checksums.txt` + `grep \| sha256sum -c -` |
| GitHub release, MUSL/GNU split | Layer 6a (`gws`), Layer 20c (`zoxide`) | Pick MUSL — base image is bookworm GLIBC 2.36; `-gnu` builds targeting GLIBC ≥2.39 fail at runtime |
| GitHub release, no checksum file | Layer 10a (`bat`), Layer 20c (`zoxide`) | Pin SHA256 per-arch as ARG literals; document accepted risk; Renovate bumps version, maintainer refreshes hashes |
| npm package | Layer 11 (`pnpm`), Layer 13 (`claude`), Layer 13b (`codex`) | `npm install -g <pkg>@${VERSION}`; install runs as root, runtime user can't bump → disable auto-update if upstream supports it |
| Python package | Layer 16 (`oci`) | `pip install --break-system-packages <pkg>==${VERSION}` (PEP 668 opt-out is intentional, single-purpose container) |
| Install script | Layer 12 (`uv`) | `curl -fsSL <script> \| sh` — only when upstream provides no archive |
| GCloud-style bundle | Layer 14 (`gcloud`) | Distro tarball, accepted-risk no-checksum (T-01-08) |
| Debian package via apt | Layer 1 base | Last resort — pulls the world. Prefer a static binary unless the tool genuinely needs system integration |
| Vendor CDN zip (no GitHub releases) | proposed `op` pattern | `curl` zip + per-arch SHA256 ARG literal + `python3 -m zipfile -e` for extraction (python3 is in Layer 1) |

For GitHub releases use `gh release view --json tagName,assets -R <owner>/<repo>` to get the latest tag without scraping HTML. Verify the asset naming pattern across architectures (`linux_amd64` vs `linux-x86_64` vs `x86_64-unknown-linux-musl`) — this is the #1 source of layer bugs.

**Reuse what's already in the base image** before adding apt deps. Layer 1 already provides `python3`, `curl`, `tar`, `git`, `make`, plus the standard coreutils. If you need to extract a zip, `python3 -m zipfile -e <zip> <dest>` works without `unzip`. If you need to parse JSON, jq is Layer 9 (later) so use `python3 -c` in earlier layers. Pulling in `apt-get install -y unzip` for a one-shot extraction adds image size and an apt-cache cleanup step you'll forget — it's almost never the right call.

### 2. Edit the Dockerfile

Add the version pin near the top with the other `ARG <TOOL>_VERSION=...` lines (around line 19-44). Keep the existing groupings — version pins, then opt-out flags, then layers — intact.

Add the opt-out flag in the `INSTALL_<TOOL>=true` block.

Add the layer in the right location: forge clients near gh/glab/gws (Layer 5/6/6a), cloud SDKs near gcloud/azure/oci (Layer 14+), shell utilities near jq/yq/bat (Layer 9/10/10a). Numbering is non-strict — `Layer 6a` shows that. New layer comments use this template:

```dockerfile
# -- Layer Nx: <tool> (one-line purpose) --------------------------------------
# <Why this install method, what's special about it, any accepted risk>
RUN set -eux; \
    if [ "${INSTALL_<TOOL>}" != "true" ]; then \
      echo "Skipping <tool> (tools.<key>=false)"; \
    else \
      <install commands>; \
      <tool> --version; \
    fi
```

Always run `<tool> --version` (or equivalent) at the end of the layer — it's the only thing that catches a successful install with a broken binary (wrong arch, mismatched GLIBC, etc.).

### 3. `internal/config/tools.go`

Add the key to `KnownTools` (alphabetically — the comment explains the hash-stability reason). Add the matching `ToolBuildArg` mapping. The key in `tools.<key>` config is usually the lowercase tool name; the ARG is `INSTALL_<UPPER>`.

### 4. `internal/build/assets/smoke-test.sh`

Add a `check_optional` line in the same alphabetical-ish block (look around lines 169-194). Format: `check_optional "<key>" <binary> <version-command>`. The `<binary>` is what `command -v` will check; the version command is what runs to confirm the binary is functional. Skip this only if the tool literally has no version flag.

### 5. `renovate.json`

Add a `customManagers` entry so Renovate auto-PRs version bumps. **Verify the datasource exists before picking it** — not every CLI publishes a public GitHub release feed. Some ship only via vendor CDN, apt, or homebrew. Picking the wrong datasource silently freezes the version.

Quick verification recipe:
- `gh release list -R <owner>/<repo>` returns rows → `github-releases` is valid.
- Returns empty / 404 → fall back to whatever channel the vendor *does* publish (apt repo `Release` file, npm registry, PyPI, Docker registry).
- The CLI you're adding might live under a non-obvious org name (`1Password/op` ≠ `agilebits/op`). Confirm by visiting the upstream docs' "install" page and following the actual download link.

Then pick the datasource:

- **GitHub releases**: `datasourceTemplate: "github-releases"`, `packageNameTemplate: "owner/repo"`. If tags are `v1.2.3`, also set `extractVersionTemplate: "^v(?<version>.*)$"` so Renovate strips the leading `v` (the ARG value rarely keeps it).
- **GitLab releases**: `datasourceTemplate: "gitlab-releases"`.
- **npm**: `datasourceTemplate: "npm"`, `packageNameTemplate: "<pkg>"`.
- **PyPI**: `datasourceTemplate: "pypi"`.
- **Docker image**: `datasourceTemplate: "docker"`.
- **Go module**: `datasourceTemplate: "go"`.
- **Apt / deb repo** (CDN-only tools — `op`, Microsoft's azure-cli mirror, etc.): `datasourceTemplate: "deb"`, `packageNameTemplate: "<pkg>?suite=<suite>&components=<components>&binaryArch=amd64"`, `registryUrlTemplate: "<https://repo-base-url/>"`. Use this when the vendor only ships through their apt mirror — Renovate scrapes the `Release` file there.
- **Git ref by SHA** (oh-my-zsh-style): `datasourceTemplate: "git-refs"`, `currentValueTemplate: "master"`, `versioningTemplate: "git"`, capture as `(?<currentDigest>[a-f0-9]{40})`.

Without a Renovate entry — or with a *wrong* one — the version pin freezes silently. A CI bot will keep the rest of the image fresh while your tool decays. That's the failure mode this step prevents.

### 6. Persistent state (auth, config, cache)

Most CLIs persist *something*. Decide before merging:

- **Auth or config under `~/.<tool>` / `~/.config/<tool>`** → add a `DefaultMounts()` entry mapping `~/.toolbox/<tool>` → the in-container default path (`/home/toolbox/.config/<tool>` or `/home/toolbox/.<tool>`).
- **Local cache (browser binaries, model weights, big artifacts)** → mount the same way. Playwright (`~/.cache/ms-playwright`) is the precedent.
- **Pure stateless tool (jq, yq, bat)** → no mount. Skip this step.

Pattern (matches gws / gcloud / azure / oci):

```go
// <Tool> auth + config — populated by `<tool> auth login` inside the container.
// Default config dir is <upstream-default> (overridable via <ENV> if upstream supports it).
{Source: "~/.toolbox/<tool>", Target: "/home/toolbox/.config/<tool>", ReadOnly: false, CreateIfMissing: true},
```

Then update `internal/config/config_test.go`:
- Bump the count `if len(mounts) != N` (and the `Errorf` message — they're separate strings, easy to miss one).
- Add `assertMount(t, mounts, "~/.toolbox/<tool>", false, true)` next to the other cloud CLIs.
- The `TestLoadWithoutConfig` count check uses the same N — update both.

### 6a. ENV-var overrides for keyring / config-dir

Some tools default to an OS keyring (Secret Service, Keychain) that doesn't exist inside the container. The `gws` install was the canonical case — without `GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file`, `gws auth login` errored with "no D-Bus session". Check the tool's docs for:

- A "file backend" / "plaintext backend" / "no-keyring" env var → set it in the Dockerfile near the layer (or before the layer if it influences install). Example: the gws layer is preceded by `ENV GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file`.
- A `<TOOL>_CONFIG_DIR` override → only set this if you're *not* using the bind-mount default path. Bind-mounting the upstream default is preferable because it survives upstream changing the override variable.

Set ENV unconditionally (Dockerfile `ENV` can't be conditional). Harmless when the tool is opted out — the variable just sits unused.

### 7. README

The `## Tools bundled` table (lines 7-31) tracks every user-visible tool. Add a row in the same rough order as the install layers. The version column uses the `M.m.x` form (so a patch bump doesn't re-trigger a doc PR).

## Half-installed CLI (gws-style fix)

Skip everything above, do only:

1. Research the tool's default state path on Linux (`~/.config/<tool>` is the modern default; legacy tools use `~/.<tool>`).
2. Check whether it needs a keyring-backend ENV (see 6a).
3. Add the `DefaultMounts()` entry + bump `config_test.go` counts + add the `assertMount`.
4. If a keyring-backend ENV is needed, add it to the Dockerfile near the existing layer.
5. `/verify`.

This took ~5 edits for gws and shipped clean. Don't over-engineer.

## What success looks like

Run `/verify` at the end. Expect:

```
lint:       OK
go-test:    OK
smoke-test: SKIPPED   # unless `make build` already ran in this session
```

If smoke-test is skipped, the test run did **not** validate the binary actually executes inside the image — only that the configuration is internally consistent. Tell the user this honestly and suggest `make build && make test` for an end-to-end check before they tag a release. Don't run `make build` implicitly: it's multi-minute and rebuilds every layer below the new one.

## Things that look like shortcuts but aren't

- **Don't skip the SHA256 verification** even when upstream offers no `.sha256` file. Use the per-arch ARG-literal pattern (zoxide / bat). Documented accepted risk is fine; unverified curl-pipe-bash is not.
- **Don't bypass the opt-out flag** — every tool needs an `INSTALL_<TOOL>=true` ARG so users can disable it via `tools.<key>: false`. Skipping it breaks `ResolveImage`'s local-image hashing.
- **Don't add the tool to `KnownTools` without alphabetising** — the comment explicitly calls out the hash-stability requirement. A reordered list silently invalidates every cached `toolbox:local-<hash>` image.
- **Don't fix lint/test failures the user didn't ask about** if they surface during `/verify`. Report them and stop. The `/verify` skill itself codifies this rule.
- **Don't mount `~/.secrets`** or any other host path that wasn't requested. The `DefaultMounts` doc comment explains why (D-08).

## CLAUDE.md gotcha

When a tool has a non-obvious quirk worth surfacing to future contributors (the gws keyring-backend trick is a perfect example), add a one-line bullet to the "Non-obvious gotchas" section in `CLAUDE.md`. If the quirk is fully captured by an in-Dockerfile comment, skip the gotcha — duplication rots faster than it helps.
