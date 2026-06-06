---
paths:
  - "internal/config/**"
  - "internal/configio/**"
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
- **Config load order** (highest first): `--config` → walked-up `.toolbox.yaml` → `~/.toolbox.yaml` → `TOOLBOX_*` env → defaults. Source of truth: `config.Plan` in `internal/config/plan.go`. Legacy `tools:` block → one-time stderr warning + ignore.
