import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const root = fileURLToPath(new URL("../..", import.meta.url));

test("preflight limits complexity to the diff and reuses the coverage floor", () => {
  const result = spawnSync("make", ["-n", "go-preflight", "BASE=HEAD"], { cwd: root, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /--enable-only=gocognit/);
  assert.match(result.stdout, /complexity-patch.py --base "HEAD"/);
  assert.match(result.stdout, /--new-from-patch="dist\/\.scratch\/complexity.patch"/);
  const minimum = /COVERAGE_MIN: '(\d+)'/.exec(readFileSync(join(root, ".github/workflows/ci.yml"), "utf8"))[1];
  assert.ok(result.stdout.includes(`--minimum "${minimum}" --warn-only`));
});

test("formatting excludes ignored scratch and downloaded modules", () => {
  const result = spawnSync("make", ["-n", "go-fmt"], { cwd: root, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /git .*ls-files.*--exclude-standard/);
  assert.match(result.stdout, /docker run --rm -i/);
  assert.match(result.stdout, /bash .github\/scripts\/format-go.sh/);
  assert.doesNotMatch(result.stdout, /find \./);
});

test("scratch and persistent coverage live in a Go-ignored directory", () => {
  const result = spawnSync("make", ["-n", "scratch", "go-coverage"], { cwd: root, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /mkdir -p "dist\/\.scratch"/);
  assert.match(result.stdout, /-coverprofile=dist\/\.scratch\/coverage.out/);
});

function coverageFixture(run) {
  const directory = mkdtempSync(join(tmpdir(), "toolbox-delta-"));
  const env = { ...process.env, GIT_CONFIG_GLOBAL: "/dev/null", GIT_CONFIG_SYSTEM: "/dev/null" };
  const git = (...args) => {
    const result = spawnSync("git", args, { cwd: directory, env, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    return result.stdout.trim();
  };
  try {
    writeFileSync(join(directory, "go.mod"), "module example.test/app\n");
    writeFileSync(join(directory, "main.go"), "package main\nfunc main() {\n println(1)\n}\n");
    git("init", "-q");
    git("add", ".");
    git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "baseline");
    run(directory, git("rev-parse", "HEAD"));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

test("formatting handles deleted sources, spaces and ignored scratch", () => {
  coverageFixture((directory) => {
    const bin = join(directory, "bin");
    mkdirSync(bin);
    const log = join(directory, "format.json");
    writeFileSync(join(bin, "gofmt"), `#!/usr/bin/env node
require('node:fs').writeFileSync(process.env.FORMAT_LOG, JSON.stringify(process.argv.slice(2)));
`, { mode: 0o755 });
    rmSync(join(directory, "main.go"));
    writeFileSync(join(directory, "new source.go"), "package main\n");
    writeFileSync(join(directory, ".gitignore"), "dist/\n");
    mkdirSync(join(directory, "dist/.scratch"), { recursive: true });
    writeFileSync(join(directory, "dist/.scratch/copied.go"), "not project source\n");
    const selection = spawnSync("git", ["ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go"], { cwd: directory }).stdout;
    const result = spawnSync("bash", [join(root, ".github/scripts/format-go.sh")], { cwd: directory, input: selection, env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, FORMAT_LOG: log }, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(JSON.parse(readFileSync(log, "utf8")), ["-w", "--", "new source.go"]);
    rmSync(join(directory, "new source.go"));
    rmSync(log);
    const empty = spawnSync("bash", [join(root, ".github/scripts/format-go.sh")], { cwd: directory, input: selection, env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, FORMAT_LOG: log }, encoding: "utf8" });
    assert.equal(empty.status, 0, empty.stderr);
    assert.equal(existsSync(log), false, "empty file selection invoked the formatter");
  });
});

test("formatting selects linked-worktree sources before entering Docker", () => {
  coverageFixture((directory) => {
    const linked = join(directory, "linked");
    const added = spawnSync("git", ["worktree", "add", "-qb", "linked", linked, "HEAD"], { cwd: directory, encoding: "utf8" });
    assert.equal(added.status, 0, added.stderr);
    const bin = join(directory, "bin");
    mkdirSync(bin);
    const log = join(directory, "selection");
    writeFileSync(join(bin, "docker"), "#!/usr/bin/env node\nconst fs = require('node:fs'); fs.writeFileSync(process.env.FORMAT_LOG, fs.readFileSync(0));\n", { mode: 0o755 });
    const result = spawnSync("make", ["-f", join(root, "Makefile"), "go-fmt"], { cwd: linked, env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, FORMAT_LOG: log }, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(readFileSync(log, "utf8"), "main.go\0");
  });
});

test("complexity patches include changed function declarations but not untouched siblings", () => {
  coverageFixture((directory, base) => {
    const original = "package main\nfunc main() {\n println(1)\n}\n\nfunc untouched() {}\n";
    writeFileSync(join(directory, "main.go"), original);
    writeFileSync(join(directory, ".gitattributes"), "*.go diff=golang\n");
    const git = (...args) => spawnSync("git", args, { cwd: directory, encoding: "utf8" });
    git("add", ".");
    const commit = git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "sibling");
    assert.equal(commit.status, 0, commit.stderr);
    base = git("rev-parse", "HEAD").stdout.trim();
    writeFileSync(join(directory, "main.go"), original.replace("println(1)", "println(2)"));
    writeFileSync(join(directory, "new.go"), "package main\nfunc added() {}\n");
    const result = spawnSync("python3", [join(root, ".github/scripts/complexity-patch.py"), "--base", base], { cwd: directory, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /\+func main\(\)/);
    assert.match(result.stdout, /\+func added\(\)/);
    assert.doesNotMatch(result.stdout, /untouched/);
  });
});

test("delta coverage includes working-tree changes and warns without masking its estimate", () => {
  coverageFixture((directory, base) => {
    writeFileSync(join(directory, "main.go"), "package main\nfunc main() {\n println(2)\n println(3)\n}\n");
    const profile = join(directory, "coverage.out");
    writeFileSync(profile, "mode: atomic\nexample.test/app/main.go:3.1,3.12 1 1\nexample.test/app/main.go:4.1,4.12 1 0\n");
    const args = [join(root, ".github/scripts/new-code-coverage.py"), profile, "--base", base, "--minimum", "80"];
    const result = spawnSync("python3", args, { cwd: directory, encoding: "utf8" });
    assert.equal(result.status, 1, result.stderr);
    assert.match(result.stdout, /1\/2.*50\.0%/);
    const warning = spawnSync("python3", [...args, "--warn-only"], { cwd: directory, encoding: "utf8" });
    assert.equal(warning.status, 0, warning.stderr);
    assert.match(warning.stdout, /WARNING/);
  });
});

test("delta coverage counts untracked Go sources but not tests", () => {
  coverageFixture((directory, base) => {
    writeFileSync(join(directory, "new.go"), "package main\nfunc missing() {}\n");
    writeFileSync(join(directory, "new_test.go"), "package main\nfunc fixture() {}\n");
    const profile = join(directory, "coverage.out");
    writeFileSync(profile, "mode: set\nexample.test/app/new.go:2.1,2.18 1 0\nexample.test/app/new_test.go:2.1,2.18 1 0\n");
    const result = spawnSync("python3", [join(root, ".github/scripts/new-code-coverage.py"), profile, "--base", base, "--minimum", "80"], { cwd: directory, encoding: "utf8" });
    assert.equal(result.status, 1, result.stderr);
    assert.match(result.stdout, /0\/1/);
  });
});

test("delta coverage calls out changed files missing from the profile", () => {
  coverageFixture((directory, base) => {
    writeFileSync(join(directory, "new.go"), "package main\nfunc missing() {}\n");
    const profile = join(directory, "coverage.out");
    writeFileSync(profile, "mode: set\nexample.test/app/main.go:2.1,4.2 1 1\n");
    const args = [join(root, ".github/scripts/new-code-coverage.py"), profile, "--base", base, "--minimum", "80"];
    const result = spawnSync("python3", args, { cwd: directory, encoding: "utf8" });
    assert.equal(result.status, 2);
    assert.match(result.stdout, /WARNING.*new.go/);
    const advisory = spawnSync("python3", [...args, "--warn-only"], { cwd: directory, encoding: "utf8" });
    assert.equal(advisory.status, 0, advisory.stderr);
    assert.match(advisory.stdout, /incomplete/);
  });
});

test("delta coverage ignores deletion-only changes and rejects corrupt profiles", () => {
  coverageFixture((directory, base) => {
    writeFileSync(join(directory, "main.go"), "package main\nfunc main() {\n}\n");
    const profile = join(directory, "coverage.out");
    writeFileSync(profile, "mode: atomic\nexample.test/app/main.go:2.1,3.2 1 1\n");
    const args = [join(root, ".github/scripts/new-code-coverage.py"), profile, "--base", base, "--minimum", "80"];
    const result = spawnSync("python3", args, { cwd: directory, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /No changed executable/);
    writeFileSync(profile, "broken\n");
    const corrupt = spawnSync("python3", args, { cwd: directory, encoding: "utf8" });
    assert.equal(corrupt.status, 2);
    assert.match(corrupt.stderr, /invalid coverage profile/);
  });
});

test("dockergate runner maps identity, socket group and daemon-visible fixture paths", () => {
  const directory = mkdtempSync(join(tmpdir(), "toolbox runner "));
  const bin = join(directory, "bin");
  mkdirSync(bin);
  const log = join(directory, "calls.jsonl");
  writeFileSync(join(bin, "docker"), `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.CALL_LOG, JSON.stringify(args) + '\\n');
if (args[0] === 'ps' && process.env.TEST_ANCHOR) console.log('existing-anchor');
if (args.includes('stat')) console.log('43212');
`, { mode: 0o755 });
  writeFileSync(join(bin, "id"), "#!/bin/sh\ncase $1 in -u) echo 43210;; -g) echo 43211;; esac\n", { mode: 0o755 });
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, CALL_LOG: log };
  const args = [join(root, ".github/scripts/go-dockergate.sh"), directory, "runtime:test", "runner:test", "TestOne|TestTwo", "./internal/container/"];
  try {
    const result = spawnSync("bash", args, { cwd: directory, env, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    const calls = readFileSync(log, "utf8").trim().split("\n").map(JSON.parse);
    const run = calls.at(-1);
    assert.equal(run[run.indexOf("--user") + 1], "43210:43211");
    assert.equal(run[run.indexOf("--group-add") + 1], "43212");
    assert.ok(run.includes(`${directory}:${directory}`));
    assert.ok(run.includes(`TMPDIR=${directory}/dist/.scratch/dockergate/tmp-43210`));
    assert.ok(run.includes("GOPATH="));
    assert.deepEqual(run.slice(-8), ["go", "test", "-tags", "dockergate", "-count=1", "-run", "TestOne|TestTwo", "./internal/container/"]);
    const blocked = spawnSync("bash", args, { cwd: directory, env: { ...env, TEST_ANCHOR: "1" }, encoding: "utf8" });
    assert.equal(blocked.status, 1);
    assert.match(blocked.stderr, /peer anchor/);
    const after = readFileSync(log, "utf8").trim().split("\n").map(JSON.parse);
    assert.equal(after.length, calls.length + 1, "runner touched the daemon after detecting a live peer anchor");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("Go test targets forward explicit build tags", () => {
  const result = spawnSync("make", ["-n", "go-test-one", "PKG=./internal/container", "RUN=TestExecShell", "TAGS=dockergate"], { cwd: root, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /go test .* -tags "dockergate"/);
});
