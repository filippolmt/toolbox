#!/usr/bin/env python3
"""Report the Claude Code env vars a CLAUDE_CODE_VERSION bump touches.

Reads the upstream CHANGELOG.md sections after OLD up to and including NEW
and prints a Markdown report of:
- every env var those lines name outside the privacy matrix, marked when the
  env-vars reference has a row for it (rows carry no anchor, so the link is
  the variables table);
- every line naming a variable the privacy matrix mentions (set by the image
  or deliberately left unset), since a change there can invalidate a decision.

Prints nothing when the range names no env var, or when NEW is not above OLD
(a downgrade or revert has nothing new to read). Exits 1 with the reason on
stderr whenever it cannot tell: upstream unreadable, NEW's changelog section
not published yet, or a source no longer in the shape parsed here, so CI
never mistakes "could not look" for "nothing new".

Usage: claude-env-changes.py OLD NEW [--changelog FILE] [--reference FILE]
       [--matrix FILE]   (the URLs below are fetched when a FILE is omitted)
"""
import argparse
import re
import sys
import urllib.request
from pathlib import Path

CHANGELOG_URL = "https://raw.githubusercontent.com/anthropics/claude-code/main/CHANGELOG.md"
REFERENCE_URL = "https://code.claude.com/docs/en/env-vars.md"
REFERENCE_PAGE = "https://code.claude.com/docs/en/env-vars#variables"
MATRIX = Path(__file__).resolve().parents[2] / "docs/internals/privacy.md"
MATRIX_HEADING = "## Claude Code env-var matrix"
# Marks the report so the workflow finds and updates its own comment.
MARKER = "<!-- claude-env-changes -->"
# A PR comment holds 65536 characters; a many-release jump can outgrow it.
COMMENT_MAX = 60000

# A backticked env var, with or without a value: `FOO_BAR`, `FOO_BAR=1`. The
# underscore keeps out backticked acronyms such as `API`.
ENV_VAR = re.compile(r"`([A-Z][A-Z0-9]*_[A-Z0-9_]+)(?:=[^`]*)?`")
SECTION = re.compile(r"^## (\d+(?:\.\d+)+)\s*$", re.M)
VERSION = re.compile(r"\d+(?:\.\d+)+")
REFERENCE_ROW = "| `"


class Unreadable(Exception):
    """A source this script cannot read with confidence."""


def version(v):
    return tuple(int(p) for p in v.split("."))


def changelog_lines(changelog, old, new):
    """(version, line) for each bullet in the sections old < v <= new."""
    lo, hi = version(old), version(new)
    heads = list(SECTION.finditer(changelog))
    if lo < hi and new not in (m.group(1) for m in heads):
        raise Unreadable(f"the changelog has no section for {new} yet")
    out = []
    for i, m in enumerate(heads):
        if not lo < version(m.group(1)) <= hi:
            continue
        end = heads[i + 1].start() if i + 1 < len(heads) else len(changelog)
        for line in changelog[m.end():end].splitlines():
            if line.startswith("- "):
                out.append((m.group(1), line[2:].strip()))
    return out


def matrix_vars(matrix_md):
    """Every env var the matrix section names, bullets and prose alike."""
    _, sep, section = matrix_md.partition(MATRIX_HEADING)
    if not sep:
        raise Unreadable(f"privacy.md has no {MATRIX_HEADING!r}")
    return set(ENV_VAR.findall(section.split("\n## ", 1)[0]))


def report(changelog, old, new, reference, matrix):
    """The Markdown report, or "" when the range names no env var."""
    if REFERENCE_ROW not in reference:
        raise Unreadable("the env-vars reference has no variable rows; format changed?")

    def mark(var):
        listed = f"| `{var}` |" in reference
        return f"`{var}` ({f'[reference]({REFERENCE_PAGE})' if listed else 'not in the reference'})"

    named = {}  # var -> first (version, line)
    touched = []  # (version, line, matrix vars it names)
    for ver, line in changelog_lines(changelog, old, new):
        found = ENV_VAR.findall(line)
        for var in found:
            named.setdefault(var, (ver, line))
        if hit := sorted(set(found) & matrix):
            touched.append((ver, line, hit))
    if not named:
        return ""

    out = [MARKER, f"### Claude Code env vars in {old} → {new}", ""]
    if unlisted := sorted(v for v in named if v not in matrix):
        out += ["Named in the changelog, not in the `docs/internals/privacy.md` matrix:", ""]
        out += [f"- {mark(var)} — {named[var][0]}: {named[var][1]}" for var in unlisted]
        out.append("")
    if touched:
        out += ["Lines naming a variable the matrix covers — check the decision still holds:", ""]
        out += [f"- {', '.join(map(mark, hit))} — {ver}: {line}" for ver, line, hit in touched]
        out.append("")
    text = "\n".join(out)
    if len(text) > COMMENT_MAX:
        text = text[:COMMENT_MAX] + "\n\n…truncated: read the changelog for the rest.\n"
    return text


def read(path, url):
    try:
        if path:
            return Path(path).read_text()
        # The docs host answers 403 to urllib's default User-Agent.
        req = urllib.request.Request(url, headers={"User-Agent": "claude-env-changes"})
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.read().decode()
    except OSError as e:
        raise Unreadable(f"cannot read {path or url}: {e}") from e


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("old")
    ap.add_argument("new")
    ap.add_argument("--changelog")
    ap.add_argument("--reference")
    ap.add_argument("--matrix", default=str(MATRIX))
    args = ap.parse_args()
    try:
        for v in (args.old, args.new):
            if not VERSION.fullmatch(v):
                raise Unreadable(f"{v!r} is not a dotted version")
        changelog = read(args.changelog, CHANGELOG_URL)
        if not SECTION.search(changelog):
            raise Unreadable("the changelog has no version sections; format changed?")
        text = report(changelog, args.old, args.new, read(args.reference, REFERENCE_URL),
                      matrix_vars(read(args.matrix, "")))
    except Unreadable as e:
        sys.exit(f"claude-env-changes: {e}")
    sys.stdout.write(text)


if __name__ == "__main__":
    main()
