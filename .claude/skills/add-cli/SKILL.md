---
name: add-cli
description: Add a new CLI binary (or wire missing auth/persistence for an existing one) to the toolbox image — Dockerfile install layer + version ARG + `internal/catalog/catalog.go` `Entries` row + `smoke-test.sh` check + Renovate `customManager` + (when the CLI persists state) a `~/.toolbox/<tool>` bind-mount in `internal/mountplan/defaults.go`. Use this whenever the user says things like "add <X> to the toolbox", "install <X> in the container", "put <X> in the image", "add <X> CLI", "wire auth for <X>", "persist <X> credentials", "save <X> authentication", or names a binary they want available inside `toolbox shell`. Also use it when an audit shows a CLI is in the Dockerfile but its credentials don't survive `toolbox stop` — that's the gws-style half-installed case this skill explicitly handles. Always perform the edits autonomously and finish with `/verify`; don't hand the user a checklist to apply themselves.
---

# /add-cli

Wire a new CLI (or fix a half-wired one) into the toolbox image. The work is mechanical but spread across **four files** (five when the tool persists state), and a missing entry in any of them silently regresses something — Renovate stops bumping the version, a catalog bijection test fails the build, smoke-test passes a broken binary, or `gh auth login` writes to a tmpfs that vanishes on `toolbox stop`. Doing it all in one shot is faster and safer than triaging the gap later.

Two facts shape every edit here:

- **Every CLI is installed unconditionally.** There is no per-tool opt-out — no `INSTALL_<TOOL>` ARG, no `tools.<key>: false`, no skip-guard in the layer. The legacy `tools:` config block was removed; don't add opt-out plumbing.
- **`internal/catalog/catalog.go` is the single source of truth** for "what tools exist". The image is always the canonical `:latest` from GHCR (no per-build hash), so nothing you add has to preserve hash stability — but two Go tests fail the build if the catalog and the image/init.d drift apart (see step 3).

The pattern for the half-installed case crystallised in the gws auth-mount fix: gws was already in the Dockerfile but `gws auth login` lost credentials on every container recreate because no `~/.toolbox/gws` mount existed and the OS keyring backend isn't available inside the container. The fix landed in `internal/mountplan/defaults.go` (canonical mount list) + `internal/mountplan/defaults_test.go` (mount-count + `assertMount`) + Dockerfile (one ENV), and `/verify` came back green.

## When to branch

Before touching anything, classify the CLI into one of three states. Grep, don't guess:

```bash
grep -n "<TOOL>_VERSION" internal/build/assets/Dockerfile
grep -n "\"<tool>\"" internal/catalog/catalog.go
grep -n "<tool>" internal/build/assets/smoke-test.sh
grep -n "\"~/.toolbox/<tool>\"" internal/mountplan/defaults.go
grep -n "<TOOL>_VERSION" renovate.json
```

| State | What's there | What to do |
|-------|-------------|------------|
| **Brand-new** | nothing | Full pipeline: research → install layer → ARG → catalog entry → smoke-test → Renovate → (optional) auth mount + ENV |
| **Half-installed** (gws-style) | Dockerfile + catalog + smoke-test + Renovate, but no auth mount | Auth-only path: `defaults()` entry in `internal/mountplan/defaults.go` + test count + assertion in `internal/mountplan/defaults_test.go` + ENV override if needed |
| **Fully wired** | everything above | Stop. Tell the user it's already complete and ask what they actually want changed |

The half-installed case is real and common — a contributor adds a binary without realising the tool persists state under `~/.config/<tool>` or `~/.<tool>`. Auth that survives a single shell but disappears on `toolbox stop` is worse than no auth, because users blame their own setup.

## Brand-new CLI: the full pipeline

### 1. Research upstream

Pick the install method by matching the closest analog already in the Dockerfile. Don't invent a new pattern — every existing stage/layer encodes a hard-won fix (GLIBC mismatches, missing checksum files, pip vs apt vs npm).

