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

`toolbox sdd init <name>` is host-side and idempotent. It edits up to two files in the current repo:

1. `.toolbox.yaml` — sets `sdd.<name>: true` (creating the file if missing, preserving comments and key order via the `configio.UpsertFile` pipeline; re-run behaviour under [SDD install steps](#sdd-install-steps)).
2. `.gitignore` — upserts the fenced block below (skipped when the skill's `GitignoreEntries` is nil).

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

`toolbox sdd init <name>` writes a fenced block into the workspace `.gitignore`:

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
- Disabling a skill (removing the `.toolbox.yaml` flag) leaves the orphaned fence block in `.gitignore`. There is no `toolbox sdd uninstall` today — clean it up manually.
