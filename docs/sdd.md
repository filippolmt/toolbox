# SDD — Spec-Driven-Development skill packs

Per-repo opt-in skill packs (gsd, bmad, openspec) pinned in `internal/sdd/registry.go` and bootstrapped inside the container on shell start. This file covers the `toolbox sdd` CLI, the `sdd:` config shapes, and the `.gitignore` fence.

## Supported integrations

Three skill packs are registered, each pinned to an npm package version (Renovate-bumped):

| Key | Package | Notes |
|-----|---------|-------|
| `gsd` | `@opengsd/gsd-core` | non-interactive bootstrap |
| `bmad` | `bmad-method` | requires a one-time manual `npx bmad-method install` (the `_bmad/` marker is user-authored and committed) |
| `openspec` | `@fission-ai/openspec` | non-interactive bootstrap |

## CLI usage

```bash
toolbox sdd list          # show supported integrations + pinned versions
toolbox sdd init <name>   # wire the current repo for <name>
```

`toolbox sdd list` prints the registry: each key with its pinned `package@version`.

`toolbox sdd init <name>` is host-side and idempotent. It edits up to two files:

1. `.toolbox.yaml` — the project config found by walking up from the current directory, created in the current directory when there is none. Sets `sdd.<name>: true`, preserving comments and key order via the `configedit.ApplyChecked` pipeline; re-run behaviour under [SDD install steps](#sdd-install-steps). The flag is a config key, not an SDD artefact: writing a fresh file in a subdirectory would shadow the repo's own config wholesale, since only the nearest one is ever loaded.
2. `.gitignore` in the current directory — the workspace the container mounts. Upserts the fenced block below (skipped when the skill's `GitignoreEntries` is nil).

The two can therefore land in different directories, and that is the intent: the flag governs the repo, the fence governs the directory you actually open. Both entry points — the CLI and the config editor — resolve them identically.

The actual install runs inside the container on the next `toolbox shell`, driven by the skill's `InstallSteps`.

## SDD install steps

`sdd.<key>` in `.toolbox.yaml` accepts two shapes (#317):

```yaml
sdd:
  gsd: true            # bool shorthand — registry-default InstallSteps
  gsd:                 # object form — overrides the steps wholesale
    steps:
      - ["--claude", "--global", "--config-dir", "./.claude"]
      - ["--codex", "--local"]
```

The object form implies `enabled: true` (writing it at all is the opt-in; an explicit `enabled: false` next to `steps:` disables while keeping the override around). Steps replace the registry defaults **wholesale**, not per-entry: partial merge would leave no way to drop a default step. Token rules are validated at config load (`config.ValidateSDD`): non-empty, whitespace-free, no `;` — the host→container encoding joins args with spaces and steps with `;`, and the bash bootstrap re-splits on exactly those, so a violating token would shift arg boundaries silently instead of erroring. Unknown keys with a `steps:` override are rejected loudly (bool-shorthand typos stay silently dropped as before — see `sessionplan.sddEnv`). `toolbox sdd init <key>` re-runs leave an object-form entry untouched instead of clobbering it back to `true`.

**gsd claude default is the skill-form layout, kept workspace-local.** Since opengsd#2808, gsd-core emits *only* hyphen-form suggestions (`/gsd-<cmd>`, `bin/lib/runtime-slash.cjs`: "The colon form is never emitted"), which Claude Code routes only for `skills/gsd-<cmd>/` installs. The old `--claude --local` layout wrote `commands/gsd/*.md`, routable only as colon-form `/gsd:<cmd>` — every gsd "Next Up" suggestion pointed at a command that didn't exist. The default step is therefore `--claude --global --config-dir ./.claude`: gsd's "global" (skill-form) layout, but materialised inside the workspace's `./.claude/` so nothing leaks into the shared `~/.claude` (a bare `--global` would write into the host-persisted `~/.toolbox/claude` mount, shared across every workspace) and the per-workspace sentinel semantics stay intact. Hook commands in the generated `.claude/settings.json` come out workspace-relative (`./.claude/hooks/…`) because the `--config-dir` is relative — portable across container/host.

**Sentinel fingerprint covers version AND steps.** The entrypoint sentinel (`~/.toolbox-state/sdd.<key>.<workspace-hash>.version`) stores `<version>|<steps>`, so editing a `steps:` override re-runs the bootstrap on the next shell without waiting for a Renovate version bump. Pre-fingerprint sentinels (bare version) mismatch once and trigger a single idempotent reinstall — deliberate: it migrates every existing gsd workspace off the colon-routed layout.

**Migration residue (upstream gap):** re-running the skill-form install over a legacy `--claude --local` layout cleans `.claude/commands/gsd/` but leaves the gsd hooks block in `.claude/settings.local.json` next to the new `.claude/settings.json` copy — Claude Code merges both, so gsd hooks fire twice until the stale block in `settings.local.json` is removed by hand (one-time).

## SDD `.gitignore` fence

`toolbox sdd init <name>` writes a fenced block into the `.gitignore` of the current directory — the workspace the container mounts at `/workspace`, which is where the skill's artefacts materialise. It is not moved to the git root even when the two differ: `GitignoreEntries` are anchored globs, so a root fence would not cover a subdirectory's `.claude/`.

```
# >>> sdd-managed/<name> (toolbox)
<glob 1>
<glob 2>
…
# <<< sdd-managed/<name> (toolbox)
```

The block content comes verbatim from `Skill.GitignoreEntries` in `internal/sdd/registry.go`. Patterns (not enumerated paths) are the contract: `.claude/get-shit-done/`, `.claude/skills/gsd-*/`, `.codex/agents/gsd-*` — coverage stays stable across upstream version bumps because new files shipped by a future version still land under one of the documented install roots.

Contract:

- `toolbox sdd init <name>` is host-side and idempotent. Re-running on an unchanged registry leaves the block byte-identical.
- An existing `.gitignore` keeps its non-fence lines intact (the upsert splices the block by fence markers).
- Skills whose upstream installer emits purely user-authored content (bmad) leave `GitignoreEntries` nil; the fence is skipped entirely. The host reports `skipped (skill produces user-authored content)`. openspec is the mixed case: `openspec/{specs,changes}` are user-authored and committed, while the per-tool adapter files under `.claude/skills/openspec-*`, `.claude/commands/opsx/`, `.codex/skills/openspec-*` plus the default `openspec/config.yaml` scaffold are regenerated by `openspec init --force` / `openspec update` on every shell and land in the fenced block. Drop the `openspec/config.yaml` entry per-repo once you customise the `context:` / `rules:` blocks — otherwise the scaffold stays generated and ignored.
- Disabling a skill through the config editor (`toolbox config ui` SDD toggle) removes both its `.toolbox.yaml` flag and its `.gitignore` fence, and so does resetting the whole `sdd` key with `r` while the Repo scope is selected — a fence survives that reset only if a flag in another layer still enables its skill. The TUI and the `sdd init` CLI share one write seam (`internal/configedit`) and resolve the same two paths, so they leave identical file state.
- The `sdd` key cannot be enabled in the editor's Global scope: the opt-in is anchored to the workspace, so a global flag would fence exactly one repo. See [ADR 0011](adr/0011-the-sdd-opt-in-is-anchored-to-the-workspace.md). A global flag written by hand still enables the skill everywhere and is still resettable from that scope — reset then leaves every workspace's fence alone.
- The `sdd init` CLI only enables; there is no `toolbox sdd uninstall`, so a skill disabled by hand-editing `.toolbox.yaml` leaves an orphan fence to clean up manually.
