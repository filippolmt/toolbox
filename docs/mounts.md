# Mounts

How the container sees host paths: the isolated `~/.toolbox/` credential layout, the `mounts:` merge semantics for overriding defaults, the `mounts_root` global retarget, startup hooks, and the `toolbox mounts` CLI. Config-key reference lives in [configuration](configuration.md); the `toolbox mounts` command summary in [commands](commands.md#toolbox-mounts).

## Auth isolation under `~/.toolbox/`

Every credential path the container sees lives under `~/.toolbox/` on the host (`.claude`, `state`, `gh`, `glab`, `rtk/{config,data}`, `cf/{auth,config}`, …) or is a symlink to the host's real file (`ssh`, `gitconfig`). Canonical list in `internal/mountplan/defaults.go`, exposed as `mountplan.Defaults()`. `~/.secrets` is intentionally NOT mounted.

So by default the container never sees the real `~/.ssh`, `~/.gitconfig`, `~/.claude`, etc. directly — `gh auth login` inside the container writes to `~/.toolbox/gh/` on the host, not to the host's `~/.config/gh/`. To opt individual CLIs into the host's real credential path instead, see [inherit-host-auth](configuration.md#inherit-host-auth).

rtk and cf are the two tools whose state spans two binds because upstream splits config across non-XDG paths and exposes no env override:

- rtk: `~/.config/rtk` (config) + `~/.local/share/rtk` (analytics/tee dumps).
- cf: `~/.cf/config.toml` (OAuth tokens) + `~/.config/cf/config.json` (context defaults, completion marker).

In both cases the bind sources are nested under a single `~/.toolbox/<tool>/` root so the host layout stays flat.

## SSH host-key trust (git over SSH)

Git operations against SSH remotes (`git@github.com`, `git@gitlab.com`, …) work without re-prompting host-key verification on every call. Because the `ssh` mount is a read-only symlink to the host, the Debian default (`StrictHostKeyChecking ask` + `UserKnownHostsFile ~/.ssh/known_hosts`) would deadlock inside the container — ssh prompts on first contact but cannot persist the accepted key to the read-only `known_hosts`, so the prompt returns forever.

The image ships `/etc/ssh/ssh_config.d/10-toolbox.conf` (baked, system-wide) that sets:

- `StrictHostKeyChecking accept-new` — host keys are trusted on first contact without a prompt, while *changed* keys are still rejected (anti-MITM).
- `UserKnownHostsFile ~/.toolbox-state/known_hosts` — known_hosts lives on the writable, persistent `state` mount instead of the read-only `~/.ssh/known_hosts`, so accepted keys survive `toolbox stop` and never re-prompt on the next session.

The `ssh` and `gitconfig` mounts stay read-only by design — this trust handling deliberately avoids making them writable. Build seam in [image-build internals](internals/image-build.md). The state mount itself is the writable `state` default in `internal/mountplan/defaults.go`.

## `mounts:` merge semantics

User-declared mounts in `.toolbox.yaml` patch / replace / append / disable defaults by `name` (see `mergeMounts` in `internal/mountplan/merge.go`). The `mounts:` list is merged on top of the defaults — you only declare what changes. Each user entry is interpreted by `name`:

| Form | Behavior |
|------|----------|
| `name` matches a default, `target` omitted | **Patch**: only the fields you set override the default (typically `source`). |
| `name` matches a default, `target` set | **Replace**: the entire default entry is swapped for yours. |
| `name` not a default (or omitted) | **Append**: added after the default set. |
| `name` matches a default, `disabled: true` | **Remove**: the default is dropped from the resolved set. |

Default mount names: `claude`, `codex`, `state`, `ssh`, `gitconfig`, `gh`, `glab`, `gcloud`, `gws`, `azure`, `oci`, `kube`, `playwright-cache`, `startup.d`, `npm-global`, `go`, `docker-sock`. A patch referencing an unknown name fails `Plan()` loudly at startup so typos surface immediately.

Examples:

```yaml
mounts:
  # Retarget the gws auth dir to a custom host path.
  - name: gws
    source: /Volumes/work/creds/gws

  # Drop the Docker socket bind for a project that shouldn't see it.
  - name: docker-sock
    disabled: true

  # Add an extra project-specific mount.
  - name: project-data
    source: /opt/data
    target: /data
    readonly: true
```

Bool fields in a *patch* can flip `false → true` but not `true → false` (mapstructure can't tell "not set" from `false`). For that case, use the replace form by also setting `target`.

Validation: any entry that sets `target` (replace or anonymous append) must also set a non-empty `source`. An empty source would silently bind the current working directory, so it's rejected at startup.

### Source paths

`source` accepts (resolved by `resolveAll` against the dir from which `toolbox shell` was invoked):

- absolute paths (`/Volumes/work/creds/gws`),
- home-relative paths (`~/credentials/github` — `~` expands to the host user's home),
- CWD-relative paths (`./test`, `../shared/data`, or plain `data`) — resolved against the directory you invoked `toolbox shell` from, which is normally the project root.

CWD resolution lets per-project `.toolbox.yaml` reference paths inside the project without hardcoding absolute prefixes:

```yaml
mounts:
  - name: project-data
    source: ./fixtures
    target: /workspace/fixtures
    create_if_missing: true
```

## `mounts_root` retarget

When you want every toolbox-managed credential / state dir to live somewhere other than `~/.toolbox/` (encrypted volume, shared work drive, alternate user home), set `mounts_root` instead of patching each entry individually:

```yaml
# Move ~/.toolbox/.claude → /Volumes/work/toolbox/.claude,
# ~/.toolbox/gh → /Volumes/work/toolbox/gh, and so on for every default.
mounts_root: /Volumes/work/toolbox

mounts:
  # Per-mount patches still win — gws stays on a different drive.
  - name: gws
    source: /Volumes/secure/gws
```

Setting `mounts_root: /custom/path` rewrites every default mount whose Source starts with `~/.toolbox/` to live under the new root, applied *before* the user merge inside `mountplan.Merge`. Per-mount patches still win, so a global root + a single per-name override coexist. `docker-sock` and `SymlinkFrom` targets are not touched (they reference real host paths, not toolbox-managed mirrors).

`mounts_root` accepts absolute paths (`/Volumes/work/toolbox`) and home-relative paths (`~/work/toolbox-state`). Relative paths are rejected at startup by `config.ValidateMountsRoot`. Bare `~` is rejected too — it would rewrite `~/.toolbox/.claude` to `~/.claude`, exposing toolbox state on the real host home and defeating credential isolation. Use a sub-path (`~/toolbox-state`) or an absolute path.

**Scope: global vs per-project.** `mounts_root` follows the same precedence as every other config field:

| Where you set it | Effect |
|------------------|--------|
| `~/.toolbox.yaml` only | Applied to every `toolbox shell`, in every project. |
| `./.toolbox.yaml` only | Applied only when `toolbox shell` runs in that project directory. |
| Both | Project file replaces the global value for that project; other projects keep the global one. (Scalar field — no concatenation.) |

`mounts_root` is applied first to retarget every default; per-name `mounts:` patches are then layered on top, so a single mount can still escape the global root in any one project. Other projects keep using the global root unchanged.

**Migration note.** `mounts_root` only changes where the container *binds* its state — it does not move data. If you already have credentials and history in `~/.toolbox/` and switch to a new root, the new directories are auto-created empty (per default `create_if_missing: true`) and the original `~/.toolbox/` data is left untouched. Move it yourself before the next `toolbox shell` if you want continuity:

```bash
# Stop any running toolbox, then carry your state over.
toolbox stop
mv ~/.toolbox /Volumes/work/toolbox
```

## Startup hooks

Drop any `*.sh` file into `~/.toolbox/startup.d/` on the host and it will be executed by the entrypoint on every `toolbox shell`, before your zsh prompt. Use this for per-user bootstrap that should not live in the image (installing Claude Code skill packs, `direnv` shims, custom env bootstrap, etc.). Hooks run as the toolbox user, share the mounted credentials, and can write to the per-user npm prefix at `~/.toolbox/npm-global/` without needing root.

See [`examples/startup.d/`](../examples/startup.d/) for a ready-to-copy example that installs a single-binary npm CLI on first shell.

### Per-repo startup hooks

When bootstrap logic should live with the project (e.g. installing a one-off lint tool, seeding a fixture DB) instead of in your global `~/.toolbox/startup.d/`, patch the default `startup.d` mount in the repo's `.toolbox.yaml`:

```yaml
mounts:
  - name: startup.d
    source: ./startup.d
```

Then drop hook scripts into `./startup.d/*.sh` at the repo root. The patch retargets the `startup.d` mount by name, so the default `create_if_missing: true` and read-only flags are kept; only the host source changes. Because the source is CWD-relative, it resolves to the repo root when you run `toolbox shell` there, and the hooks only fire for that project. Run `toolbox stop` once after editing `.toolbox.yaml` so the new mount source takes effect ([bindings are fixed at container creation](commands.md#publishing-ports)).

Trade-off vs. the global directory: per-repo hooks let a repo's `.toolbox.yaml` (and any contributor with push access) run code inside your container with your mounted credentials — treat them like any other repo-shipped automation. Commit the `startup.d/` directory if the hooks should be shared, or add it to `.gitignore` for per-user setup.

## mounts CLI

`toolbox mounts` edits the `mounts:` list / `mounts_root:` key without hand-editing YAML, mapping 1:1 onto the merge semantics above — no new schema shapes:

- `list [--defaults-only]` — effective view from `mountplan.Merge(cfg)`, each row classified against `mountplan.Defaults()` by name: `(default)` / `(patched)` / `(disabled)` / `(user)`. Disabled defaults are listed too (the resolved set drops them).
- `add <name> --source --target [--readonly]` — writes the replace/append form; a name matching a default replaces it wholesale.
- `disable <name>` — writes the `{name, disabled: true}` patch; the name is validated against defaults + user entries first, because a patch referencing an unknown name fails the next `Plan()` load.
- `remove <name>` — deletes a **user-list entry only**. Defaults are not stored in the file, so a default-only name gets an explanatory error pointing at `disable` instead.
- `root <path>` — pre-validates with `config.ValidateMountsRoot`, then writes `mounts_root:`.

All writers go through the comment-preserving `configio.UpsertFile` pipeline (via `internal/configedit`) and accept `--where global|local` (see [--where targeting](commands.md#--where-targeting)). Unknown names get Levenshtein "did you mean" suggestions.
