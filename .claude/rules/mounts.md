---
paths:
  - "internal/mountplan/**"
  # The inherit_host_auth whitelist (catalog.Entry.HostAuthMount) lives in
  # the catalog — edits there must see the auth-isolation gotchas:
  - "internal/catalog/**"
---

# Mount gotchas — backstory in [`docs/mounts.md`](../../docs/mounts.md)

- **Auth isolation**: every credential under `~/.toolbox/` (canonical list `mountplan.Defaults()`); `~/.secrets` NOT mounted. `mounts:` patches/replaces/appends/disables defaults by `name`; `mounts_root` retargets pre-merge. → [auth-isolation](../../docs/mounts.md#auth-isolation-under-toolbox), [mounts](../../docs/mounts.md)
- **Profiles** (`toolbox shell --profile <name>`): `mountplan.Profile{Name, Share}` (nil = default root) threaded through `PlanInput.Profile` → `mountplan.Merge/Plan`. `Profile.Root()` = `~/.toolbox/profiles/<name>` and wins over config `mounts_root` for the invocation (resolved in `Merge`, no `cfg` mutation). Folded into the container-name hash via `ContainerNameFor(ws, ProfileName(p))` so it gets its own container. `--share <tool,…>` = skip-set on `applyMountsRoot` (`matchesShareToken`, prefix-matched; shared by `shareCovers`+`validateShare`, typos rejected). `Profile.EffectiveShare()` always appends `bridge` — retargeting the bridge daemon dir breaks in-container forwarding; ssh/gitconfig stay host-shared for free (SymlinkFrom points at host regardless of root) and are non-shareable. → [profiles](../../docs/mounts.md#profiles), [profiles usage](../../docs/commands.md#profiles)
- **Skip warnings**: `resolveAll` is the only place that prefixes a warning with `mount skipped: `, because only it knows whether the mount survived — the printer shows `plan.Warnings` verbatim. Messages must not say it themselves (that printed the phrase twice), and a mount that binds while warning must not get it. `TestResolveAllAnnouncesSkipExactlyOnce` + `TestResolveAllDoesNotCallBoundMountSkipped` guard both halves.

## npm-global shadow gotcha

### The shadow

the [`npm-global` mount](../../docs/mounts.md) is the global npm prefix and `PATH` puts `~/.npm-global/bin` ahead of `/usr/local/bin`, so a volume copy — seeded pre-baking, by a transitive dep, or by a hand `npm i -g` — shadows the image-pinned `/usr/local` one and the Renovate bump never reaches the user. Observed with `pyright`, then again with `pi` and `codegraph`.

### The healer

`init.d/15-npm-shadow-dedupe.sh` heals this on every start: for every package that owns a bin symlink under `~/.npm-global/bin` and that the image also bakes, it `npm rm -g`s the volume copy unless that copy is **strictly newer** than the baked one. Two scoping rules are load-bearing. The bin symlink is the candidate test, never the directory listing: npm flattens a package's dependencies into that same tree and links bins only for what it was asked to install, so a hoisted dep has no link — and dropping one because the image bakes the name breaks its dependent. A global prefix carries no manifest to ask instead (`npm ls -g --depth=0` just enumerates the directory), and a package with no link cannot shadow anything on `PATH` anyway. And the comparison runs on the version with `-` translated to `~`, because `sort -V` otherwise ranks `2.0.0-beta.1` above `2.0.0` and a stale prerelease would shadow the release it led to, on exactly the agents that publish prereleases here — idempotent, offline, non-fatal. The rule is directional on the version and **never a name list**, because `@anthropic-ai/claude-code`, `@openai/codex` and `@earendil-works/pi-coding-agent` are baked *and* self-update into the volume: a list wide enough to catch their stale copies would roll back a genuine self-update on every shell start, and that is also why `PATH` is intentionally NOT reordered. An unreadable version on either side keeps the volume copy — removing on a failed probe is the one outcome that loses work. Held by `TestNpmShadowDedupeDropsOnlyTheCopiesTheImageOvertook` + `TestNpmShadowDedupeKeepsACopyItCannotCompare`, which run the embedded script — they live with the asset, under [`image-build.md`](image-build.md). Registered in `systemInitScripts` (`internal/catalog/init_d_bijection_test.go`), counted in the smoke-test init.d literals.

### Why only npm-global gets one

The same PATH-ahead-of-`/usr/local/bin` shape now covers four persisted bin dirs — `npm-global`, plus `~/go/bin`, `$PNPM_HOME/bin` and uv's `UV_TOOL_BIN_DIR` — and **only npm-global gets a healer**. That asymmetry is deliberate: an npm shadow is what a stale volume or a transitive dep produces by itself, whereas `go install golang.org/x/tools/gopls@latest` over the baked `gopls`/`goimports` is an explicit act by someone who wants that version. The version-directional rule respects both — an explicit newer install keeps winning, in either ecosystem — so the healer stays npm-only until a shadow is actually observed elsewhere. Don't append the new dirs after `/usr/local/bin` either: that would make their mounts pointless.

### Where the ENV entries are declared

All four entries are declared together in the Dockerfile below the RUN tail, beside the npm-global one, because an `ENV` above the tail lands in every following RUN's cache key and trips the Invalidation Floor gate (measured: 12 substantial layers moved). → [Invalidation Floor](../../CONTEXT.md#invalidation-floor)

- **`inherit_host_auth: [<key>, …]`**: opt CLI into reading host credential path (RW — token refreshes need writes) instead of isolated `~/.toolbox/<key>/`. Whitelist on `catalog.Entry.HostAuthMount`. Default `[]` keeps full isolation. → [inherit-host-auth](../../docs/configuration.md#inherit-host-auth)
