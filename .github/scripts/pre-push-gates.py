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
DOCKERGATE_PKGS = ("container", "mountplan", "sessionplan", "reload", "imagereclaim")


def renovate_pin():
    m = re.search(r"RENOVATE_VERSION:\s*['\"]?([^'\"\s]+)", (ROOT / ".github/workflows/ci.yml").read_text())
    return m.group(1) if m else "<RENOVATE_VERSION from ci.yml>"


# (matches, command, CI job, note). Every row is checked against every path; a
# command two rows name prints once.
ROWS = [
    (lambda p: p.endswith(".go"),
     "make go-check", "ci.yml (coverage-enforced test + lint)", ""),
    (lambda p: p.startswith(".pi/"),
     "make check-agent", "ci.yml (lint)", ""),
    (lambda p: p.startswith("internal/build/assets/") or p == "go.mod",
     "make test", "docker-ci.yml (build + smoke)", ""),
    (lambda p: any(p.startswith(f"internal/{d}/") for d in DOCKERGATE_PKGS),
     "make go-dockergate", "docker-ci.yml (real-daemon gates)",
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
     "make check-devtools", "the workflow that calls the script, on the next push",
     "plus `shellcheck` on any changed shell script"),
    (lambda p: p in (".github/scripts/host-share-git-rewrite.sh", "internal/build/assets/entrypoint.sh"),
     "make test-host-share", "none: no runner serves a Host Share", "from a Mac running Docker Desktop"),
    (lambda p: p.startswith("internal/build/assets/") and p.endswith(".sh"),
     "find internal/build/assets -name '*.sh' ! -name 'zshrc.sh' -print0 | xargs -0 shellcheck -S error",
     "ci.yml (shellcheck)", "zshrc.sh is zsh, which shellcheck cannot parse"),
    (lambda p: p.endswith(".md") or p.startswith(".claude/rules/") or p == "internal/build/assets/Dockerfile",
     "make go-test-one PKG=./internal/devrules", "ci.yml (test)",
     "devrules holds the docs map, the rule files and the Claude env-knob matrix to the files they describe"),
    (lambda p: p.endswith(".md") or p.startswith(".claude/rules/"),
     "make check-links", "docs.yml", ""),
]


def git_lines(*args):
    out = subprocess.run(["git", "-C", str(ROOT), *args], capture_output=True, text=True, check=True).stdout
    return [line for line in out.splitlines() if line]


def touched(base):
    paths = git_lines("diff", "--name-only", f"{base}...HEAD")
    paths += git_lines("diff", "--name-only", "HEAD")
    paths += git_lines("ls-files", "--others", "--exclude-standard")
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
        print("no gate: nothing a CI job checks was touched")
        return
    for cmd, e in found.items():
        more = f" (+{len(e['paths']) - 1} more)" if len(e["paths"]) > 1 else ""
        print(cmd)
        print(f"    CI: {e['ci']}; touched: {e['paths'][0]}{more}")
        if e["note"]:
            print(f"    {e['note']}")


if __name__ == "__main__":
    main()
