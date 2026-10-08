#!/usr/bin/env bash
set -euo pipefail

valid_branch() {
    case "$1" in
        feat/?*|fix/?*) return 0 ;;
        renovate/?*) [ "$2" = 'renovate[bot]' ] ;;
        *) return 1 ;;
    esac
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
    echo "self-test OK"
}

case "${1:-}" in
    --self-test) self_test; exit $? ;;
    "") echo "usage: $0 <branch> <actor> <head-repository> <base-repository> | --self-test" >&2; exit 2 ;;
esac

branch=$1
actor=${2:-}
head_repository=${3:-}
base_repository=${4:-}
[ -n "$actor" ] && [ -n "$head_repository" ] && [ -n "$base_repository" ] || {
    echo "usage: $0 <branch> <actor> <head-repository> <base-repository> | --self-test" >&2
    exit 2
}

# Fork contributors do not control the source repository's branch policy.
[ "$head_repository" != "$base_repository" ] && exit 0

if ! valid_branch "$branch" "$actor"; then
    echo "::error::branch '$branch' must match feat/* or fix/*; Renovate owns renovate/*" >&2
    exit 1
fi
