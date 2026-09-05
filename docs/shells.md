# Named shells

Multiple independent workspaces from one CLI: the `shells:` config block maps a short name to a directory (plus an optional env overlay), and `toolbox shell <name>` / `toolbox stop <name>` target that workspace's container. Each workspace gets its own container; they run side by side and share the persistent `~/.toolbox/` state.

## Targeting: `toolbox shell [name|dir]`

`toolbox shell`'s optional argument (and `toolbox stop`'s, symmetrically) resolves as:

- **no argument** — the current working directory (the normal per-project flow).
- **`<name>`** (non-absolute string) — looked up in the `shells:` map by raw config key; the shell opens in that entry's `path`.
- **`<abs-dir>`** (absolute path) — a one-shot session on that directory, no config read or written.

`toolbox stop <name|dir>` stops the matching container; `toolbox stop --all` stops every toolbox container on the host (no positional argument allowed).

## The `shells:` block

```yaml
shells:
  infra:
    path: /Users/me/work/infra     # required, absolute
    env:                           # optional per-shell env overlay
      AWS_PROFILE: prod
  scratch:
    path: /tmp/scratch
```

`shells.<name>.path` must be absolute (`toolbox config doctor` flags an empty path as an error and a missing directory as a warning — it may be created by `--create`).

## Env overlays

The per-shell `env:` map overlays the top-level [`env:` passthrough](configuration.md#env-passthrough): the top-level map is the base, per-shell keys win on collision (`config.Config.EffectiveEnv`; the `shells:` name is matched case-insensitively and space-trimmed, via `config.NormalizeShellKey`). Reserved-key rules and emission order live with the `env:` contract in configuration.md.

## Managing entries: `toolbox shells`

| Subcommand | Description |
|------------|-------------|
| `list` | List all named shells and their paths. |
| `get <name>` | Show the resolved entry (path + env overlay). |
| `add <name> --path <dir> [--create-dir] [--env K=V …]` | Add or replace a named shell. |
| `set <name> --env K=V […]` | Set/update env overlay keys on an existing entry. |
| `remove <name> [--purge-dir]` | Delete the entry (`--purge-dir` also deletes the configured directory). |

All writers accept [`--where global|local`](commands.md#--where-targeting) (default `global` — named shells are naturally per-user) and preserve comments in the touched YAML file. They also accept [`--dry-run`](commands.md#dry-runs), which prints the file the write would produce and touches nothing — `--create-dir` and `--purge-dir` are then named on stderr instead of performed.

Shell names are matched the way `toolbox shell <name>` matches them: case-insensitively and space-trimmed. A new entry is written under the canonical (lowercase) key, and an entry your file already spells differently is edited in place — so an edit never leaves two `shells:` keys that collapse into one when the config loads.

## Bootstrap shorthand: `shell --create`

`toolbox shell <name> --create` auto-bootstraps a missing named shell in `~/.toolbox.yaml`: it writes the entry and opens it in one step. `--path <dir>` picks the directory; the default is `$HOME/toolbox-shells/<name>` (or `/tmp/<name>` if the home directory is unresolvable), where `<name>` is the canonical key — so two spellings of one shell bootstrap to the same directory.
