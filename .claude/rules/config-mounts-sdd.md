---
paths:
  - "internal/config/**"
  - "internal/configio/**"
  - "internal/configedit/**"
  - "internal/mountplan/**"
  - "internal/sdd/**"
  # The inherit_host_auth whitelist (catalog.Entry.HostAuthMount) lives in
  # the catalog — edits there must see the auth-isolation gotchas:
  - "internal/catalog/**"
---

# Config / mounts / SDD gotchas — backstory in [`docs/runtime-notes.md`](../../docs/runtime-notes.md)

- **Auth isolation**: every credential under `~/.toolbox/` (canonical list `mountplan.Defaults()`); `~/.secrets` NOT mounted. `mounts:` patches/replaces/appends/disables defaults by `name`; `mounts_root` retargets pre-merge. → [auth-isolation](../../docs/runtime-notes.md#auth-isolation-under-toolbox), [mounts](../../docs/runtime-notes.md#mounts--auth-isolation)
- **`inherit_host_auth: [<key>, …]`**: opt CLI into reading host credential path (RO) instead of isolated `~/.toolbox/<key>/`. Whitelist on `catalog.Entry.HostAuthMount`. Default `[]` keeps full isolation. → [inherit-host-auth](../../docs/runtime-notes.md#inherit-host-auth)
- **SDD `.gitignore` fence**: `toolbox sdd init <key>` writes a fenced block under `# >>> sdd-managed/<key> (toolbox)` from `Skill.GitignoreEntries` globs (patterns, not enumerated paths — survive upstream bumps). Nil entries → fence skipped. → [sdd-gitignore](../../docs/runtime-notes.md#sdd-gitignore-fence)
- **SDD steps override + gsd skill-form**: `sdd.<key>` accepts bool shorthand OR `{steps: [[…]]}` (replaces registry `InstallSteps` wholesale; tokens validated — no whitespace/`;`). gsd claude default is `--claude --global --config-dir ./.claude` (skill-form, hyphen-routable `/gsd-<cmd>`; the old `--claude --local` colon layout broke every gsd suggestion). Sentinel stores `version|steps` so a steps edit re-runs the bootstrap without a version bump. → [sdd-install-steps](../../docs/runtime-notes.md#sdd-install-steps)
- **Config load order** (highest first): `--config` → walked-up `.toolbox.yaml` → `~/.toolbox.yaml` → `TOOLBOX_*` env → defaults. Source of truth: `config.Plan` in `internal/config/plan.go` (`Plan ≡ Merge(LoadLayers(...))` — regression-tested; don't fork the walk-up, use `config.WalkUpProjectConfig`). Legacy `tools:` block → one-time stderr warning + ignore.
- **Config CLI editors** (`toolbox shells|mounts|config …`): semantic layer in cobra-free `internal/configedit` (`--where` resolution, header-on-create, writers, provenance, doctor, Levenshtein suggestions); `configio` stays a dependency-light leaf (must NOT import `internal/config`). `--where global|local` (default `global`): local patches the **walked-up** file in place; `./.toolbox.yaml` created only when walk-up finds nothing. `mounts` writers emit only shapes `mergeMounts` reads — `disable` validates the name first (unknown patch name breaks the next load), defaults can only be disabled, never removed. `config show` default output is byte-for-byte frozen (golden test) — annotations only behind `--origin`. → [--where targeting](../../docs/runtime-notes.md#--where-targeting), [config provenance & doctor](../../docs/runtime-notes.md#config-provenance--doctor), [mounts CLI](../../docs/runtime-notes.md#mounts-cli)
- **`env:` passthrough**: arbitrary K=V map injected into the container shell; top-level (global/project) + per-shell `shells.<name>.env` overlay (per-shell wins, `config.EffectiveEnv` keyed by raw shell name). Reserved keys (`TOOLBOX_` prefix + `PWD`) rejected by `config.ValidateEnv`; emitted after curated entries, sorted, by `sessionplan.userEnv`. Hash-neutral. → [env-passthrough](../../docs/runtime-notes.md#env-passthrough)
