---
paths:
  - ".claude/rules/**"
  - "internal/devrules/**"
---

# The rules about the rules — the guardrails these files are held to

A rule file is loaded by its `paths:` frontmatter and by nothing else, so a
guardrail scoped to the wrong globs is a guardrail that never fires. The tests
in `internal/devrules` hold the frontmatter to the prose; they are
repo-hygiene tests, not product code, and they ride `go test ./...` so a
directory rename cannot quietly orphan a rule.

- **rule → disk**: every glob still points at something that exists
  (`TestRulePathsResolve`). Catches a rename or deletion leaving a dead glob.
- **body → frontmatter**: every package a rule *names* is scoped by that same
  rule's `paths:`, or handed to the rule that does (`TestRuleMentionsAreCovered`).
  Naming a package is the default claim that the rule governs edits there; a
  [Rule Pointer](../../CONTEXT.md#rule-pointer) is how a rule says otherwise.
  Both spellings count — the `internal/<pkg>` path and the Go qualifier
  `<pkg>.<member>`, which is what the prose actually uses. A qualifier is told
  from its lookalikes (`config.yml`, `image-build.md`, `worktree.seed`) by
  asking the package what it declares: the right half must be a top-level name
  in that package, so an unexported member counts as readily as an exported one
  and a filename never does. Pinned by
  `TestAMentionIsAQualifierNotItsLookalikes`, so it cannot go green by seeing
  nothing.
- **package → some rule**: every top-level package under `internal/` is claimed
  by at least one rule (`TestEveryPackageIsClaimedByARule`). The direction the
  other two cannot see: both pass for a package no rule has ever heard of.
- **rule file → the index**: every rule file is linked from `CLAUDE.md`
  (`TestRuleFilesAreListedInCLAUDEMd`). A rule absent from that list is one no
  reader knows to open, and Codex loads none of them automatically.

**A pointer's shape**: in the block that names the package — the bullet, the
heading or the paragraph — link the sibling rule file whose `paths:` scopes it
there, as a bare sibling target: `[mounts.md](mounts.md)`. The link is checked,
not taken on trust (`TestARulePointerNamesARuleThatCoversThePackage`):
a target whose frontmatter does not cover the package is not a pointer, and
neither is a link to the same-basename guide under `docs/`. Block-scoped on
purpose — these files cross-link each other constantly, so a file-wide link
would excuse every mention in the file.

**When one of these fails, add the glob, or the pointer — not the exemption.**
Both exemption maps (`ruleMentionExemptions`, `rulePathExemptions`) are empty on
purpose, and an entry in either is a claim about the package: that the rule does
not govern edits there, or that the package carries no guardrail worth loading.
A cross-reference is not that claim — it is a pointer, and it is settled in the
prose, where the reader is. Make the exemption claim deliberately, in the map's
comment; do not reach for it to turn a test green. Adding a package under
`internal/` is therefore two edits, not one — the package, and the rule that
owns it.

**What goes in a rule, and in what shape**, is [`CLAUDE.md`](../../CLAUDE.md)'s
to say — guardrail and test names here, meaning and why in the glossary; name
the thing that pins a value, never the value. Both halves exist for the reason
this directory does: a claim written twice drifts in one of the two places, and
an enumeration copied out of the code is stale the moment the code grows.
