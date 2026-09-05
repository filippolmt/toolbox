---
paths:
  - "internal/config/**"
  - "internal/configio/**"
  - "internal/configedit/**"
  - "internal/configrender/**"
  - "internal/configui/**"
  - "internal/configexample/**"
  # fsx holds the primitives configio callers import directly — an edit there
  # is a config-write concern.
  - "internal/fsx/**"
  # The config/shells/mounts/worktree CLI surfaces write through the
  # configedit seam documented here. cmd/** as a whole belongs to
  # container-runtime.md, so these files are named one by one:
  - "cmd/config.go"
  - "cmd/configwrite.go"
  - "cmd/shells.go"
  - "cmd/mounts.go"
  - "cmd/worktree.go"
  # The --create bootstrap writes a config file too, through the one
  # exemption the writer-lane guard allows.
  - "cmd/shell_named.go"
---

# Config gotchas — backstory in [`docs/configuration.md`](../../docs/configuration.md) and [`docs/commands.md`](../../docs/commands.md)

- **Config load order** (highest first): `--config` → walked-up `.toolbox.yaml` → `~/.toolbox.yaml` → defaults; the env-bound keys (`config.EnvBoundKeys`) additionally take `TOOLBOX_*` env *above all file layers* (viper `AutomaticEnv` runs after `MergeConfig`, so env wins — `TestMergeImageSelectionEnvOverride`). Source of truth: `config.Plan` in `internal/config/plan.go` (`Plan ≡ Merge(LoadLayers(...))` — regression-tested; don't fork the walk-up, use `config.WalkUpProjectConfig`). Legacy `tools:` block → one-time stderr warning + ignore.
## Config CLI editors (`toolbox shells|mounts|config …`)

### Package boundaries

semantic layer in cobra-free `internal/configedit` (`--where` resolution, header-on-create, the Pending Mutation constructors and the one write that applies them, provenance, doctor, Levenshtein suggestions); `configio` stays a dependency-light leaf (must NOT import `internal/config`); the host-filesystem primitives live one layer below, in `internal/fsx`. Never reimplement, fork **or re-export** an `fsx` primitive here — a caller that needs one imports `fsx` directly (a fork would have to restate what `TestAtomicWriteFileLeavesNoTemp` already holds; a re-export is caught by `TestNoPackageReExportsAnFsxPrimitive`, whose own classifier is pinned by `TestForwardedPrimitiveTellsAnAliasFromACaller` so it cannot go green by seeing nothing). The leaf constraint is about what `configio` may *import*, not about giving config callers a second name for a primitive. → [shared-fs-primitives](../../docs/internals/host-cli.md#shared-fs-primitives)

### `--where` targeting

`--where global|local` (default `global`): local patches the **walked-up** file in place; `./.toolbox.yaml` created only when walk-up finds nothing.

### `mounts` writers

`mounts` writers emit only shapes `mergeMounts` reads — `disable` validates the name first (unknown patch name breaks the next load), defaults can only be disabled, never removed. The disable shape itself is written in exactly one place, `disableMountIn`, shared by the single-name `MountDisabled` and the reconciling `MountsDisabled`.

### Rendering

`config show`'s resolved-YAML renderer lives in `internal/configrender` (`Resolved`/`ResolvedWithOrigin`, peer of `internal/configexample` which renders the annotated *template*); `cmd/config.go` is flag parsing + dispatch only. Default output is byte-for-byte frozen (golden test) — annotations only behind `--origin`. Scalar fallbacks (the effective value of an unset `shell`/`agent`/`pull`) come from the one `config.EffectiveValue(cfg, key)` seam — shared by the renderer, `configui.displayValue`, and `cmd/worktree.go`'s agent resolution so they can't drift; a parity test guards it, and its coverage guard forces every scalar `SchemaKeys()` key to be owned or explicitly exempt.

### One validated write seam

**One validated write seam**: `configedit.ApplyChecked(target, cwd, Mutator)` is the ONLY way any surface writes a config file — it renders the candidate in memory, validates it through the doctor in the layer `target` occupies, and writes only if that passes (so no transient invalid file exists and there is nothing to roll back). `configio.UpsertFile` was deleted for exactly this reason: an unvalidated write lane in the leaf package. Every `cmd` writer surface, `EnableSDD`, `configui`'s save and the `shell --create` bootstrap all route through it, each taking `cwd`. An error always means nothing was written, so `reportWrite` must not run.

**One vocabulary, and the `cmd` edge applies it**: a writer command is a named [Pending Mutation](../../CONTEXT.md#pending-mutation) constructor plus one `applyOrPreview` call — never a typed writer wrapping it. Two guardrails ride on that: every write path stays renderable through `configedit.Render` (`TestEveryWriterMutationRendersWithoutTouchingDisk`), and a command whose halves must land together composes them into **one** mutation, never two write calls — `configedit.Shell` is that case (`TestShellWritesPathAndEnvAsOneMutation`).

**`Preview` is the write minus its last line**: `configedit.Preview(target, cwd, Mutator)` shares one body with `ApplyChecked` (`checkedCandidate`) — same read, same rendering, same doctor verdict, same no-op short-circuit — and returns the candidate instead of writing it. Never build a second rendering to preview an edit: that is a claim about the write rather than the write itself, the exact drift the collapse removed. `TestPreviewReturnsTheCandidateWithoutTouchingTheFile`, `TestPreviewIsGatedLikeTheWrite`, `TestPreviewOfANoOpRendersTheFileItFound`.

### The writer-command edge (`--where` + `--dry-run`)

`cmd/configwrite.go` owns it: `registerWriteFlags` registers both flags in one call, and `applyOrPreview` is the ONLY lane a writer command reaches `ApplyChecked`/`Preview` through. Two guards, because each passes on the other's failure — `TestEveryConfigWriterCommandOffersDryRun` (a `--where` carrier without `--dry-run`: a writer wired by hand) and `TestConfigWritesGoThroughTheDryRunLane` (a `--dry-run` nothing reads). The lane guard's one exemption is `cmd/shell_named.go`'s `--create` bootstrap: a side effect of entering a shell, not a writer command with a flag surface. A host-side effect a dry run could not take back (`shells add --create-dir`, `shells remove --purge-dir`) is named on stderr, never performed — stdout carries the candidate document alone, so a dry run pipes (`TestDryRunPrintsTheCandidateAndWritesNothing`, `TestDryRunReportsTheRejectionTheWriteWould`, `TestDryRunSkipsHostSideEffects`). Why it came for free: [Pending Mutation](../../CONTEXT.md#pending-mutation).

**An empty scalar removes the key**, never writes it empty (`configedit.Scalar`) — the reset behind `toolbox config set --image ""` and `toolbox mounts root ""` (`TestConfigSetEmptyImageRemovesKey`, `TestMountsRootEmptyValueRemovesTheKey`). → [resetting a scalar](../../docs/commands.md#--where-targeting)

**`existed` comes from the write, never from a stat**: `ApplyChecked` returns `(changed, existed, err)`, `existed` being what its own read found, and `reportWrite`'s created-vs-updated line is drawn from it. A `cmd` site that stats the target itself is asking a question the write already answered, through a window in which the two answers can differ (`TestApplyCheckedReportsWhetherTheFileExisted`).

### What the gate judges

The gate answers "does *this file* introduce a fault", never "is the merged config flawless": the candidate is linted **on top of the other layer** (the target's own layer is dropped — global iff `target == ~/.toolbox.yaml`, else project; `--config` excluded on purpose) and whatever that other layer says **on its own is subtracted**. On top so a value the file gets wrong is caught even when a higher layer masks it today; over the other layer so a file declaring only part of an entity passes (`shells set <n> --env … --where local` for a globally-defined shell); minus the other layer so a project file's error cannot block `config set --where global`, which the user cannot fix from there. `lintStack(lower, higher)`'s arguments are precedence positions, NOT provenance — that is what lets a candidate be judged unmasked; the explicit slot cannot serve, `Merge` documents it as short-circuiting the other two. Rejections are prefixed with the target path, because the finding names the key and not the file it lives in.

### `config edit`, the one write outside the gate

`config edit` is the one write outside the gate — `$EDITOR` writes the file directly — so it reports findings to stderr and exits non-zero afterwards, never reverting hand-written work. Only error-severity findings gate; warnings stay with `toolbox config doctor`.

### Flag-value validation

Fail-fast on a bad flag value goes through the one `config.ValidateKey(key, value)` rule (keeps the usage-error exit code), never a `cmd`-private validator table or a direct `config.Validate*` call from a write path; a guard test forces every scalar `fieldValidators` key to stay reachable through it.

### `config ui`

`internal/configui` (bubbletea) is a thin presentation layer only — it resolves via `config.Plan`/`configedit.Compute` and writes through `configedit.ApplyChecked`. Config domain logic stays out, and the tea layer stays thin over the pure adapter funcs, which hold the testable resolve/validate/write logic.

The two values it is built on are glossary terms — meaning and rationale in [Pending Mutation](../../CONTEXT.md#pending-mutation) and [Key Descriptor](../../CONTEXT.md#key-descriptor). What an edit here must hold:

- One model of a pending edit: `Model.pendingMutator` hands the *same* `configedit.Mutator` to the preview (`previewDiff` → `configedit.Render`) and to the save (`configedit.ApplyChecked`). `TestPreviewMatchesWriterForEveryEditableKey` is the net, and a new editable key adds a case there.
- Both render sides go through `configedit.Render`, never `configio.RenderDocument` directly — the header-on-create policy lives in `configedit.headerAware`, and a preview that skipped it under-reported a file creation by whole lines.
- Adding a key is one `configui.keyDescriptors` row (`descriptor.go`) plus that test case, never a new switch; `TestKeyDescriptorsCoverEveryKey` fails on a missing row instead of letting it surface as a blank TUI line.
- The per-key facts that are NOT presentation are asked of their owner: `configedit.PerEntryKey` (which keys are attributed per entry) and `config.DeprecatedAliases` (which deprecated alias folds into which live key — the same fold `fillDefaultsBackstop` performs, guarded by `TestDeprecatedAliasesAreFoldedByTheLoadPath`).
- Test placement follows the files, which say what they hold: a new mutator's semantics go in `configedit/mutate_test.go` (pure, on bytes — that is where a CLI writer's node work belongs too, now that it is a mutator); `configedit/apply_test.go` owns the write pipeline (comment preservation, document bootstrap, the byte-equal short-circuit, the returned `existed` bit, rejection-without-writing, layer placement); `configedit/read_test.go` the single-file readers; `configui`'s own writer tests cover only that its saves reach that seam — with one carve-out: the reset's per-key artefact reconciliation (today the SDD `.gitignore` fences) is a decision only `configui` makes, so its scope asymmetry is asserted there, on disk, in `model_test.go`.

→ [--where targeting](../../docs/commands.md#--where-targeting), [dry runs](../../docs/commands.md#dry-runs), [config provenance & doctor](../../docs/commands.md#config-provenance--doctor), [config ui](../../docs/commands.md#config-ui), [mounts CLI](../../docs/mounts.md#mounts-cli)

- **Worktree seeding**: `create`/`open` copy a curated allowlist of gitignored per-repo state into the new worktree (`cmd.seedWorktreeFiles`): defaults `.claude/settings.local.json`, `.env`/`.env.*`, `openspec/`, `.planning/`, plus `worktree.seed` config extras. Gate is `git check-ignore` — copy ONLY paths git actually ignores (tracked = already in checkout; non-ignored untracked = left alone). Allowlist-based, NOT "copy all ignored files" (would pull `node_modules/`, `graphify-out/`, `dist/`). Non-clobber, best-effort. Symlinks recreated (not dereferenced — external target not materialised); a wholly-ignored dir seeded without per-file gating; on a real `check-ignore` error → fallback to `settings.local.json` only. `-z` (NUL) so non-ASCII names survive. `ValidateWorktreeSeed` rejects abs/`..`/empty. → [worktree](../../docs/configuration.md#worktree), [worktree seeding](../../docs/commands.md#toolbox-worktree)
- **`env:` passthrough**: arbitrary K=V map injected into the container shell; top-level (global/project) + per-shell `shells.<name>.env` overlay (per-shell wins, `config.EffectiveEnv`; `cfg.Shells` is keyed by viper's lowercased name, so every lookup — `EffectiveEnv`, `cmd.shellPathFor`, `shells get|set` — goes through `config.NormalizeShellKey` = trim+lower, and every writer — `shells add|set|remove`, the `--create` bootstrap, and `config ui`'s `configedit.Shells` — through `configedit.ShellKeyIn` (sole owner: writes the canonical key for a new entry, edits an existing differently-spelled one in place). Matching literally is what made the reconciling `Shells` mutator drop a file's `Infra:` as unwanted and re-create it, taking its `env:` block with it). The overlay is applied by `sessionplan.composeEnv` from the raw `PlanInput.Name`, NOT by `cmd` pre-mixing into `cfg.Env` — the planner reads the config, never rewrites it. Reserved keys (`TOOLBOX_` prefix + `PWD`) rejected by `config.ValidateEnv`; emitted after curated entries, sorted, by `sessionplan.userEnv`. Hash-neutral. → [env-passthrough](../../docs/configuration.md#env-passthrough)
