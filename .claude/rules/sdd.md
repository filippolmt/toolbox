---
paths:
  - "internal/sdd/**"
  - "cmd/sdd.go"
  # The .gitignore fence is written through the configedit SDD seam:
  - "internal/configedit/**"
---

# SDD gotchas — backstory in [`docs/sdd.md`](../../docs/sdd.md)

- **SDD `.gitignore` fence**: `toolbox sdd init <key>` writes a fenced block under `# >>> sdd-managed/<key> (toolbox)` from `Skill.GitignoreEntries` globs (patterns, not enumerated paths — survive upstream bumps). Nil entries → fence skipped. Write logic lives in the `internal/configedit` SDD seam (`SetSDDEnabled` yaml flag + `Write`/`RemoveSDDGitignore` fence, `EnableSDD`/`ReconcileSDDGitignore` compose them; `configio.RemoveFence` is the delete counterpart to `SpliceFence`) — both `cmd/sdd.go` (CLI, enable-only) and `configui.SaveSDD` (TUI, enable+disable, Doctor-gated yaml then post-commit fence loop) route through it, so the two paths produce identical `.toolbox.yaml` + `.gitignore` state. → [sdd-gitignore](../../docs/sdd.md#sdd-gitignore-fence)
- **SDD steps override + gsd skill-form**: `sdd.<key>` accepts bool shorthand OR `{steps: [[…]]}` (replaces registry `InstallSteps` wholesale; tokens validated — no whitespace/`;`). gsd claude default is `--claude --global --config-dir ./.claude` (skill-form, hyphen-routable `/gsd-<cmd>`; the old `--claude --local` colon layout broke every gsd suggestion). Sentinel stores `version|steps` so a steps edit re-runs the bootstrap without a version bump. → [sdd-install-steps](../../docs/sdd.md#sdd-install-steps)
