import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
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
const reference = "| `CLAUDE_CODE_NEW_KNOB` | Set to 1 to turn on the thing |\n| `DISABLE_TELEMETRY` | Opt out |\n";
const matrix = `# Privacy

## Claude Code env-var matrix

- \`DISABLE_UPDATES=1\` — block updates.

\`DISABLE_TELEMETRY\` is intentionally NOT set.

## Next section

- \`NOT_IN_MATRIX\` — outside the section.
`;

// files: overrides of the fixtures above; null leaves that file missing.
function run(old, next, files = {}) {
  const dir = mkdtempSync(join(tmpdir(), "claude-env-changes-"));
  try {
    const args = [script, old, next];
    for (const [name, body] of Object.entries({ changelog, reference, matrix, ...files })) {
      const path = join(dir, name);
      if (body !== null) writeFileSync(path, body);
      args.push(`--${name}`, path);
    }
    return spawnSync("python3", args, { encoding: "utf8" });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test("reports the env vars of every section after OLD up to NEW", () => {
  const r = run("2.1.10", "2.1.12");
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /^<!-- claude-env-changes -->/);
  assert.match(r.stdout, /- `CLAUDE_CODE_NEW_KNOB` \(\[reference\]\([^)]+\)\) — 2\.1\.12: Added/);
  // A matrix variable, set or deliberately unset, goes to the decision list.
  assert.match(r.stdout, /check the decision still holds:\n\n- `DISABLE_TELEMETRY` \(\[reference\]\([^)]+\)\) — 2\.1\.11: Changed/);
  assert.doesNotMatch(r.stdout, /OLD_KNOB/, "OLD's own section is already shipped");
  assert.doesNotMatch(r.stdout, /`API`/, "a backticked acronym is not an env var");
});

test("marks a variable the reference has no row for", () => {
  const r = run("2.1.10", "2.1.12", { reference: "| `SOMETHING_ELSE` | x |\n" });
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /`CLAUDE_CODE_NEW_KNOB` \(not in the reference\)/);
});

test("prints nothing when the range names no env var or goes backwards", () => {
  for (const [old, next] of [["2.1.12", "2.1.12"], ["2.1.12", "2.1.10"]]) {
    const r = run(old, next);
    assert.equal(r.status, 0, r.stderr);
    assert.equal(r.stdout, "", `${old} → ${next}`);
  }
});

test("fails instead of reporting nothing when it cannot tell", () => {
  const cases = {
    "no version sections": { changelog: "<html>rate limited</html>" },
    "no section for 2.1.13": {},
    "no variable rows": { reference: "<html>moved</html>" },
    "has no '## Claude Code env-var matrix'": { matrix: "# Privacy\n" },
    "cannot read": { changelog: null },
    "not a dotted version": {},
  };
  for (const [reason, files] of Object.entries(cases)) {
    const next = reason.includes("2.1.13") ? "2.1.13" : reason.includes("dotted") ? "" : "2.1.12";
    const r = run("2.1.10", next, files);
    assert.equal(r.status, 1, `${reason}: ${r.stdout}`);
    assert.ok(r.stderr.includes(reason), `${reason}: ${r.stderr}`);
    assert.equal(r.stdout, "");
  }
});

test("truncates a report that would outgrow a PR comment", () => {
  const lines = Array.from({ length: 1500 }, (_, i) => `- Added \`KNOB_${i}\` ${"x".repeat(40)}`).join("\n");
  const r = run("2.1.10", "2.1.12", { changelog: `## 2.1.12\n\n${lines}\n` });
  assert.equal(r.status, 0, r.stderr);
  assert.ok(r.stdout.length < 61000, `${r.stdout.length} characters`);
  assert.match(r.stdout, /…truncated: read the changelog for the rest\.\n$/);
});
