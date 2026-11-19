import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const root = fileURLToPath(new URL("../..", import.meta.url));
// Outside the DooD-guard test, run make as from a native terminal: a checkout
// on a toolbox's root filesystem would otherwise trip the guard in every test.
const makeEnv = { ...process.env, TOOLBOX_HOST_WORKSPACE: "" };

test("preflight limits complexity to the diff and reuses the coverage floor", () => {
  const result = spawnSync("make", ["-n", "go-preflight", "BASE=HEAD"], { cwd: root, env: makeEnv, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /--enable-only=gocognit/);
  assert.match(result.stdout, /complexity-patch.py --base "HEAD"/);
  assert.match(result.stdout, /--new-from-patch="dist\/\.scratch\/complexity.patch"/);
  const minimum = /COVERAGE_MIN: '(\d+)'/.exec(readFileSync(join(root, ".github/workflows/ci.yml"), "utf8"))[1];
  assert.ok(result.stdout.includes(`--minimum "${minimum}" --warn-only`));
});

test("formatting excludes ignored scratch and downloaded modules", () => {
  const result = spawnSync("make", ["-n", "go-fmt"], { cwd: root, env: makeEnv, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /git .*ls-files.*--exclude-standard/);
  assert.match(result.stdout, /docker run --rm -i/);
  assert.match(result.stdout, /bash .github\/scripts\/format-go.sh/);
  assert.doesNotMatch(result.stdout, /find \./);
});

test("scratch and persistent coverage live in a Go-ignored directory", () => {
  const result = spawnSync("make", ["-n", "scratch", "go-coverage"], { cwd: root, env: makeEnv, encoding: "utf8" });
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
    const result = spawnSync("make", ["-f", join(root, "Makefile"), "go-fmt"], { cwd: linked, env: { ...makeEnv, PATH: `${bin}:${process.env.PATH}`, FORMAT_LOG: log }, encoding: "utf8" });
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
if (args[0] === 'ps' && process.env.TEST_PS_FAILURE) process.exit(1);
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
    const repeated = spawnSync("bash", args, { cwd: directory, env: { ...env, GATE_COUNT: "30" }, encoding: "utf8" });
    assert.equal(repeated.status, 0, repeated.stderr);
    assert.ok(readFileSync(log, "utf8").trim().split("\n").map(JSON.parse).at(-1).includes("-count=30"), "GATE_COUNT did not reach go test");
    const callsBeforeBadCount = readFileSync(log, "utf8").trim().split("\n").length;
    const badCount = spawnSync("bash", args, { cwd: directory, env: { ...env, GATE_COUNT: "0" }, encoding: "utf8" });
    assert.equal(badCount.status, 2);
    assert.match(badCount.stderr, /GATE_COUNT must be a positive integer/);
    assert.equal(readFileSync(log, "utf8").trim().split("\n").length, callsBeforeBadCount, "runner touched the daemon with an invalid GATE_COUNT");
    const blocked = spawnSync("bash", args, { cwd: directory, env: { ...env, TEST_ANCHOR: "1" }, encoding: "utf8" });
    assert.equal(blocked.status, 1);
    assert.match(blocked.stderr, /peer anchor/);
    const after = readFileSync(log, "utf8").trim().split("\n").map(JSON.parse);
    assert.equal(after.length, callsBeforeBadCount + 1, "runner touched the daemon after detecting a live peer anchor");
    const failed = spawnSync("bash", args, { cwd: directory, env: { ...env, TEST_PS_FAILURE: "1" }, encoding: "utf8" });
    assert.equal(failed.status, 1, "runner treated a failed anchor inspection as absence");
    assert.match(failed.stderr, /Cannot inspect.*peer anchor/);
    const finalCalls = readFileSync(log, "utf8").trim().split("\n").map(JSON.parse);
    assert.equal(finalCalls.length, after.length + 1, "runner continued after the daemon refused inspection");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("Go test targets forward explicit build tags", () => {
  const result = spawnSync("make", ["-n", "go-test-one", "PKG=./internal/container", "RUN=TestExecShell", "TAGS=dockergate"], { cwd: root, env: makeEnv, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /go test .* -tags "dockergate"/);
});

test("mounting targets refuse a directory the host daemon cannot see", () => {
  const directory = mkdtempSync(join(tmpdir(), "toolbox-local-"));
  const bin = join(directory, "bin");
  mkdirSync(bin);
  writeFileSync(join(bin, "findmnt"), "#!/bin/sh\necho \"$FAKE_MOUNT\"\n", { mode: 0o755 });
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, TOOLBOX_HOST_WORKSPACE: "/Users/dev/projects", FAKE_MOUNT: "/" };
  const make = (target, overrides = {}) =>
    spawnSync("make", ["-n", "-f", join(root, "Makefile"), "-C", directory, target], { env: { ...env, ...overrides }, encoding: "utf8" });
  try {
    for (const target of ["go-test", "check-links", "scratch"]) {
      const blocked = make(target);
      assert.notEqual(blocked.status, 0, `${target} mounted a container-local directory`);
      assert.match(blocked.stderr, /host Docker daemon cannot see/);
    }
    assert.equal(make("help").status, 0, "a target that mounts nothing was blocked");
    assert.equal(make("go-test", { FAKE_MOUNT: "/Users/dev/projects" }).status, 0, "a host bind was blocked");
    assert.equal(make("go-test", { TOOLBOX_HOST_WORKSPACE: "" }).status, 0, "a native host terminal was blocked");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

function gatesFor(paths) {
  const result = spawnSync("python3", [".github/scripts/pre-push-gates.py", "--files", "-"], {
    cwd: root, input: paths.join("\n"), encoding: "utf8",
  });
  assert.equal(result.status, 0, result.stderr);
  return result.stdout;
}

test("gates map a sessionplan edit to go-check and the real-daemon gate", () => {
  const out = gatesFor(["internal/sessionplan/plan.go", "README.md"]);
  for (const gate of ["make go-check", "make go-dockergate", "make go-test-one PKG=./internal/devrules", "make check-links"]) {
    assert.ok(out.includes(gate), `missing ${gate}:\n${out}`);
  }
  assert.doesNotMatch(out, /^make test$/m);
});

test("gates list a command once however many rows name it", () => {
  const out = gatesFor(["internal/build/assets/entrypoint.sh", ".github/scripts/host-share-git-rewrite.sh"]);
  assert.equal(out.match(/^make test-host-share$/gm).length, 1, out);
  assert.match(out, /^make test$/m);
  assert.match(out, /shellcheck -S error/);
});

test("gates cite only make targets and scripts that exist", () => {
  const makefile = readFileSync(join(root, "Makefile"), "utf8");
  const src = readFileSync(join(root, ".github/scripts/pre-push-gates.py"), "utf8");
  const targets = [...src.matchAll(/\bmake ([a-z][\w-]*)/g)].map((m) => m[1]);
  assert.ok(targets.length > 0, "no make target found in pre-push-gates.py");
  for (const target of targets) {
    assert.match(makefile, new RegExp(`^${target}:(?!=)`, "m"), `make ${target} is not a Makefile target`);
  }
  const scripts = [...src.matchAll(/\.github\/scripts\/[\w.-]+\.\w+/g)].map((m) => m[0]);
  assert.ok(scripts.length > 0, "no .github/scripts path found in pre-push-gates.py");
  for (const script of scripts) {
    assert.ok(existsSync(join(root, script)), `${script} does not exist`);
  }
});

test("a changed .github/scripts shell script gets shellcheck as a command", () => {
  assert.match(gatesFor([".github/scripts/check-branch-name.sh"]), /^shellcheck \.github\/scripts\/\*\.sh$/m);
  assert.doesNotMatch(gatesFor([".github/scripts/complexity-patch.py"]), /^shellcheck/m);
});

test("a non-Go file in a daemon-gated package still needs go-check", () => {
  const out = gatesFor(["internal/container/testdata/x.json"]);
  assert.match(out, /^make go-check$/m, out);
  assert.match(out, /^make go-dockergate$/m, out);
});

test("gates fail loudly on a base git cannot resolve", () => {
  const result = spawnSync("python3", [".github/scripts/pre-push-gates.py", "--base", "no-such-ref"], { cwd: root, encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /no-such-ref/);
  assert.doesNotMatch(result.stdout, /no gate/);
});

test("make gates diffs against BASE", () => {
  const result = spawnSync("make", ["-n", "gates", "BASE=HEAD"], { cwd: root, env: makeEnv, encoding: "utf8" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /pre-push-gates.py --base "HEAD"/);
});

test("gates pin the renovate validator to ci.yml's RENOVATE_VERSION", () => {
  const pin = /renovate@(\S+) /.exec(gatesFor(["renovate.json"]))[1];
  const ciLine = readFileSync(join(root, ".github/workflows/ci.yml"), "utf8").split("\n").find((l) => l.includes("RENOVATE_VERSION:"));
  assert.ok(ciLine, "ci.yml has no RENOVATE_VERSION line");
  assert.ok(ciLine.includes(pin) && /^\d+\.\d+\.\d+$/.test(pin), `pin ${pin} is not ci.yml's (${ciLine.trim()})`);
});

test("gates follow docker-ci's image filter: daemon packages get the gate, the rest the build", () => {
  const daemon = gatesFor(["internal/sessionplan/x.go"]);
  assert.match(daemon, /^make go-dockergate$/m, daemon);
  assert.doesNotMatch(daemon, /^make test$/m, daemon);
  for (const path of ["internal/build/assets/Dockerfile", ".github/fixtures/android-e2e/x"]) {
    const out = gatesFor([path]);
    assert.match(out, /^make test$/m, `${path}: ${out}`);
    assert.doesNotMatch(out, /^make go-dockergate$/m, `${path}: ${out}`);
  }
  const outside = gatesFor(["internal/config/x.go"]);
  assert.doesNotMatch(outside, /^make (test|go-dockergate)$/m, outside);
});

test("the real-daemon gate asks for a fresh image only when the image changed", () => {
  const goOnly = gatesFor(["internal/sessionplan/x.go"]);
  assert.match(goOnly, /nothing `make build` reads changed: the default image serves/, goOnly);
  assert.doesNotMatch(goOnly, /after `make build`/, goOnly);
  // --files cannot read go.mod's diff, so it counts as changed.
  for (const input of ["internal/build/assets/Dockerfile", "go.mod"]) {
    const withImage = gatesFor(["internal/sessionplan/x.go", input]);
    assert.match(withImage, /after `make build`, pass FULL=/, `${input}: ${withImage}`);
  }
  const triggerOnly = gatesFor(["internal/sessionplan/x.go", ".github/workflows/docker-ci.yml"]);
  assert.match(triggerOnly, /the default image serves/, triggerOnly);
});

test("a go.mod change asks for a fresh image only when GO_VERSION moved", () => {
  const repo = mkdtempSync(join(tmpdir(), "gates-gomod-"));
  try {
    const git = (...args) => {
      const r = spawnSync("git", ["-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", ...args], { encoding: "utf8" });
      assert.equal(r.status, 0, r.stderr);
    };
    const gomod = (toolchain, dep, go = "1.26.0") => `module x\n\ngo ${go}\n\ntoolchain go${toolchain}\n\nrequire example.com/dep ${dep}\n`;
    git("init", "-q", "-b", "main");
    mkdirSync(join(repo, "internal/sessionplan"), { recursive: true });
    writeFileSync(join(repo, "internal/sessionplan/x.go"), "package x\n");
    writeFileSync(join(repo, "go.mod"), gomod("1.26.1", "v1.0.0"));
    git("add", ".");
    git("commit", "-q", "-m", "base");
    git("switch", "-q", "-c", "fix/x");
    writeFileSync(join(repo, "internal/sessionplan/x.go"), "package x // changed\n");
    const run = () => spawnSync("python3", [".github/scripts/pre-push-gates.py", "--repo", repo, "--base", "main"], { cwd: root, encoding: "utf8" });

    writeFileSync(join(repo, "go.mod"), gomod("1.26.1", "v1.1.0"));
    const requireOnly = run();
    assert.equal(requireOnly.status, 0, requireOnly.stderr);
    assert.match(requireOnly.stdout, /the default image serves/, `a require bump leaves the image as it is:\n${requireOnly.stdout}`);

    // GO_VERSION is the toolchain line when there is one (Makefile, docker-ci).
    writeFileSync(join(repo, "go.mod"), gomod("1.26.1", "v1.0.0", "1.26.1"));
    const goOnly = run();
    assert.equal(goOnly.status, 0, goOnly.stderr);
    assert.match(goOnly.stdout, /the default image serves/, `a go bump under a toolchain leaves GO_VERSION as it is:\n${goOnly.stdout}`);

    writeFileSync(join(repo, "go.mod"), gomod("1.26.2", "v1.0.0"));
    const toolchain = run();
    assert.equal(toolchain.status, 0, toolchain.stderr);
    assert.match(toolchain.stdout, /after `make build`, pass FULL=/, `a toolchain move is the GO_VERSION build arg:\n${toolchain.stdout}`);
  } finally {
    rmSync(repo, { recursive: true, force: true });
  }
});

test("gates read renames, non-ASCII paths and untracked files from git", () => {
  const repo = mkdtempSync(join(tmpdir(), "gates-"));
  try {
    const git = (...args) => {
      const r = spawnSync("git", ["-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", ...args], { encoding: "utf8" });
      assert.equal(r.status, 0, r.stderr);
    };
    git("init", "-q", "-b", "main");
    mkdirSync(join(repo, "internal/container"), { recursive: true });
    writeFileSync(join(repo, "internal/container/data.json"), "{}\n");
    git("add", ".");
    git("commit", "-q", "-m", "base");
    git("switch", "-q", "-c", "feat/x");
    mkdirSync(join(repo, "assets"));
    git("mv", "internal/container/data.json", "assets/data.json");
    git("commit", "-q", "-m", "move");
    mkdirSync(join(repo, "docs"));
    writeFileSync(join(repo, "docs/caffè.md"), "x\n");
    const result = spawnSync("python3", [".github/scripts/pre-push-gates.py", "--repo", repo, "--base", "main"], { cwd: root, encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
    assert.match(result.stdout, /^make go-dockergate$/m, `the move out of internal/container must keep its gate:\n${result.stdout}`);
    assert.match(result.stdout, /^make check-links$/m, `the untracked non-ASCII Markdown file must match:\n${result.stdout}`);
  } finally {
    rmSync(repo, { recursive: true, force: true });
  }
});

test("gates refuse to guess a base", () => {
  const result = spawnSync("python3", [".github/scripts/pre-push-gates.py"], { cwd: root, encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /--base is required/);
});

test("go-version.sh derives GO_VERSION: the toolchain line, else the go line", () => {
  const run = (gomod) => spawnSync(".github/scripts/go-version.sh", ["-"], { cwd: root, input: gomod, encoding: "utf8" });
  const withToolchain = run("module x\n\ngo 1.26.0\n\ntoolchain go1.27.4\n");
  assert.equal(withToolchain.status, 0, withToolchain.stderr);
  assert.equal(withToolchain.stdout.trim(), "1.27.4");
  const goOnly = run("module x\n\ngo 1.26.0\n");
  assert.equal(goOnly.status, 0, goOnly.stderr);
  assert.equal(goOnly.stdout.trim(), "1.26.0", "no toolchain line: an empty GO_VERSION would override the Dockerfile's default with nothing");
  const none = run("module x\n");
  assert.notEqual(none.status, 0, "a go.mod with neither line must fail, not build with an empty GO_VERSION");
});

test("every GO_VERSION build arg comes from go-version.sh", () => {
  const makefile = readFileSync(join(root, "Makefile"), "utf8");
  assert.match(makefile, /^GO_VERSION\s*:=\s*\$\(shell \.github\/scripts\/go-version\.sh\)/m);
  for (const wf of ["docker-ci.yml", "docker-publish-reusable.yml"]) {
    const src = readFileSync(join(root, ".github/workflows", wf), "utf8");
    // An assignment, so a failing script fails the step under bash -e.
    assert.match(src, /id: gover\n\s+run: \|\n\s+version=\$\(\.github\/scripts\/go-version\.sh\)\n\s+echo "version=\$version" >> "\$GITHUB_OUTPUT"/, wf);
  }
});

test("make build refuses an empty GO_VERSION before reaching docker", () => {
  const result = spawnSync("make", ["build", "GO_VERSION="], { cwd: root, env: makeEnv, encoding: "utf8" });
  assert.notEqual(result.status, 0, result.stdout);
  assert.match(result.stderr, /GO_VERSION is empty/);
  assert.doesNotMatch(result.stdout, /docker buildx build/);
});

test("apt-install retries the network half until a deadline, and installs offline", () => {
  const directory = mkdtempSync(join(tmpdir(), "toolbox-apt-"));
  const bin = join(directory, "bin");
  mkdirSync(bin);
  const log = join(directory, "calls");
  writeFileSync(join(bin, "sudo"), '#!/bin/sh\nexec "$@"\n', { mode: 0o755 });
  // Logs each call. The first FAIL_DOWNLOADS downloads fail; with STALL set,
  // the first download hangs instead, as a stalled mirror does.
  writeFileSync(join(bin, "apt-get"), `#!/bin/sh
echo "$*" >> "$CALL_LOG"
case "$*" in
  *--download-only*)
    n=$(grep -c -- '--download-only' "$CALL_LOG")
    if [ -n "$STALL" ] && [ "$n" -eq 1 ]; then sleep 30; fi
    [ "$n" -gt "$FAIL_DOWNLOADS" ] ;;
esac
`, { mode: 0o755 });
  const script = join(root, ".github/scripts/apt-install.sh");
  const run = (overrides, args = ["zsh"]) => {
    rmSync(log, { force: true });
    const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, CALL_LOG: log, FAIL_DOWNLOADS: "0", APT_INSTALL_PAUSE: "0", APT_INSTALL_STEP_TIMEOUT: "1", ...overrides };
    const result = spawnSync("bash", [script, ...args], { env, encoding: "utf8" });
    const calls = existsSync(log) ? readFileSync(log, "utf8").trim().split("\n") : [];
    return { result, calls, updates: calls.filter((c) => c.endsWith(" update")).length };
  };
  try {
    // The incident: update succeeds, the package download fails.
    const recovered = run({ FAIL_DOWNLOADS: "2" });
    assert.equal(recovered.result.status, 0, recovered.result.stderr);
    assert.equal(recovered.updates, 3, "a failed download did not retry the update with it");
    assert.match(recovered.calls.at(-1), /install -y --no-download zsh$/, "the install did not run offline, last");
    const stalled = run({ STALL: "1" });
    assert.equal(stalled.result.status, 0, stalled.result.stderr);
    assert.equal(stalled.updates, 2, "a stalled download was not cut off and retried");
    const down = run({ FAIL_DOWNLOADS: "999", APT_INSTALL_DEADLINE: "2", APT_INSTALL_PAUSE: "1" });
    assert.equal(down.result.status, 1);
    assert.match(down.result.stderr, /giving up after \d+ attempts \(2s deadline\)/);
    assert.ok(!down.calls.some((c) => c.includes("--no-download")), "it installed after giving up");
    assert.equal(run({}, []).result.status, 2);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
