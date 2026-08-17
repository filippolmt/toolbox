#!/usr/bin/env bash
# Invalidation Floor gate — see docs/adr/0002-layer-ordering-by-invalidation-floor.md.
#
# Compares the layer set of two published manifests and fails when a change
# moved more substantial layers than a healthy bump should. Counts layers above
# MIN_BYTES rather than bytes: a GO_VERSION bump legitimately moves one big
# layer, while the regression this guards against moves many.
#
# Usage:
#   invalidation-floor.sh <baseline-index.json> <image-ref>
#   invalidation-floor.sh --self-test
#
# The self-test exercises the comparison on fixed data with no network, and the
# workflow runs it before every real comparison — a gate nobody has watched fire
# is a YAML file, not a gate.
set -euo pipefail

MAX_LAYERS="${MAX_LAYERS:-3}"
MIN_BYTES="${MIN_BYTES:-1048576}"
ARCH="${ARCH:-amd64}"

# Layers of the ARCH manifest inside an OCI index, as "<digest> <size>" lines.
# The attestation manifests carry vnd.docker.reference.type and are skipped.
layers_of() { # $1 = raw index JSON file, $2 = repo ref for the blob fetch
  local digest
  digest=$(jq -r --arg a "$ARCH" '.manifests[]
      | select(.platform.architecture == $a and .platform.os == "linux")
      | select(.annotations["vnd.docker.reference.type"] == null)
      | .digest' "$1" | head -1)
  [ -n "$digest" ] || return 1
  docker buildx imagetools inspect "${2}@${digest}" --raw \
    | jq -r '.layers[] | "\(.digest) \(.size)"'
}

# The pure half: compare two layer listings and report. Returns 1 when the
# number of moved layers above MIN_BYTES exceeds MAX_LAYERS.
verdict() { # $1 = old listing, $2 = new listing
  local moved n mb
  moved=$(mktemp)
  comm -23 <(sort "$2") <(sort "$1") > "$moved"
  n=$(awk -v m="$MIN_BYTES" '$2 > m' "$moved" | wc -l)
  mb=$(awk '{s += $2} END {printf "%.0f", s / 1048576}' "$moved")
  echo "Moved $(wc -l < "$moved") layer(s) of $(wc -l < "$2"), ${n} above $((MIN_BYTES / 1048576)) MB, ${mb} MB total."
  rm -f "$moved"
  if [ "$n" -gt "$MAX_LAYERS" ]; then
    echo "::error::Invalidation Floor regression: ${n} substantial layers moved (max ${MAX_LAYERS}), ${mb} MB pushed to every puller. See docs/adr/0002-layer-ordering-by-invalidation-floor.md"
    return 1
  fi
}

self_test() {
  local dir base healthy regression
  dir=$(mktemp -d); trap 'rm -rf "$dir"' RETURN
  base="$dir/base"; healthy="$dir/healthy"; regression="$dir/regression"
  # Four big layers and two tiny ones. Four, not three: MAX_LAYERS is a `-gt`
  # bound, so a regression fixture sitting exactly on the threshold would pass
  # and the self-test would assert nothing.
  printf 'sha256:aaa 200000000\nsha256:bbb 100000000\nsha256:ccc 50000000\nsha256:ddd 40000000\nsha256:eee 20000\nsha256:fff 20000\n' > "$base"
  # Healthy: both tiny layers replaced — several layers move, none of them big.
  printf 'sha256:aaa 200000000\nsha256:bbb 100000000\nsha256:ccc 50000000\nsha256:ddd 40000000\nsha256:ggg 20000\nsha256:hhh 20000\n' > "$healthy"
  # Regression: every big layer replaced too.
  printf 'sha256:iii 200000000\nsha256:jjj 100000000\nsha256:kkk 50000000\nsha256:lll 40000000\nsha256:ggg 20000\nsha256:hhh 20000\n' > "$regression"

  verdict "$base" "$healthy"    >/dev/null || { echo "self-test FAILED: healthy change rejected"; return 1; }
  verdict "$base" "$regression" >/dev/null && { echo "self-test FAILED: regression accepted"; return 1; }
  verdict "$base" "$base"       >/dev/null || { echo "self-test FAILED: identical images rejected"; return 1; }
  echo "self-test OK (healthy passes, regression fails, identical passes)"
}

case "${1:-}" in
  --self-test) self_test; exit $? ;;
  "") echo "usage: $0 <baseline-index.json> <image-ref> | --self-test" >&2; exit 2 ;;
esac

baseline="$1"
ref="$2"

# A missing or unusable baseline is not a failure: the first publish of a repo
# has no previous :latest, and a half-written file from an interrupted inspect
# should not redden a build it says nothing about.
if [ ! -s "$baseline" ] || ! jq -e . "$baseline" >/dev/null 2>&1; then
  echo "::notice::No usable baseline manifest — nothing to compare."
  exit 0
fi

work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
docker buildx imagetools inspect "$ref" --raw > "$work/current.json"
layers_of "$baseline" "${ref%%:*}" > "$work/old.txt"
layers_of "$work/current.json" "${ref%%:*}" > "$work/new.txt"
verdict "$work/old.txt" "$work/new.txt"
