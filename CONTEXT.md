# Context

Project-specific vocabulary used by AI tooling and code reviewers. Add a
term here when a refactor or design conversation gives a new concept its
name; the file is the single source of truth for what those concepts mean
in this codebase.

## Glossary

### Mount Plan

The full pipeline that turns a `Config` and a workspace path into the
typed bind set handed to `ContainerCreate`, plus the shell `WorkingDir`.

Concretely: `defaults() → applyMountsRoot → mergeMounts → resolveAll →
append workspace bind → append host-path mirror (when safe)`. Owned by
`internal/mountplan`. The single seam runtime callers and tests cross is
`mountplan.Plan(cfg, workspace)`; pure merge inspection (no filesystem
side-effects) is exposed as `mountplan.Merge(cfg)`.

Why the term exists: before this concept was named, the same logic was
spread across `internal/config` (defaults + merge + root retarget),
`internal/mount` (filesystem resolve), and `internal/container/lifecycle`
(workspace + DooD-mirror append). Reading any one stage missed the
others; tests stubbed each in isolation and bugs hid in the handoffs.
The "Mount Plan" name turns one fragmented walk into one deep module.

### Tool Catalog

The canonical declaration of every bundled tool: a single typed table
whose entries describe each tool's key, default state, Dockerfile
`INSTALL_*` ARG name, and (for later phases) description, init script,
and smoke-test hook.

Concretely: `Entries → Keys / BuildArg / Defaults / IsDefault /
WriteCanonical`. Owned by `internal/catalog`. Consumers are
`internal/build/tag.go` (build args + canonical-encoded image hash via
`WriteCanonical`) and `internal/config` (thin shims over `Defaults` and
`IsDefault`); the future Phase 10 init manifest reads the optional
`Description` / `InitScript` / `SmokeTest` fields. Optional fields are
excluded from the canonical hash encoding so populating them is
hash-neutral for users.

Why the term exists: before this concept was named, three parallel
hand-maintained literals described the same 30 tools — a `KnownTools`
slice and a `ToolBuildArg` map in `internal/config/tools.go`, with the
upcoming Phase 10 init manifest poised to be the third. Adding a tool
meant editing three files plus the Dockerfile install layer; missing
one site silently broke either the build args, the image hash, or
(eventually) the boot init. The "Tool Catalog" name turns three
fan-outs into one declaration with typed accessors.

### Config Plan

The full pipeline that turns the cobra `--config` flag plus the host's
`.toolbox.yaml` files into a fully-resolved, fully-validated `*Config`
handed to subcommands.

Concretely: `viper-defaults (per-call viper.New) → walk-up search from
CWD for the nearest .toolbox.yaml → file-load (global ~/.toolbox.yaml +
project + explicit --config when set) → tool-defaults from
catalog.Keys() → mount-defaults (no-op on *Config; mountplan.Plan owns
the actual mount-defaults) → validate (ValidateMountsRoot +
ValidateShell)`. Owned by `internal/config`. The single seam runtime
callers and tests cross is `config.Plan(searchFrom, explicitOverride)`;
pure merge inspection (no filesystem, byte-input only) is exposed as
`config.Merge(global, project, explicit []byte)`. Each invocation uses
a fresh `*viper.Viper` so callers see no cross-call state.

Why the term exists: before this concept was named, the same logic was
split across `cmd/root.go::initConfig` (walk-up + viper-seeding +
file-load + env-prefix) and `internal/config/Load` (unmarshal +
validate). Reading either site alone missed half the contract;
subcommand tests primed the global viper singleton to drive `Load`,
forcing `viper.Reset()` ceremony in every test body. The "Config Plan"
name turns one fragmented init flow into one deep module mirroring
the Mount Plan + Tool Catalog deepening pattern, and the per-call
`viper.New()` instance retires `viper.Reset()` from the test surface.
