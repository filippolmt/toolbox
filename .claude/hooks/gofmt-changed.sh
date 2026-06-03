#!/bin/sh
# Stop-hook formatter: keeps Claude-authored Go gofmt-clean once per turn.
#
# Why a Stop hook (not PostToolUse): Claude is the sole author of Go here.
# Formatting eagerly after every Write/Edit rewrites the file under Claude's
# feet -> its cached view goes stale -> the next Edit in the same turn fails
# the exact-string match and forces a wasted re-Read. Running once at turn end
# (after Claude has stopped editing) sidesteps that entirely.
#
# Why `golangci-lint fmt` (not raw gofmt): it applies the SAME formatter set
# as CI (.golangci.yml `formatters`, run by `make go-lint`). Raw gofmt would
# diverge the moment that list gains gofumpt/goimports, and nothing would
# catch the drift. The pinned version is read from the Makefile so there is
# one source of truth, not a third hard-coded copy.
#
# Host has no Go -> formatting goes through the golangci-lint container, same
# as the Makefile. TOOLBOX_HOST_WORKSPACE mirrors the Makefile's DooD path fix
# (the literal in-container path is not resolvable by the host daemon).
set -u

root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
cd "$root" || exit 0

# Skip the whole turn when no Go file is dirty/new -> zero docker cost on the
# common (non-Go) turn. --porcelain also catches untracked new files, which a
# plain `git diff` would miss.
[ -n "$(git status --porcelain -- '*.go' 2>/dev/null)" ] || exit 0

ver=$(sed -n 's#.*golangci/golangci-lint:\([^ ]*\).*#\1#p' Makefile | head -1)
[ -n "$ver" ] || exit 0

src=${TOOLBOX_HOST_WORKSPACE:-$root}

# `fmt ./...` runs only the configured formatters (no build, no type-check) and
# rewrites just the files that need it. Whole-module is robust: passing named
# files trips golangci-lint's "must all be in one directory" rule when a turn
# touches more than one package. Non-blocking: errors surface on stderr but the
# hook always exits 0 so it never wedges the turn.
out=$(docker run --rm -v "$src":/src -w /src \
	"golangci/golangci-lint:$ver" golangci-lint fmt ./... 2>&1) || true
[ -n "$out" ] && printf 'gofmt hook: %s\n' "$out" >&2

exit 0
