#!/usr/bin/env python3
"""List the gates CI would run for the paths a change touches.

The pre-push gate table, as data: each row maps path patterns to the local
command that runs what its CI job runs. Touched paths are the committed diff
since --base (merge-base, like CI) plus uncommitted and untracked files, so a
gate is listed before the push that would fail it. --files - reads the paths
from stdin instead (tests).

Prints the commands, it does not run them: some need the host (a Mac running
Docker Desktop for the Host Share) or a built image.
"""
import argparse
import re
import subprocess
import sys
from pathlib import Path
from typing import Callable, NamedTuple

ROOT = Path(__file__).resolve().parents[2]


def docker_filter():
    """The `docker` path filter docker-ci.yml builds the image for, read from
    the workflow so the two cannot drift: `dir/**` patterns as prefixes, the
    rest as exact paths."""
    m = re.search(r"^\s*docker:\n((?:\s+- '.*'\n)+)", (ROOT / ".github/workflows/docker-ci.yml").read_text(), re.M)
    if not m:
        sys.exit("pre-push-gates: docker-ci.yml has no `docker:` path filter; update docker_filter()")
    return [pat.removesuffix("**") for pat in re.findall(r"- '([^']+)'", m.group(1))]


DOCKER_PATHS = docker_filter()
# The Go packages in that filter beyond the image assets: docker-ci runs the
# real-daemon gates for them.
DAEMON_PATHS = [p for p in DOCKER_PATHS if p.startswith("internal/") and not p.startswith("internal/build/")]


def matches(path, patterns):
    return any(path.startswith(pat) if pat.endswith("/") else path == pat for pat in patterns)


def dockergate_pkg(p):
    return matches(p, DAEMON_PATHS)


def docker_ci_trigger(p):
    """A path in docker-ci's filter beyond the daemon-gated Go: docker-ci
    builds and smoke-tests the image for it."""
    return matches(p, DOCKER_PATHS) and not dockergate_pkg(p)


# What `make build` reads: the build context, and the GO_VERSION build arg
# derived from go.mod. Only these leave the default image stale.
BUILD_CONTEXT = "internal/build/assets/"


def go_version(gomod):
    """GO_VERSION as the Makefile's awk derives it: the last toolchain line
    without its "go" prefix, else the first go line. None for no go.mod."""
    if gomod is None:
        return None
    toolchain = go = None
    for line in gomod.splitlines():
        fields = line.split()
        if len(fields) < 2:
            continue
        if fields[0] == "toolchain":
            toolchain = fields[1].removeprefix("go")
        elif fields[0] == "go" and go is None:
            go = fields[1]
    return toolchain or go


def image_stale(paths, go_versions=None):
    """Whether the change touches what `make build` reads. go_versions is
    GO_VERSION (before, after); None (no git to read it) counts any go.mod
    change."""
    if any(p.startswith(BUILD_CONTEXT) for p in paths):
        return True
    if "go.mod" not in paths:
        return False
    return go_versions is None or go_versions[0] != go_versions[1]


def renovate_pin():
    m = re.search(r"RENOVATE_VERSION:\s*['\"]?([^'\"\s]+)", (ROOT / ".github/workflows/ci.yml").read_text())
    return m.group(1) if m else "<RENOVATE_VERSION from ci.yml>"


class Gate(NamedTuple):
    matches: Callable[[str], bool]
    command: str
    ci: str
    note: str = ""
    # Printed instead of note when nothing `make build` reads changed.
    fresh_note: str = ""


