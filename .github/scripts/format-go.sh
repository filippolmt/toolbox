#!/usr/bin/env bash
set -euo pipefail

# Git selection comes from the host: linked-worktree metadata is not mounted.
while IFS= read -r -d '' file; do
  if [[ -f $file && ! -L $file ]]; then
    printf '%s\0' "$file"
  fi
done | xargs -0 sh -c 'if [ "$#" -gt 0 ]; then exec gofmt -w -- "$@"; fi' gofmt
