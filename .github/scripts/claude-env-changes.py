#!/usr/bin/env python3
"""Report the Claude Code env vars a CLAUDE_CODE_VERSION bump touches.

Reads the upstream CHANGELOG.md sections after OLD up to and including NEW
and prints a Markdown report of:
- every env var those lines name, marked when the env-vars reference lists it;
- every line naming a variable the privacy matrix mentions (set by the image
  or deliberately left unset), since a change there can invalidate a decision.

Prints nothing when the range names no env var. A fetch failure exits 2 with
the reason on stderr, so CI can tell "nothing new" from "could not look".

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
COMMENT_MAX = 60000

# A backticked env var, with or without a value: `FOO_BAR`, `FOO_BAR=1`. The
# underscore keeps out backticked acronyms such as `API`.
ENV_VAR = re.compile(r"`([A-Z][A-Z0-9]*_[A-Z0-9_]+)(?:=[^`]*)?`")
SECTION = re.compile(r"^## (\d+(?:\.\d+)+)\s*$", re.M)


def version(v):
    return tuple(int(p) for p in v.split("."))


def changelog_lines(changelog, old, new):
    """(version, line) for each bullet in the sections old < v <= new."""
    lo, hi = version(old), version(new)
    heads = list(SECTION.finditer(changelog))
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
        raise ValueError(f"privacy.md has no {MATRIX_HEADING!r}")
    section = section.split("\n## ", 1)[0]
    return set(ENV_VAR.findall(section))


def report(changelog, old, new, reference, matrix):
    """The Markdown report, or "" when the range names no env var."""
    named = {}  # var -> first (version, line)
    touched = []  # (version, line, matrix vars it names)
    for ver, line in changelog_lines(changelog, old, new):
        found = ENV_VAR.findall(line)
        for var in found:
            named.setdefault(var, (ver, line))
        hit = sorted(set(found) & matrix)
        if hit:
            touched.append((ver, line, hit))
    if not named:
        return ""

    out = [MARKER, f"### Claude Code env vars in {old} → {new}", ""]
    new_vars = sorted(v for v in named if v not in matrix)
    if new_vars:
        out += ["Named in the changelog, not in the `docs/internals/privacy.md` matrix:", ""]
        for var in new_vars:
            ver, line = named[var]
            ref = f"[reference]({REFERENCE_PAGE})" if f"| `{var}` |" in reference else "not in the reference"
            out.append(f"- `{var}` ({ref}) — {ver}: {line}")
        out.append("")
    if touched:
        out += ["Lines naming a variable the matrix covers — check the decision still holds:", ""]
        out += [f"- {', '.join(f'`{v}`' for v in hit)} — {ver}: {line}" for ver, line, hit in touched]
        out.append("")
    text = "\n".join(out)
    # A PR comment holds 65536 characters; a many-release jump can outgrow it.
    if len(text) > COMMENT_MAX:
        text = text[:COMMENT_MAX] + "\n\n…truncated: read the changelog for the rest.\n"
    return text


def read(path, url):
    if path:
        return Path(path).read_text()
    with urllib.request.urlopen(url, timeout=30) as r:
        return r.read().decode()


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("old")
    ap.add_argument("new")
    ap.add_argument("--changelog")
    ap.add_argument("--reference")
    ap.add_argument("--matrix", default=str(MATRIX))
    a = ap.parse_args()
    try:
        changelog = read(a.changelog, CHANGELOG_URL)
        reference = read(a.reference, REFERENCE_URL)
    except OSError as e:
        print(f"claude-env-changes: cannot fetch upstream: {e}", file=sys.stderr)
        sys.exit(2)
    if not SECTION.search(changelog):
        print("claude-env-changes: changelog has no version sections; format changed?", file=sys.stderr)
        sys.exit(2)
    sys.stdout.write(report(changelog, a.old, a.new, reference, matrix_vars(Path(a.matrix).read_text())))


if __name__ == "__main__":
    main()
