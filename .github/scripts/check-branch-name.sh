#!/usr/bin/env bash
set -euo pipefail

rule="must match feat/* or fix/*; Renovate owns renovate/*"

valid_branch() {
    case "$1" in
        feat/?*|fix/?*) return 0 ;;
        renovate/?*) [ "$2" = 'renovate[bot]' ] ;;
        *) return 1 ;;
    esac
}

# The branch a shell command creates (`git switch -c`, `checkout -b`,
# `worktree add -b`), or nothing.
created_branch() {
    grep -oE '\bgit( +-C +[^ ]+)? +(switch|checkout|worktree +add)[^;&|]* +-[cCbB] +[^ ;&|]+' <<<"$1" \
        | tail -n 1 | awk '{print $NF}' || true
}

# Claude Code PreToolUse hook on Bash: refuses to create or push a branch CI
# would reject, before the round trip through a red PR. Fails open when the
# input or jq is missing.
# shortcut: a push reads the branch from the hook's cwd, not from `git -C`
# or a refspec; widen it if pushes from another tree show up.
hook() {
    local input cmd branch
    command -v jq >/dev/null || return 0
    input=$(cat)
    cmd=$(jq -r '.tool_input.command // empty' <<<"$input") || return 0
    branch=$(created_branch "$cmd")
    if [ -z "$branch" ] && grep -qE '\bgit( +-C +[^ ]+)? +push\b' <<<"$cmd"; then
        branch=$(git -C "$(jq -r '.cwd // "."' <<<"$input")" branch --show-current 2>/dev/null) || return 0
    fi
    [ -n "$branch" ] || return 0
    valid_branch "$branch" human && return 0
    echo "branch '$branch' $rule; a dependency bump takes fix/ (.github/scripts/check-branch-name.sh, run by ci.yml)" >&2
    return 2
}

self_test() {
    local branch
    for branch in feat/example fix/example; do
        valid_branch "$branch" human || { echo "self-test FAILED: rejected $branch" >&2; return 1; }
    done
    valid_branch renovate/example 'renovate[bot]' || { echo "self-test FAILED: rejected Renovate" >&2; return 1; }
    for branch in main feat/ fix/ refactor/example renovate/example; do
        if valid_branch "$branch" human; then
            echo "self-test FAILED: accepted $branch" >&2
            return 1
        fi
    done
    local cmd want got
    while IFS='|' read -r cmd want; do
        got=$(created_branch "$cmd")
        [ "$got" = "$want" ] || { echo "self-test FAILED: '$cmd' created '$got', want '$want'" >&2; return 1; }
    done <<'EOF'
git switch -c chore/x|chore/x
git checkout -b fix/y origin/main|fix/y
git fetch -q && git switch -q -c feat/z origin/main && echo ok|feat/z
git -C /repo worktree add ../wt -b fix/w|fix/w
git switch main|
git push -u origin fix/y|
EOF
    echo "self-test OK"
}

case "${1:-}" in
    --self-test) self_test; exit $? ;;
    --hook) hook; exit $? ;;
    "") echo "usage: $0 <branch> <actor> <head-repository> <base-repository> | --self-test | --hook" >&2; exit 2 ;;
esac

branch=$1
actor=${2:-}
head_repository=${3:-}
base_repository=${4:-}
[ -n "$actor" ] && [ -n "$head_repository" ] && [ -n "$base_repository" ] || {
    echo "usage: $0 <branch> <actor> <head-repository> <base-repository> | --self-test | --hook" >&2
    exit 2
}

# Fork contributors do not control the source repository's branch policy.
[ "$head_repository" != "$base_repository" ] && exit 0

if ! valid_branch "$branch" "$actor"; then
    echo "::error::branch '$branch' $rule" >&2
    exit 1
fi
