"""Select complete changed Go functions for golangci-lint's new-issue filter."""

import argparse
from pathlib import Path
import re
import subprocess
import sys


def expand_context(patch):
    active = False
    for line in patch.splitlines():
        if line.startswith("diff --git "):
            active = False
        if line.startswith("@@ "):
            match = re.search(r"\+(\d+)(?:,(\d+))? @@", line)
            if match is None:
                raise ValueError("invalid function-context diff")
            yield f"@@ -0,0 +{match[1]},{match[2] or 1} @@"
            active = True
        elif active:
            # Context becomes selected code, not a real patch to apply. This
            # includes the declaration where gocognit reports a body-only edit.
            if line.startswith((" ", "+")):
                yield "+" + line[1:]
        else:
            yield line


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", default="origin/main")
    args = parser.parse_args()
    git = ["git", "-c", f"safe.directory={Path.cwd()}"]
    options = ["--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/"]
    try:
        base = subprocess.check_output([*git, "merge-base", args.base, "HEAD"], text=True).strip()
        patch = subprocess.check_output([*git, "diff", *options, "-U0", "-W", base, "--", "*.go", ":(exclude)*_test.go"], text=True)
        print("\n".join(expand_context(patch)))
        untracked = subprocess.check_output([*git, "ls-files", "--others", "--exclude-standard", "-z", "--", "*.go"], text=True)
        for name in untracked.split("\0"):
            if not name or name.endswith("_test.go"):
                continue
            result = subprocess.run([*git, "diff", "--no-index", *options, "--", "/dev/null", name], text=True, capture_output=True)
            if result.returncode not in (0, 1):
                raise ValueError(result.stderr.strip())
            print(result.stdout, end="")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Cannot select changed Go functions: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    sys.exit(main())
