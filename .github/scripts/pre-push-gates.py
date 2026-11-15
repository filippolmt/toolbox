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


def renovate_pin():
    m = re.search(r"RENOVATE_VERSION:\s*['\"]?([^'\"\s]+)", (ROOT / ".github/workflows/ci.yml").read_text())
    return m.group(1) if m else "<RENOVATE_VERSION from ci.yml>"


# (matches, command, CI job, note). Every row is checked against every path; a
# command two rows name prints once.
ROWS = [
    (lambda p: p.endswith(".go") or dockergate_pkg(p),
     "make go-check", "ci.yml (coverage-enforced test + lint)", ""),
    (lambda p: p.startswith(".pi/"),
     "make check-agent", "ci.yml (lint)", ""),
    (lambda p: matches(p, DOCKER_PATHS) and not dockergate_pkg(p),
     "make test", "docker-ci.yml (build + smoke)", ""),
    (dockergate_pkg,
     "make go-dockergate", "docker-ci.yml (build + smoke + real-daemon gates)",
     "against the intended runtime image: after `make build`, pass FULL=toolbox-local:build"),
    (lambda p: p == "renovate.json",
     f"npx --yes --package renovate@{renovate_pin()} renovate-config-validator renovate.json",
     "ci.yml (renovate-validate)", "pinned: unpinned `latest` has shipped an unfetchable tarball before"),
    (lambda p: p == ".goreleaser.yaml",
     "goreleaser release --snapshot --clean --skip=publish,validate", "ci.yml (cask)",
     "then read dist/homebrew/Casks/toolbox.rb: `goreleaser check` renders no template"),
    (lambda p: p.startswith(".github/workflows/"),
     "actionlint", "the workflow itself, on the next push", ""),
    (lambda p: p.startswith(".github/scripts/"),
     "make check-devtools", "the workflow that calls the script, on the next push", ""),
    (lambda p: p.startswith(".github/scripts/") and p.endswith(".sh"),
     "shellcheck .github/scripts/*.sh", "the workflow that calls the script, on the next push",
     "make check-devtools shellchecks only two of them"),
    (lambda p: p in (".github/scripts/host-share-git-rewrite.sh", "internal/build/assets/entrypoint.sh"),
     "make test-host-share", "none: no runner serves a Host Share", "from a Mac running Docker Desktop"),
    (lambda p: p.startswith("internal/build/assets/") and p.endswith(".sh"),
     "find internal/build/assets -name '*.sh' ! -name 'zshrc.sh' -print0 | xargs -0 shellcheck -S error",
     "ci.yml (shellcheck)", "zshrc.sh is zsh, which shellcheck cannot parse"),
    (lambda p: p.endswith(".md") or p.startswith(".claude/rules/") or p == "internal/build/assets/Dockerfile",
     "make go-test-one PKG=./internal/devrules", "ci.yml (test)",
     "devrules holds the docs map, the rule files and the Claude env-knob matrix to the files they describe"),
    # The Dockerfile is held by devrules, not by check-links: lychee reads
    # Markdown only.
    (lambda p: p.endswith(".md") or p.startswith(".claude/rules/"),
     "make check-links", "docs.yml", ""),
]


def git_paths(*args):
    # -z: no C-quoting of non-ASCII paths, which would hide their suffix.
    r = subprocess.run(["git", "-C", str(ROOT), *args, "-z"], capture_output=True, text=True)
    if r.returncode != 0:
        sys.exit(f"pre-push-gates: git {' '.join(args)}: {r.stderr.strip()}")
    return [p for p in r.stdout.split("\0") if p]


def touched(base):
    # --no-renames: a move lists both paths, so a file moved out of a gated
    # package still triggers that package's gate.
    paths = git_paths("diff", "--name-only", "--no-renames", f"{base}...HEAD")
    paths += git_paths("diff", "--name-only", "--no-renames", "HEAD")
    paths += git_paths("ls-files", "--others", "--exclude-standard")
    return sorted(set(paths))


def gates(paths):
    found = {}
    for match, cmd, ci, note in ROWS:
        hits = [p for p in paths if match(p)]
        if hits:
            entry = found.setdefault(cmd, {"ci": ci, "note": note, "paths": []})
            entry["paths"] += [p for p in hits if p not in entry["paths"]]
    return found


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--base", default="origin/main")
    ap.add_argument("--files", help="'-' reads touched paths from stdin instead of git")
    args = ap.parse_args()
    paths = sorted({l.strip() for l in sys.stdin if l.strip()}) if args.files == "-" else touched(args.base)
    found = gates(paths)
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
