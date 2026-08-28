---
name: add-cli
description: Wire a CLI into the toolbox image — install layer, catalog row, smoke check, Renovate manager, auth bind-mount. Use when the user wants a binary available inside `toolbox shell`, or when an already-installed CLI's credentials don't survive a container recreate.
---

# /add-cli

Wire a CLI into the toolbox image. The work is mechanical but spread across **four files** (five when the tool persists state), and a gap in any one silently regresses something — Renovate stops bumping the version, a catalog bijection test fails the build, smoke-test blesses a broken binary, or `<tool> auth login` writes to a tmpfs that vanishes on `toolbox stop`. Apply every edit yourself and finish on the gate; a checklist handed back to the user is not the deliverable.

Two facts shape every edit here:

- **Every CLI installs unconditionally.** No `INSTALL_<TOOL>` ARG, no `tools.<key>: false`, no skip-guard in the layer — the legacy `tools:` config block is gone. Leave opt-out plumbing out.
- **`internal/catalog/catalog.go` declares what tools exist.** The Dockerfile installs; the catalog *declares*, and that declaration drives `inherit_host_auth` eligibility, the init.d bijection and "what's actually in this image". The image is always the canonical `:latest` from GHCR (no per-build hash), so nothing you add has to preserve hash stability — but two Go tests fail the build when the catalog and the image/init.d drift apart (step 3).

## When to branch

Classify the CLI first. Grep, don't guess:

```bash
grep -n "<TOOL>_VERSION" internal/build/assets/Dockerfile
grep -n "\"<tool>\"" internal/catalog/catalog.go
grep -n "<tool>" internal/build/assets/smoke-test.sh
grep -n "\"~/.toolbox/<tool>\"" internal/mountplan/defaults.go
grep -n "<TOOL>_VERSION" renovate.json
```

