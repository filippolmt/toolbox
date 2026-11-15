# Spec-Driven-Development skill packs are removed; `sdd:` is a retired key

Status: accepted — supersedes [ADR 0011](0011-the-sdd-opt-in-is-anchored-to-the-workspace.md)

The question comes from
[#1463](https://github.com/filippolmt/toolbox/issues/1463). Toolbox could
install three repo-local Spec-Driven-Development skill packs (`gsd`, `bmad`,
`openspec`) on every `toolbox shell`, opted in per workspace with
`sdd.<key>: true`. The feature had grown a registry, a `toolbox sdd` command,
a config key with its own validator and decode hook, a `config ui` editor with
a workspace-only write guard, a session env contract, an entrypoint install
loop, a `.gitignore` fence writer and three Renovate managers. Nobody used it
any more, and it taxed every config-schema change and every Renovate bump.

## Decision

Remove the feature end to end. `sdd:` becomes a
[Retired Key](../../CONTEXT.md#retired-key): a config that still carries it
keeps loading, and the key is named once per process at load and as a
`config doctor` finding, pointing at
[retired keys](../configuration.md#retired-keys).

Toolbox does not delete what a pack already installed in a repository
(`.claude/skills/…`, `_bmad/`, `openspec/`, the `sdd-managed` `.gitignore`
fences): those are the developer's files, and removing them is the
developer's call.

## Considered options

- **Ignore `sdd:` silently.** Rejected: a workspace that relied on the packs
  would stop being upgraded with nothing saying why.
- **Reject `sdd:` as a hard error.** Rejected: it would block every shell in a
  workspace for a key that no longer does anything.
- **Clean up installed artefacts.** Rejected: toolbox would delete files in a
  developer's repository on its own initiative, and some of them are
  committed.

## Consequences

- The workspace-only key guard (ADR 0011) had `sdd` as its only member and is
  gone with it; every remaining key can be written to either Config Scope side.
- A workspace that still has a pack installed keeps the version it has; it is
  no longer upgraded.