| Source | Closest analog | Pattern |
|--------|----------------|---------|
| GitHub release, single static binary, sha256sums file published | `fetch-gh` stage | `curl tarball` + `curl checksums.txt` + `grep \| sha256sum -c -` |
| GitHub release, MUSL/GNU split | `fetch-gws`, `fetch-zoxide` stages | Pick MUSL — base image is bookworm GLIBC 2.36; `-gnu` builds targeting GLIBC ≥2.39 fail at runtime |
| GitHub release, no checksum file | `fetch-bat`, `fetch-zoxide` stages | Pin SHA256 per-arch as ARG literals; document accepted risk; Renovate bumps version, maintainer refreshes hashes |
| npm package | final-stage `pnpm` / `claude` / `codex` layers | `npm install -g <pkg>@${VERSION}`; install runs as root, runtime user can't bump → disable auto-update if upstream supports it |
| Python package | final-stage `oci` layer | `pip install --break-system-packages <pkg>==${VERSION}` (PEP 668 opt-out is intentional, single-purpose container) |
| Install script | (none currently) | `curl -fsSL <script> \| sh` — only when upstream provides no archive |
| GCloud-style bundle | `fetch-gcloud` stage | Distro tarball, accepted-risk no-checksum (T-01-08); relocatable SDKs run from `/out` in-stage |
| Debian package via apt | final-stage base apt / `azure-cli` layers | Last resort — pulls the world. Prefer a static binary unless the tool genuinely needs system integration |
| Vendor CDN zip (no GitHub releases) | `fetch-bun` stage | `curl` zip + SHA256 check + `unzip` installed in-stage (fetch-stage helpers never reach the final image) |

For GitHub releases use `gh release view --json tagName,assets -R <owner>/<repo>` to get the latest tag without scraping HTML. Verify the asset naming pattern across architectures (`linux_amd64` vs `linux-x86_64` vs `x86_64-unknown-linux-musl`) — this is the #1 source of layer bugs.

**Helper deps cost nothing in a fetch stage.** `fetch-base` provides `curl`, `git`, `tar`, CA certs and coreutils; anything extra a fetch stage apt-installs (`unzip`, `jq`, `python3`) stays out of the final image by construction. The frugality rule only applies to **final-stage** layers: there, reuse what the base apt layer already provides (`python3 -m zipfile -e` instead of installing `unzip`, `python3 -c` instead of jq) — final-stage apt installs do land in the image.

### 2. Edit the Dockerfile

Add the version pin in the global `ARG` block at the top (before the first `FROM` — global ARGs are re-declared bare inside the consuming stage). Keep the existing groupings intact.