| State | What's there | Path to take |
|-------|-------------|--------------|
| **Brand-new** | nothing | Full pipeline, steps 1–7 |
| **Half-installed** (gws-style) | everything except the auth mount | [Half-installed](#half-installed-cli): mount + test, nothing else |
| **Fully wired** | all five | Stop. Say it's already complete and ask what should change |

Half-installed is common: a contributor adds a binary without realising the tool persists state under `~/.config/<tool>` or `~/.<tool>`. Auth that survives one shell and disappears on `toolbox stop` is worse than no auth, because users blame their own setup.

## Brand-new CLI: the full pipeline

### 1. Research upstream

Pick the install method by matching the closest analog already in the Dockerfile. Every existing stage encodes a hard-won fix (GLIBC mismatches, missing checksum files, pip vs apt vs npm), so match a pattern instead of inventing one.

| Source | Closest analog | Pattern |
|--------|----------------|---------|
| GitHub release, single static binary, sha256sums published | `fetch-gh` stage | `curl tarball` + `curl checksums.txt` + `grep \| sha256sum -c -` |
| GitHub release, MUSL/GNU split | `fetch-gws`, `fetch-zoxide` stages | Pick MUSL — base image is bookworm GLIBC 2.36; `-gnu` builds targeting GLIBC ≥2.39 fail at runtime |
| GitHub release, no checksum file | `fetch-bat`, `fetch-zoxide` stages | Version pin + HTTPS only. Never hand-pin per-arch SHA256 literals: they go stale on every version bump and on an upstream re-tag of the same version, breaking the build for a guarantee that pinning your own hash never provided |
| npm package | final-stage `pnpm` / `claude` / `codex` layers | `npm install -g <pkg>@${VERSION}`; install runs as root, runtime user can't bump → disable auto-update if upstream supports it |
| Python package | final-stage `oci` layer | `pip install --break-system-packages <pkg>==${VERSION}` (PEP 668 opt-out is intentional, single-purpose container) |
| Install script | (none currently) | `curl -fsSL <script> \| sh` — only when upstream ships no archive at all |
| GCloud-style bundle | `fetch-gcloud` stage | Distro tarball, accepted-risk no-checksum (T-01-08); relocatable SDKs run from `/out` in-stage |
| Debian package via apt | final-stage base apt / `azure-cli` layers | Last resort — pulls the world. Prefer a static binary unless the tool genuinely needs system integration |
| Vendor CDN zip (no GitHub releases) | `fetch-bun` stage | `curl` zip + SHA256 check + `unzip` installed in-stage (fetch-stage helpers never reach the final image) |

Prefer a verified archive over a piped install script: `curl … | sh` is the last resort of this table, not a shortcut past the checksum work.

For GitHub releases, `gh release view --json tagName,assets -R <owner>/<repo>` gives the latest tag without scraping HTML. Verify the asset naming pattern across architectures (`linux_amd64` vs `linux-x86_64` vs `x86_64-unknown-linux-musl`) — the #1 source of layer bugs.

**Helper deps cost nothing in a fetch stage.** `fetch-base` provides `curl`, `git`, `tar`, CA certs and coreutils; anything extra a fetch stage apt-installs (`unzip`, `jq`, `python3`) stays out of the final image by construction. Frugality binds **final-stage** layers only: there, reuse what the base apt layer already provides (`python3 -m zipfile -e` over installing `unzip`, `python3 -c` over jq), because final-stage apt installs do land in the image.

### 2. Edit the Dockerfile

Add the version pin in the global `ARG` block at the top (before the first `FROM` — global ARGs are re-declared bare inside the consuming stage), keeping the existing groupings intact.

Where the install goes depends on the source type — see [build-layout](../../../docs/internals/image-build.md#build-layout-parallel-fetch-stages--frequency-ordered-tail):

- **Static binary / tarball / relocatable bundle** → new `FROM fetch-base AS fetch-<tool>` stage next to its analogs, artefacts under `/out` mirroring the final filesystem, plus one `COPY --link --from=fetch-<tool> /out/ /` line in the final stage's COPY block. Fetch stages run in parallel and re-run independently on version bumps.
- **npm / pip / apt installs** (need the final stage's node/python/dpkg) → final-stage `RUN` layer, **placed by Renovate bump frequency**: rarely-bumped near the top of the RUN tail (azure/oci area), frequently-bumped near the end (claude/graphify area, before the completions precompute). Place it deliberately, not at the end of the file.

New fetch stages use this template:

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

Always end the stage or layer with `<tool> --version` (or equivalent) — it's the only thing that catches a successful install with a broken binary (wrong arch, mismatched GLIBC) before the smoke test.

Spell the tool consistently: the catalog `Key` must appear as a whole-word token somewhere in the Dockerfile (the install layer / ARG naturally provides it). That's what `TestCatalogDockerfilePresence` checks — the underscore key `playwright_cli` is allowed to match the token `playwright-cli`, but a typo'd or missing token fails the build.

### 3. `internal/catalog/catalog.go`

Append a row to `Entries`, alphabetical by `Key` — housekeeping only now that no image hash depends on order, but it keeps the bijection diffs legible:

```go
{Key: "<tool>"},
```

`Entry` has exactly three fields:

- **`Key`** — the tool key, also the `inherit_host_auth` value. Must match the token used in the Dockerfile layer/ARG.
- **`InitScript`** — set only when the tool ships a runtime `init.d/<NN>-<tool>.sh` script (the init.d gotcha in `.claude/rules/image-build.md` lists the synced edits that requires); `""` otherwise.
- **`HostAuthMount`** — set only when the tool should be eligible for `inherit_host_auth` (reading the host's real credential path, read-only). Most tools leave it nil. Shape: `&HostAuthMount{HostPath: "~/.config/<tool>", ContainerPath: "/home/toolbox/.config/<tool>"}`.

Two Go tests enforce the catalog↔image bijection, so a missing or misspelled entry fails `make go-test` (not just the slow image build):

- **`TestCatalogDockerfilePresence`** — every `Key` must appear in the embedded Dockerfile. The reverse (an orphan install layer with no catalog row) is enforced socially by reviewers and this skill, because regex over arbitrary install verbs is unreliable.
- **`TestCatalogInitDBijection`** — the set of `InitScript` values (plus a tiny `systemInitScripts` carve-out for flag-driven scripts like the loopback bridge) must exactly equal the `init.d/*.sh` files on disk.

### 4. `internal/build/assets/smoke-test.sh`

Add a `check_optional "<key>" <binary> <version-command>` line in the same alphabetical-ish block as the other tool checks. `<binary>` is what `command -v` checks; the version command confirms the binary is functional. Skip only if the tool has no version flag at all.

If you also added an `init.d/<NN>-<tool>.sh` script, bump the `count -ne N` gate **and** the `N (M catalog InitScripts + K system …)` message in the smoke-test's init.d bijection block. `TestSmokeTestInitDCountLiteral` derives all three numbers from the catalog plus the embedded `init.d/`, so let `make go-test` tell you what they should be rather than counting by hand.

If the tool ships a zsh completion into `/usr/share/zsh/vendor-completions/`, bump the `-ge N` floor in `_zsh_vendor_completions_check` the same way — `TestSmokeTestVendorCompletionsFloor` derives N from the Dockerfile write sites. The completion gotcha in `.claude/rules/image-build.md` covers the two edits and the declared-exception list.

### 5. `renovate.json`

Add a `customManagers` entry, copying the shape of the closest of the ~50 managers already there. **Confirm the feed exists before picking a datasource** — a wrong datasource freezes the version pin silently while CI keeps the rest of the image fresh. That silent freeze is the failure mode this step prevents.

- `gh release list -R <owner>/<repo>` returns rows → `github-releases`, `packageNameTemplate: "owner/repo"`. Tags shaped `v1.2.3` also need `extractVersionTemplate: "^v(?<version>.*)$"`, since the ARG value rarely keeps the `v`.
- Empty or 404 → follow the actual download link from the vendor's install docs (the org name is often non-obvious: `1Password/op` ≠ `agilebits/op`) and use the channel they *do* publish: `npm`, `pypi`, `docker`, `go`, `gitlab-releases`.
- CDN-only tools shipping through a vendor apt mirror (`op`, Microsoft's azure-cli mirror): `datasourceTemplate: "deb"`, `packageNameTemplate: "<pkg>?suite=<suite>&components=<components>&binaryArch=amd64"`, `registryUrlTemplate: "<https://repo-base-url/>"` — Renovate scrapes the `Release` file there.
- Pinned git SHA (oh-my-zsh style): `git-refs` + `currentValueTemplate: "master"` + `versioningTemplate: "git"`, capturing `(?<currentDigest>[a-f0-9]{40})`.

### 6. Persistent state (auth, config, cache)

Most CLIs persist *something*. Decide before merging:

- **Auth or config under `~/.<tool>` / `~/.config/<tool>`** → add a `defaults()` entry in `internal/mountplan/defaults.go` mapping `~/.toolbox/<tool>` to the in-container default path (`/home/toolbox/.config/<tool>` or `/home/toolbox/.<tool>`).
- **Local cache (browser binaries, model weights, big artifacts)** → mount the same way. Playwright (`~/.cache/ms-playwright`) is the precedent.
- **Split-state tool (state spans two non-XDG paths upstream)** → two binds nested under a single `~/.toolbox/<tool>/` root, flat host layout. Precedents: rtk (`rtk/config` + `rtk/data`) and cf (`cf/auth` + `cf/config`). Use this only when the tool exposes no env override to consolidate; otherwise prefer one bind.
- **Pure stateless tool (jq, yq, bat)** → no mount. Skip this step.

Mount only paths the user asked for — never `~/.secrets` or another host path nobody requested. The `DefaultMounts` doc comment explains why (D-08).

Pattern (matches gws / gcloud / azure / oci, all in `internal/mountplan/defaults.go`):

```go
// <Tool> auth + config — populated by `<tool> auth login` inside the container.
// Default config dir is <upstream-default> (overridable via <ENV> if upstream supports it).
{Name: "<tool>", Source: "~/.toolbox/<tool>", Target: "/home/toolbox/.config/<tool>", ReadOnly: false, CreateIfMissing: true},
```

`Name:` is **mandatory** — `mountplan.Merge` uses it to apply user `mounts:` patches/replaces/disables (e.g. `mounts: [{name: <tool>, source: /elsewhere}]`). A default mount without a `Name` silently breaks that contract; `TestDefaultsHaveNames` enforces uniqueness.

Then update `internal/mountplan/defaults_test.go`:
- Bump the count `if len(mounts) != N` **and** the `Errorf` message — separate strings, easy to miss one.
- Add `assertMount(t, mounts, "~/.toolbox/<tool>", false, true)` next to the other cloud CLIs in `TestDefaults`.

**Keyring and config-dir ENV overrides.** Some tools default to an OS keyring (Secret Service, Keychain) that doesn't exist inside the container — `gws auth login` errored with "no D-Bus session" until `ENV GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file` preceded its layer. Check the tool's docs for:

- A file / plaintext / no-keyring backend variable → set it in the Dockerfile near the layer (before the layer when it influences install).
- A `<TOOL>_CONFIG_DIR` override → set it only when you're *not* bind-mounting the upstream default path. Bind-mounting the default is preferable: it survives upstream renaming the override variable.

ENV is unconditional (Dockerfile `ENV` can't branch, and every tool is always present anyway).

### 7. README

The `## What's inside` table near the top of `README.md` tracks every user-visible tool. Add a row in the same rough order as the install layers. The version column uses the `M.m.x` form, so a patch bump doesn't re-trigger a doc PR.

## Half-installed CLI

Skip the pipeline above, do only:

1. Research the tool's default state path on Linux (`~/.config/<tool>` is the modern default; legacy tools use `~/.<tool>`).
2. Add the `defaults()` entry in `internal/mountplan/defaults.go` (with `Name:` set), bump the `len(mounts) != N` count and its `Errorf` message, and add the `assertMount` line in `internal/mountplan/defaults_test.go`.
3. Add the keyring-backend ENV to the Dockerfile near the existing layer, when the tool needs one (step 6).
4. `make go-check`.

That was ~5 edits for gws and shipped clean. Keep it that size.

## CLAUDE.md gotcha

When a tool has a non-obvious quirk worth surfacing to future contributors (the gws keyring backend is the model case), add a one-line bullet to the "Gotchas" section in `CLAUDE.md`. When an in-Dockerfile comment already captures the quirk, leave it there — duplication rots faster than it helps.

## Success

Every file in your branch's row edited, the `CLAUDE.md` gotcha judged, and `make go-check` green — lint plus the catalog bijection, smoke-count and mount-count tests these edits move.

Report pre-existing lint/test failures and stop when they surface during that gate. A failure your edits didn't cause is a separate change; folding it in hides which edit broke what.

That gate proves the configuration is internally consistent, **not** that the new binary executes inside the image. Say so plainly and point the user at `make test` (build + smoke) for the end-to-end check; `docker-ci.yml` triggers on `internal/build/assets/**`, so CI runs it on this change anyway. Leave `make build` to them — multi-minute, and it rebuilds every layer below the new one.