# Every row is checked against every path; a command two rows name prints once.
CALLER_WORKFLOW = "the workflow that calls the script, on the next push"
ROWS = [
    Gate(lambda p: p.endswith(".go") or dockergate_pkg(p),
     "make go-check", "ci.yml (coverage-enforced test + lint)"),
    Gate(lambda p: p.startswith(".pi/"),
     "make check-agent", "ci.yml (lint)"),
    Gate(docker_ci_trigger,
     "make test", "docker-ci.yml (build + smoke)"),
    Gate(dockergate_pkg,
     "make go-dockergate", "docker-ci.yml (build + smoke + real-daemon gates)",
     "against the intended runtime image: after `make build`, pass FULL=<the tag it printed> (toolbox-local:build; a worktree: toolbox-local:<worktree>)",
     "nothing `make build` reads changed: the default image serves, no build needed"),
    Gate(lambda p: p == "renovate.json",
     f"npx --yes --package renovate@{renovate_pin()} renovate-config-validator renovate.json",
     "ci.yml (renovate-validate)", "pinned: unpinned `latest` has shipped an unfetchable tarball before"),
    Gate(lambda p: p == ".goreleaser.yaml",
     "goreleaser release --snapshot --clean --skip=publish,validate", "ci.yml (cask)",
     "then read dist/homebrew/Casks/toolbox.rb: `goreleaser check` renders no template"),
    Gate(lambda p: p.startswith(".github/workflows/"),
     "actionlint", "the workflow itself, on the next push"),
    Gate(lambda p: p.startswith(".github/scripts/"),
     "make check-devtools", CALLER_WORKFLOW),
    Gate(lambda p: p.startswith(".github/scripts/") and p.endswith(".sh"),
     "shellcheck .github/scripts/*.sh", CALLER_WORKFLOW,
     "make check-devtools shellchecks only the scripts it lists"),
    Gate(lambda p: p in (".github/scripts/host-share-git-rewrite.sh", "internal/build/assets/entrypoint.sh"),
     "make test-host-share", "none: no runner serves a Host Share", "from a Mac running Docker Desktop"),
    Gate(lambda p: p.startswith("internal/build/assets/") and p.endswith(".sh"),
     "find internal/build/assets -name '*.sh' ! -name 'zshrc.sh' -print0 | xargs -0 shellcheck -S error",
     "ci.yml (shellcheck)", "zshrc.sh is zsh, which shellcheck cannot parse"),
    Gate(lambda p: p.endswith(".md") or p.startswith(".claude/rules/") or p == "internal/build/assets/Dockerfile",
     "make go-test-one PKG=./internal/devrules", "ci.yml (test)",
     "devrules holds the docs map, the rule files and the Claude env-knob matrix to the files they describe"),
    # The Dockerfile is held by devrules, not by check-links: lychee reads
    # Markdown only.
    Gate(lambda p: p.endswith(".md") or p.startswith(".claude/rules/"),
     "make check-links", "docs.yml"),
]


def git_paths(repo, *args):
    # -z: no C-quoting of non-ASCII paths, which would hide their suffix.
    r = subprocess.run(["git", "-C", str(repo), *args, "-z"], capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f"pre-push-gates: git {' '.join(args)}: {r.stderr.strip()}")
    return [p for p in r.stdout.split("\0") if p]


def touched(repo, base):
    # --no-renames: a move lists both paths, so a file moved out of a gated
    # package still triggers that package's gate.
    paths = git_paths(repo, "diff", "--name-only", "--no-renames", f"{base}...HEAD")
    paths += git_paths(repo, "diff", "--name-only", "--no-renames", "HEAD")
    paths += git_paths(repo, "ls-files", "--others", "--exclude-standard")
    return sorted(set(paths))


def go_versions(repo, base):
    """GO_VERSION at the merge-base with base and in the working tree, the
    same two ends touched() diffs."""
    r = subprocess.run(["git", "-C", str(repo), "merge-base", base, "HEAD"], capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f"pre-push-gates: git merge-base {base} HEAD: {r.stderr.strip()}")
    old = subprocess.run(["git", "-C", str(repo), "show", f"{r.stdout.strip()}:go.mod"], capture_output=True, text=True)
    new = Path(repo, "go.mod")
    return (go_version(old.stdout if old.returncode == 0 else None),
            go_version(new.read_text() if new.exists() else None))


def gates(paths, stale=True):
    found = {}
    for row in ROWS:
        hits = [p for p in paths if row.matches(p)]
        if hits:
            note = row.fresh_note if row.fresh_note and not stale else row.note
            entry = found.setdefault(row.command, {"ci": row.ci, "note": note, "paths": []})
            entry["paths"] += [p for p in hits if p not in entry["paths"]]
    return found


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    # No --base default: the Makefile's BASE is the one place it is set.
    ap.add_argument("--base", help="ref the change is diffed against (merge-base, like CI)")
    ap.add_argument("--repo", default=str(ROOT), help="repository whose changes are read (tests)")
    ap.add_argument("--files", help="'-' reads touched paths from stdin instead of git")
    args = ap.parse_args()
    if args.files == "-":
        paths = sorted({line.strip() for line in sys.stdin if line.strip()})
        stale = image_stale(paths)
    elif args.base:
        paths = touched(args.repo, args.base)
        stale = image_stale(paths, go_versions(args.repo, args.base) if "go.mod" in paths else None)
    else:
        ap.error("--base is required unless --files - is given")
    found = gates(paths, stale)
    if not found:
        print("no gate in the table matches the touched paths")
        return
    for cmd, e in found.items():
        more = f" (+{len(e['paths']) - 1} more)" if len(e["paths"]) > 1 else ""
        print(cmd)
        print(f"    CI: {e['ci']}; touched: {e['paths'][0]}{more}")
        if e["note"]:
            print(f"    {e['note']}")


if __name__ == "__main__":
    main()