Where the install goes depends on the source type — see [build-layout](../../../docs/runtime-notes.md#build-layout-parallel-fetch-stages--frequency-ordered-tail):

- **Static binary / tarball / relocatable bundle** → new `FROM fetch-base AS fetch-<tool>` stage next to its analogs, artefacts under `/out` mirroring the final filesystem, plus one `COPY --link --from=fetch-<tool> /out/ /` line in the final stage's COPY block. Fetch stages run in parallel and re-run independently on version bumps.
- **npm / pip / apt installs** (need the final stage's node/python/dpkg) → final-stage `RUN` layer, **placed by Renovate bump frequency**: rarely-bumped near the top of the RUN tail (azure/oci area), frequently-bumped near the end (claude/graphify area, before the completions precompute). Don't append blindly at the end of the file.

Stages install unconditionally (no opt-out guard). New fetch stages use this template:

```dockerfile
# <tool> (one-line purpose).
# <Why this install method, what's special about it, any accepted risk>
FROM fetch-base AS fetch-<tool>
ARG TARGETARCH
ARG <TOOL>_VERSION
RUN set -eux; \
    <download + checksum verify>; \
    <extract into /out/usr/local/bin>; \
    /out/usr/local/bin/<tool> --version
```

Always run `<tool> --version` (or equivalent) at the end of the stage/layer — it's the only thing that catches a successful install with a broken binary (wrong arch, mismatched GLIBC, etc.) before the smoke test.

Spell the tool consistently: the catalog `Key` must appear as a whole-word token somewhere in the Dockerfile (the install layer / ARG naturally provides it). That's what `TestCatalogDockerfilePresence` checks — e.g. the underscore key `playwright_cli` is allowed to match the token `playwright-cli`, but a typo'd or missing token fails the build.

### 3. `internal/catalog/catalog.go`

Append a row to `Entries`, alphabetical by `Key`. Keep it sorted for readability and so the bijection diffs stay legible — sorting no longer affects any image hash (there isn't one), it's purely housekeeping:

```go
{Key: "<tool>"},
```

`Entry` has three fields you may populate (the trailing `Description` / `SmokeTest` fields are reserved — leave them unset):

- **`Key`** — the tool key, also the `inherit_host_auth` value. Must match the token used in the Dockerfile layer/ARG.
- **`InitScript`** — set only when the tool ships a runtime `init.d/<NN>-<tool>.sh` script (see the init.d gotcha in CLAUDE.md for the synced edits that requires); leave `""` otherwise.
- **`HostAuthMount`** — set only when the tool should be eligible for `inherit_host_auth` (reading the host's real credential path, read-only). Most tools leave it nil. Shape: `&HostAuthMount{HostPath: "~/.config/<tool>", ContainerPath: "/home/toolbox/.config/<tool>"}`.

Two Go tests enforce the catalog↔image bijection, so a missing or misspelled entry fails `make go-test` (not just the slow image build):

- **`TestCatalogDockerfilePresence`** — every `Key` must appear in the embedded Dockerfile. (The reverse — an orphan install layer with no catalog row — is enforced socially by reviewers + this skill, because regex over arbitrary install verbs is unreliable. Catalog is the source of truth: if it's in the image, it's in `Entries`.)
- **`TestCatalogInitDBijection`** — the set of `InitScript` values (plus a tiny `systemInitScripts` carve-out for flag-driven scripts like the loopback bridge) must exactly equal the `init.d/*.sh` files on disk.

### 4. `internal/build/assets/smoke-test.sh`

Add a `check_optional` line in the same alphabetical-ish block (look around the other tool checks). Format: `check_optional "<key>" <binary> <version-command>`. The `<binary>` is what `command -v` checks; the version command confirms the binary is functional. Skip this only if the tool literally has no version flag.

If you also added an `init.d/<NN>-<tool>.sh` script, bump the hand-maintained `count -ne N` literal in the smoke-test's init.d bijection block. `TestCatalogInitDBijection` (Go) catches the catalog↔disk direction, but that smoke-test count is a separate literal that drifts silently — count by hand.

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

- **Auth or config under `~/.<tool>` / `~/.config/<tool>`** → add a `defaults()` entry in `internal/mountplan/defaults.go` mapping `~/.toolbox/<tool>` → the in-container default path (`/home/toolbox/.config/<tool>` or `/home/toolbox/.<tool>`).
- **Local cache (browser binaries, model weights, big artifacts)** → mount the same way. Playwright (`~/.cache/ms-playwright`) is the precedent.
- **Split-state tool (state spans two non-XDG paths upstream)** → two binds nested under a single `~/.toolbox/<tool>/` root, flat host layout. Precedents: rtk (`rtk/config` + `rtk/data`) and cf (`cf/auth` + `cf/config`). Use this only when the tool exposes no env override to consolidate; otherwise prefer one bind.
- **Pure stateless tool (jq, yq, bat)** → no mount. Skip this step.

Pattern (matches gws / gcloud / azure / oci — all live in `internal/mountplan/defaults.go`):

```go
// <Tool> auth + config — populated by `<tool> auth login` inside the container.
// Default config dir is <upstream-default> (overridable via <ENV> if upstream supports it).
{Name: "<tool>", Source: "~/.toolbox/<tool>", Target: "/home/toolbox/.config/<tool>", ReadOnly: false, CreateIfMissing: true},
```

The `Name:` field is **mandatory** — `mountplan.Merge` uses it to apply user `mounts:` patches/replaces/disables (e.g. `mounts: [{name: <tool>, source: /elsewhere}]`). A default mount without a `Name` silently breaks that contract; `TestDefaultsHaveNames` in `internal/mountplan/defaults_test.go` enforces uniqueness.

Then update `internal/mountplan/defaults_test.go`:
- Bump the count `if len(mounts) != N` (and the `Errorf` message — they're separate strings, easy to miss one).
- Add `assertMount(t, mounts, "~/.toolbox/<tool>", false, true)` next to the other cloud CLIs in `TestDefaults`.

### 6a. ENV-var overrides for keyring / config-dir

Some tools default to an OS keyring (Secret Service, Keychain) that doesn't exist inside the container. The `gws` install was the canonical case — without `GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file`, `gws auth login` errored with "no D-Bus session". Check the tool's docs for:

- A "file backend" / "plaintext backend" / "no-keyring" env var → set it in the Dockerfile near the layer (or before the layer if it influences install). Example: the gws layer is preceded by `ENV GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file`.
- A `<TOOL>_CONFIG_DIR` override → only set this if you're *not* using the bind-mount default path. Bind-mounting the upstream default is preferable because it survives upstream changing the override variable.

Set ENV unconditionally (Dockerfile `ENV` can't be conditional, and there's no opt-out anyway — every tool is always present).

### 7. README

The `## What's inside` table (the tool/version table near the top of `README.md`) tracks every user-visible tool. Add a row in the same rough order as the install layers. The version column uses the `M.m.x` form (so a patch bump doesn't re-trigger a doc PR).

## Half-installed CLI (gws-style fix)

Skip everything above, do only:

1. Research the tool's default state path on Linux (`~/.config/<tool>` is the modern default; legacy tools use `~/.<tool>`).
2. Check whether it needs a keyring-backend ENV (see 6a).
3. Add the `defaults()` entry in `internal/mountplan/defaults.go` (with `Name:` set) + bump the `len(mounts) != N` count and add the `assertMount` line in `internal/mountplan/defaults_test.go`.
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

If smoke-test is skipped, the test run did **not** validate the binary actually executes inside the image — only that the configuration is internally consistent (catalog bijection, mount counts, etc.). Tell the user this honestly and suggest `make build && make test` for an end-to-end check before they tag a release. Don't run `make build` implicitly: it's multi-minute and rebuilds every layer below the new one.

## Things that look like shortcuts but aren't

- **Don't skip the SHA256 verification** even when upstream offers no `.sha256` file. Use the per-arch ARG-literal pattern (zoxide / bat). Documented accepted risk is fine; unverified curl-pipe-bash is not.
- **Don't add a Dockerfile install layer without the matching `catalog.Entries` row.** `TestCatalogDockerfilePresence` fails the build, and even if it didn't, the catalog is the single discoverable list that drives `inherit_host_auth` eligibility, the init.d bijection, and "what's actually in this image". The Dockerfile installs; the catalog *declares*.
- **Don't fix lint/test failures the user didn't ask about** if they surface during `/verify`. Report them and stop. The `/verify` skill itself codifies this rule.
- **Don't mount `~/.secrets`** or any other host path that wasn't requested. The `DefaultMounts` doc comment explains why (D-08).

## CLAUDE.md gotcha

When a tool has a non-obvious quirk worth surfacing to future contributors (the gws keyring-backend trick is a perfect example), add a one-line bullet to the "Gotchas" section in `CLAUDE.md`. If the quirk is fully captured by an in-Dockerfile comment, skip the gotcha — duplication rots faster than it helps.
