import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import pathRules, { parseRulePaths, ReadCoverage, rulesForPath, type Rule } from "../extensions/path-rules.ts";

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

test("read coverage must be contiguous and complete", () => {
  const coverage = new ReadCoverage();
  coverage.record("rule", 3, 5);
  assert.equal(coverage.complete("rule", 5), false);
  coverage.record("rule", 1, 2);
  coverage.record("rule", 3, 5);
  assert.equal(coverage.complete("rule", 5), true);
  coverage.clear();
  assert.equal(coverage.complete("rule", 1), false);
});

test("blocks edits until successful reads cover each rule and invalidates changed rules", async () => {
  const cwd = await mkdtemp(join(tmpdir(), "path-rules-"));
  try {
    const rule = join(cwd, ".claude/rules/runtime.md");
    await mkdir(join(cwd, ".claude/rules"), { recursive: true });
    await writeFile(rule, `---
paths:
  - "src/**"
---
one
two
three
`);

    const handlers = new Map<string, (event: any, ctx: any) => Promise<any>>();
    pathRules({ on: (name: string, handler: any) => handlers.set(name, handler) } as any);
    const call = handlers.get("tool_call")!;
    const result = handlers.get("tool_result")!;
    const ctx = { cwd };
    const edit = { toolName: "edit", input: { path: "src/a.go" } };
    const read = { toolName: "read", input: { path: rule }, content: [{ type: "text", text: "" }] };

    assert.equal((await call(edit, ctx)).block, true);
    await result({ ...read, isError: true }, ctx);
    assert.equal((await call(edit, ctx)).block, true);

    await result({ ...read, isError: false, input: { path: rule, offset: 1 }, content: [{ type: "text", text: "Use offset=5 to continue" }] }, ctx);
    assert.equal((await call(edit, ctx)).block, true);
    await result({ ...read, isError: false, input: { path: rule, offset: 5 } }, ctx);
    assert.equal(await call(edit, ctx), undefined);

    await result({ toolName: "write", isError: false, input: { path: rule }, content: [] }, ctx);
    const invalidated = await call(edit, ctx);
    assert.equal(invalidated.block, true);
    assert.match(invalidated.reason, /re-read/);
    assert.match(invalidated.reason, /rule edits invalidate prior reads/);
  } finally {
    await rm(cwd, { recursive: true, force: true });
  }
});
