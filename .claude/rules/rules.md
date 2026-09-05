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
  rule's `paths:` (`TestRuleMentionsAreCovered`). Naming a package is the
  default claim that the rule governs edits there.
- **package → some rule**: every top-level package under `internal/` is claimed
  by at least one rule (`TestEveryPackageIsClaimedByARule`). The direction the
  other two cannot see: both pass for a package no rule has ever heard of.
- **rule file → the index**: every rule file is linked from `CLAUDE.md`
  (`TestRuleFilesAreListedInCLAUDEMd`). A rule absent from that list is one no
  reader knows to open, and Codex loads none of them automatically.

**When one of these fails, add the glob — not the exemption.** Both exemption
maps (`ruleMentionExemptions`, `rulePathExemptions`) are empty on purpose, and
an entry in either is a claim about the package: that the rule does not govern
edits there, or that the package carries no guardrail worth loading. Make that
claim deliberately, in the map's comment; do not reach for it to turn a test
green. Adding a package under `internal/` is therefore two edits, not one — the
package, and the rule that owns it.

**What goes in a rule, and in what shape**, is [`CLAUDE.md`](../../CLAUDE.md)'s
to say — guardrail and test names here, meaning and why in the glossary; name
the thing that pins a value, never the value. Both halves exist for the reason
this directory does: a claim written twice drifts in one of the two places, and
an enumeration copied out of the code is stale the moment the code grows.
