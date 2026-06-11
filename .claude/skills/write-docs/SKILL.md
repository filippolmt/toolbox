---
name: write-docs
description: >-
  Write or update documentation in the toolbox repo following its layout
  contract (topic files under docs/, maintainer internals under
  docs/internals/, README as shopfront, docs/README.md as the only full
  section index). Use this whenever a change adds, renames, or removes a CLI
  command, flag, config key, default mount, bundled tool, env var, or failure
  mode; whenever the user asks "where do I document X", "document this",
  "update the docs", "update the README" (in any language), or reports the
  docs are stale or duplicated; and whenever a code PR changes behavior that any file under
  docs/ describes. Even a one-line doc edit should go through this skill —
  it encodes where each fact lives, the anchor contract, and the link-check
  gate that CI enforces.
---

# Writing documentation in this repo

The docs are organized so that every fact has exactly one home and every
section is reachable from one canonical index. A doc change that ignores this
breaks one of two CI-enforced invariants: the lychee link check
(`.github/workflows/docs.yml`) or the no-drift rule (the same fact stated in
two places will eventually contradict itself). This skill tells you where a
fact goes, how to write it, and how to verify before committing.

## Layout contract — who owns what

| File | Owns |
|------|------|
| `README.md` | Shopfront only: intro, tool table, install, quick start, disposable lifecycle, the **complete** command table (one row per public command), the **complete** config-key index (one row per key), short highlights. No long-form prose — a paragraph + link at most. |
| `docs/README.md` | The **only** full section-level map: every guide, every `##` as an anchor link. |
| `docs/commands.md` | Every CLI command, flag, subcommand; `--where`; port publishing; loopback bridge; `--oauth` presets. |
| `docs/configuration.md` | Every `.toolbox.yaml` key, loading order, `TOOLBOX_*` env vars, image selection, `env:` passthrough, inherit-host-auth. |
| `docs/mounts.md` | Credential isolation, mount merge semantics, `mounts_root`, startup hooks, mounts CLI. |
| `docs/shells.md` | Named shells, env overlays. |
| `docs/bridge.md` | Host bridge daemon (browser/editor/proximo forwarding). |
| `docs/proximo.md` | `.test` reachability + CA trust. |
| `docs/sdd.md` | SDD skill packs. |
| `docs/troubleshooting.md` | One `##` section per failure mode: symptom → cause → fix, links to the owning doc. |
| `docs/internals/*.md` | Maintainer-only: image build, container lifecycle, shell-start boot, privacy lockdown, host-CLI primitives. Never mix into user-facing files. |
| `CLAUDE.md`, `.claude/rules/*.md` | Agent pointers: one-line summary + deep link. Inline content only for what no public doc covers (code-level contracts, synced-edit checklists). |
| `CONTRIBUTING.md` | Release pipelines and PR expectations only. |

User-facing vs internal test: "does a *user* of `toolbox` need this to use a
feature?" → `docs/<topic>.md`. "Does only a *maintainer* editing this repo
need it?" → `docs/internals/`. A section that serves both gets the user half
in the topic file and the seam/mechanics half in internals, cross-linked.

## Where does this fact go? (decide before writing)

1. **New command / flag / subcommand** → section or row in
   `docs/commands.md`, plus a row in the README command table if it's a new
   top-level command. Quote semantics consistent with the cobra help text —
   never contradict `cmd/*.go`.
2. **New config key** → row in `docs/configuration.md` "Key reference" +
   its own `##` section (or a link to the owning topic file if one exists,
   e.g. a mount-related key links to `docs/mounts.md`), plus a row in the
   README config index. README row is one line; semantics live in docs/.
3. **New failure mode** → new `##` section in `docs/troubleshooting.md`
   (symptom → cause → fix), linking to the doc that owns the mechanism.
4. **New default mount / bundled tool** → `docs/mounts.md` /
   README tool table; auth persistence details follow the add-cli skill.
5. **Build/boot/privacy mechanics, design rationale, rejected
   alternatives** → the matching `docs/internals/` file.
6. **A gotcha agents must not regress** → the fact lives in docs/;
   add a one-liner + link in the matching `.claude/rules/*.md` (scoped by
   its `paths:` frontmatter) or CLAUDE.md if always-on.

Before writing anything, check the fact isn't already documented:

```sh
git grep -in '<keyword>' -- '*.md' ':!openspec'
```

If it exists elsewhere, link to it — don't restate it. If it exists in the
*wrong* place, move it (lift verbatim, fix heading levels and links) and
leave a link behind only if the old location still needs the mention.

## Invariants — what CI and reviewers will check

- **One fact, one place.** State the fact once in its owning file; every
  other mention is a link. Two copies will drift.
- **README tables stay complete.** A new public command or config key
  without its README row reintroduces the discoverability gap this layout
  was built to fix.
- **`docs/README.md` index stays in sync.** Any added/renamed/removed `##`
  heading in a guide must be reflected there. It is the only full map —
  do not create a second one anywhere.
- **Headings are an anchor contract.** GitHub slugs the heading text;
  renaming one silently breaks inbound links. Prefer stable, plain-word
  headings (punctuation is dropped from slugs and produces fragile
  anchors). If you must rename, `git grep` the old anchor and fix every
  inbound link in the same change.
- **Claims match the code.** Cross-check before stating: commands/flags vs
  `cmd/*.go`, config keys/validation vs `internal/config/config.go`,
  template vs `internal/configexample/render.go`, default mounts vs
  `internal/mountplan/defaults.go`, SDD skills vs `internal/sdd/registry.go`,
  catalog/tools vs `internal/catalog/catalog.go`. If the code and an
  existing doc disagree, fix the doc and say so in the commit message.

## Style

- Repo content is **English**, regardless of the language the request came in.
- New files open with a 1–2 line scope statement saying what the file owns.
- Explain *why*, not just *what* — these docs carry rationale ("RO would
  EROFS token refreshes"), and that's what makes them durable. Keep it.
- No CHANGELOG.md, no FAQ, no man-page-style exhaustive example listings —
  per the repo's documentation strategy. Examples earn their place by
  showing a non-obvious composition, not by enumerating flags.
- When moving existing content, **lift, don't rewrite**: preserve technical
  claims, tables, and code blocks verbatim; edit only headings, scope
  intros, and links.
- Tables for short enumerable facts; prose for reasoning. Don't bury a
  caveat inside a table cell.

## Verify before you're done

```sh
make check-links   # lychee offline: relative links + #fragment anchors
```

This mirrors the CI job exactly; red here means a red PR. If Docker can't
run in your environment (sandboxed worktree, no socket), say so explicitly
and list the links you added so the reviewer can re-run the check — never
report a doc change as verified without one of the two.

Final self-check, in order: fact in exactly one place → owning file per the
contract → index row(s) updated (`docs/README.md`, README tables if
applicable) → claims cross-checked against code → `make check-links` green.
