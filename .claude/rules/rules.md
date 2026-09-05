---
paths:
  - ".claude/rules/**"
  - "internal/devrules/**"
---

# The rules about the rules — the guardrails these files are held to

A rule file is loaded by its `paths:` frontmatter and by nothing else, so a
guardrail scoped to the wrong globs is a guardrail that never fires. Three
tests in `internal/devrules` hold the frontmatter to the prose; they are
repo-hygiene tests, not product code, and they ride `go test ./...` so a
directory rename cannot quietly orphan a rule.

- **rule → disk**: every glob still points at something that exists
  (`TestRulePathsResolve`). Catches a rename or deletion leaving a dead glob.
- **body → frontmatter**: every package a rule *names* is scoped by that same
  rule's `paths:` (`TestRuleMentionsAreCovered`). Naming a package is the
  default claim that the rule governs edits there.
- **package → some rule**: every `internal/` package is claimed by at least one
  rule (`TestEveryPackageIsClaimedByARule`). The direction the other two cannot
  see: both pass for a package no rule has ever heard of.

**When one of these fails, add the glob — not the exemption.** Both exemption
maps (`ruleMentionExemptions`, `rulePathExemptions`) are empty on purpose, and
an entry in either is a claim about the package: that the rule does not govern
edits there, or that the package carries no guardrail worth loading. Make that
claim deliberately, in the map's comment; do not reach for it to turn a test
green. Adding a package under `internal/` is therefore two edits, not one — the
package, and the rule that owns it.

**Guardrail here, meaning in the glossary** — a rule carries the invariant and
the names of the tests that hold it, [`CONTEXT.md`](../../CONTEXT.md) carries
what a concept means and why it was named. Link across, never restate: a claim
written twice drifts in one of the two places, which is the failure mode this
whole directory exists to prevent. Name the thing that pins a value rather than
the value itself, for the same reason — an enumeration copied out of the code
is stale the moment the code grows.
