"""Estimate coverage of changed executable Go lines; not a SonarQube replacement."""

import argparse
from pathlib import Path
import re
import subprocess
import sys


def git(*args):
    return subprocess.check_output(
        ["git", "-c", f"safe.directory={Path.cwd()}", *args], text=True
    )


def changed_lines(base):
    ancestor = git("merge-base", base, "HEAD").strip()
    tracked = set(git("diff", "--name-only", "--diff-filter=ACMRT", "-z", ancestor, "--", "*.go").split("\0"))
    untracked = set(git("ls-files", "--others", "--exclude-standard", "-z", "--", "*.go").split("\0"))
    changed = {}
    for name in sorted(tracked | untracked):
        if not name or name.endswith("_test.go"):
            continue
        if name in untracked:
            changed[name] = set(range(1, len(Path(name).read_text().splitlines()) + 1))
            continue
        patch = git("diff", "--no-ext-diff", "--no-textconv", "--unified=0", ancestor, "--", name)
        lines = set()
        for start, count in re.findall(r"^@@ -[^ ]+ \+(\d+)(?:,(\d+))? @@", patch, re.MULTILINE):
            start, count = int(start), int(count or 1)
            lines.update(range(start, start + count))
        changed[name] = lines
    return changed


def estimate(profile, changed):
    module = re.search(r"^module\s+(\S+)", Path("go.mod").read_text(), re.MULTILINE)
    if module is None:
        raise ValueError("go.mod has no module directive")
    prefix = module[1].strip('"') + "/"
    records = Path(profile).read_text().splitlines()
    if not records or records[0] not in ("mode: atomic", "mode: set", "mode: count"):
        raise ValueError("invalid coverage profile header")
    executable, covered, represented = set(), set(), set()
    for record in records[1:]:
        location, statements, hits = record.rsplit(" ", 2)
        name, coordinates = location.rsplit(":", 1)
        if not name.startswith(prefix):
            continue
        name = name[len(prefix):]
        represented.add(name)
        start, end = coordinates.split(",")
        first = int(start.split(".")[0])
        last, column = map(int, end.split("."))
        statements, hits = int(statements), int(hits)
        if statements < 0 or hits < 0:
            raise ValueError("negative coverage counts")
        if statements == 0:
            continue
        for line in changed.get(name, set()) & set(range(first, last + (column > 1))):
            executable.add((name, line))
            if hits:
                covered.add((name, line))
    missing = sorted(name for name, lines in changed.items() if lines and name not in represented)
    return len(covered), len(executable), missing


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("profile")
    parser.add_argument("--base", default="origin/main")
    parser.add_argument("--minimum", type=float, required=True)
    parser.add_argument("--warn-only", action="store_true")
    args = parser.parse_args()
    if not 0 <= args.minimum <= 100:
        parser.error("minimum must be between zero and one hundred")
    try:
        covered, total, missing = estimate(args.profile, changed_lines(args.base))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Cannot estimate changed-line coverage: {error}", file=sys.stderr)
        return 2
    if missing:
        print(f"WARNING: no coverage records for {', '.join(missing)}; estimate is incomplete.")
        if not args.warn_only:
            return 2
    if not total:
        print("No changed executable Go lines found in the coverage profile.")
        return 0
    percentage = covered * 100 / total
    print(f"Changed-line coverage estimate: {covered}/{total} ({percentage:.1f}%); floor {args.minimum:g}%")
    if percentage < args.minimum:
        print("WARNING: add tests for changed Go code; SonarQube remains authoritative.")
        return 0 if args.warn_only else 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
