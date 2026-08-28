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
## npm-global shadow gotcha

### The shadow

the [`npm-global` mount](../../docs/mounts.md) is the global npm prefix and `PATH` puts `~/.npm-global/bin` ahead of `/usr/local/bin`, so a volume seeded pre-baking can shadow image-pinned `/usr/local` tools with stale duplicates (observed: runtime `pyright 1.1.409` vs image `1.1.410`).

### The healer

`init.d/15-npm-lsp-dedupe.sh` heals this on every start by removing ONLY the pinned LSP set (`pyright`, `typescript`, `typescript-language-server`) from the volume when present — idempotent, offline, non-fatal. Scoped to those three: `@anthropic-ai/claude-code` / `@openai/codex` are baked too but self-update into the volume and MUST keep winning, so `PATH` is intentionally NOT reordered. Registered in `systemInitScripts` (`internal/catalog/init_d_bijection_test.go`), counted in the smoke-test init.d literals.

### Why only npm-global gets one

The same PATH-ahead-of-`/usr/local/bin` shape now covers four persisted bin dirs — `npm-global`, plus `~/go/bin`, `$PNPM_HOME/bin` and uv's `UV_TOOL_BIN_DIR` — and **only npm-global gets a healer**. That asymmetry is deliberate: the pyright drift came from an *involuntary* install (a transitive dep re-seeded the volume), whereas `go install golang.org/x/tools/gopls@latest` over the baked `gopls`/`goimports` is an explicit act by someone who wants that version, so it must win. Don't append the new dirs after `/usr/local/bin` — that would make their mounts pointless — and don't grow `15-npm-lsp-dedupe.sh` to cover them until an involuntary shadow is actually observed.

### Where the ENV entries are declared

All four entries are declared together in the Dockerfile below the RUN tail, beside the npm-global one, because an `ENV` above the tail lands in every following RUN's cache key and trips the Invalidation Floor gate (measured: 12 substantial layers moved). → [Invalidation Floor](../../CONTEXT.md#invalidation-floor)

- **`inherit_host_auth: [<key>, …]`**: opt CLI into reading host credential path (RW — token refreshes need writes) instead of isolated `~/.toolbox/<key>/`. Whitelist on `catalog.Entry.HostAuthMount`. Default `[]` keeps full isolation. → [inherit-host-auth](../../docs/configuration.md#inherit-host-auth)
