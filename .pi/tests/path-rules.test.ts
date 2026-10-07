import assert from "node:assert/strict";
import test from "node:test";
import { parseRulePaths, rulesForPath, type Rule } from "../extensions/path-rules.ts";

test("parses quoted path globs from rule frontmatter", () => {
  assert.deepEqual(parseRulePaths(`---
paths:
  - "cmd/**"
  - 'internal/bridge/**'
---
body
`), ["cmd/**", "internal/bridge/**"]);
});

test("finds every rule governing a repository path", () => {
  const cwd = "/repo";
  const rules: Rule[] = [
    { file: "/repo/.claude/rules/runtime.md", globs: ["cmd/**", "internal/bridge/**"] },
    { file: "/repo/.claude/rules/config.md", globs: ["cmd/worktree.go"] },
  ];
  assert.deepEqual(
    rulesForPath(cwd, "cmd/worktree.go", rules).map((rule) => rule.file),
    ["/repo/.claude/rules/runtime.md", "/repo/.claude/rules/config.md"],
  );
  assert.deepEqual(rulesForPath(cwd, "../other/file.go", rules), []);
});
