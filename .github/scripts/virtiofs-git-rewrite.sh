#!/usr/bin/env bash
# End-to-end regression gate for Git rewrites on a Docker Desktop virtiofs bind.
# Usage: virtiofs-git-rewrite.sh <image-tag>
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <image-tag>" >&2
  exit 2
fi

image=$1
# Keep the bind source under the checked-out workspace unless Actions exposes a
# runner temp directory. Both paths are visible to the host daemon when this
# gate is launched from inside a toolbox container (Docker-outside-of-Docker).
bind_root=${RUNNER_TEMP:-$PWD}
worktree=$(mktemp -d "$bind_root/.toolbox-virtiofs-git.XXXXXX")
container="toolbox-virtiofs-git-$$"
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf "$worktree"
}
trap cleanup EXIT INT TERM

# Run as the host user, as toolbox does, so every file remains removable by the
# runner. The repository is created inside the bind: the operation must exercise
# Docker Desktop's real file-sharing path rather than the container overlay.
docker run --rm --name "$container" \
  --user "$(id -u):$(id -g)" \
  --mount "type=bind,source=$worktree,destination=/workspace" \
  --workdir /workspace \
  "$image" bash -euc '
    if [ "$(findmnt -n -o FSTYPE --target "$PWD")" != virtiofs ]; then
      echo "virtiofs gate requires a Docker Desktop virtiofs workspace bind" >&2
      exit 1
    fi
    if [ "$(git config --system --get core.checkStat)" != minimal ]; then
      echo "entrypoint did not activate core.checkStat=minimal on virtiofs" >&2
      exit 1
    fi

    git init -q -b main
    git config user.name "Toolbox CI"
    git config user.email "toolbox-ci@example.invalid"
    git config core.hooksPath /dev/null

    mkdir files
    for n in $(seq 1 200); do
      file=$(printf "files/file-%03d.txt" "$n")
      for line in $(seq 1 40); do
        printf "base line %s\n" "$line"
      done > "$file"
    done
    git add files
    git commit -qm base
    git branch feature

    for file in files/*.txt; do
      sed -i "2s/base/main/" "$file"
    done
    git commit -qam "rewrite shared files on main"

    git switch -q feature
    for file in files/*.txt; do
      sed -i "30s/base/feature-one/" "$file"
    done
    git commit -qam "first feature rewrite"
    for file in files/*.txt; do
      sed -i "35s/base/feature-two/" "$file"
    done
    git commit -qam "second feature rewrite"

    test -z "$(git status --porcelain)" || {
      echo "worktree is dirty before rebase" >&2
      git status --short >&2
      exit 1
    }

    set +e
    output=$(git rebase main 2>&1)
    status=$?
    set -e
    if [ "$status" -ne 0 ]; then
      printf "%s\n" "$output" >&2
      if printf "%s\n" "$output" | grep -q "would be overwritten by merge"; then
        echo "virtiofs regression: Git rejected a clean worktree as locally modified" >&2
      else
        echo "git rebase failed for an unexpected reason" >&2
      fi
      exit "$status"
    fi

    git merge-base --is-ancestor main HEAD
    grep -qx "main line 2" files/file-001.txt
    grep -qx "feature-one line 30" files/file-001.txt
    grep -qx "feature-two line 35" files/file-001.txt
    test -z "$(git status --porcelain)" || {
      echo "worktree is dirty after rebase" >&2
      git status --short >&2
      exit 1
    }
    echo "virtiofs Git rewrite gate passed"
  '
