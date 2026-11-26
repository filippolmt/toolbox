import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const script = fileURLToPath(new URL("./claude-env-changes.py", import.meta.url));

const changelog = `# Changelog

## 2.1.12

- Added \`CLAUDE_CODE_NEW_KNOB\` to turn on the thing
- Fixed a crash, no variable here

## 2.1.11

- Changed \`DISABLE_TELEMETRY\` to also stop X
- Fixed \`API\` calls retrying

## 2.1.10

- Added \`OLD_KNOB=1\`, already shipped before the bump
`;
const reference = "| `CLAUDE_CODE_NEW_KNOB` | Set to 1 to turn on the thing |\n";
const matrix = `# Privacy

## Claude Code env-var matrix

- \`DISABLE_UPDATES=1\` — block updates.

\`DISABLE_TELEMETRY\` is intentionally NOT set.

## Next section

- \`NOT_IN_MATRIX\` — outside the section.
`;

function run(old, next, files = {}) {
  const dir = mkdtempSync(join(tmpdir(), "claude-env-changes-"));
  const paths = {};
  for (const [name, body] of Object.entries({ changelog, reference, matrix, ...files })) {
    paths[name] = join(dir, name);
    writeFileSync(paths[name], body);
  }
  return spawnSync("python3", [script, old, next,
    "--changelog", paths.changelog, "--reference", paths.reference, "--matrix", paths.matrix], { encoding: "utf8" });
}

test("reports the env vars of every section after OLD up to NEW", () => {
  const r = run("2.1.10", "2.1.12");
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /^<!-- claude-env-changes -->/);
  assert.match(r.stdout, /- `CLAUDE_CODE_NEW_KNOB` \(\[reference\]\([^)]+\)\) — 2\.1\.12: Added/);
  // A matrix variable, set or deliberately unset, goes to the decision list.
  assert.match(r.stdout, /check the decision still holds:\n\n- `DISABLE_TELEMETRY` — 2\.1\.11: Changed/);
  assert.doesNotMatch(r.stdout, /OLD_KNOB/, "OLD's own section is already shipped");
  assert.doesNotMatch(r.stdout, /`API`/, "a backticked acronym is not an env var");
});

test("marks a variable the reference does not list", () => {
  const r = run("2.1.10", "2.1.12", { reference: "" });
  assert.match(r.stdout, /`CLAUDE_CODE_NEW_KNOB` \(not in the reference\)/);
});

test("prints nothing when the range names no env var", () => {
  const r = run("2.1.12", "2.1.12");
  assert.equal(r.status, 0, r.stderr);
  assert.equal(r.stdout, "");
});

test("a changelog without version sections fails instead of reporting nothing", () => {
  const r = run("2.1.10", "2.1.12", { changelog: "<html>rate limited</html>" });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /no version sections/);
});
