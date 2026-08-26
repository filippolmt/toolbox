#!/usr/bin/env bash
# Invalidation Floor gate — see docs/adr/0002-layer-ordering-by-invalidation-floor.md.
#
# Compares the layer set of two published manifests and fails when a change
# moved more substantial layers than a healthy bump should. Counts layers above
# MIN_BYTES rather than bytes: a GO_VERSION bump legitimately moves one big
# layer, while the regression this guards against moves many.
#
# Not every moved layer measures ordering. An unpinned `apt-get install` in the
# final stage moves whenever the Debian archive publishes something new, and
# takes every RUN beneath it along — Archive Drift, in CONTEXT.md. The canary in
# verdict() tells the two apart; follow-up 3 of the ADR has the measurements.
#
# Usage:
#   invalidation-floor.sh <baseline-index.json> <image-ref>
#   invalidation-floor.sh --self-test
#
# The self-test exercises the comparison on fixed data with no network, and the
# workflow runs it before every real comparison — a gate nobody has watched fire
# is a YAML file, not a gate.
set -euo pipefail

# Calibration lives here rather than in the workflow, so there is one literal to
# change instead of two. 6 is the bounded cost of the fifth-most-bumped tool in
# the frequency-ordered tail; the coverage arithmetic, and why a scalar bound
# cannot do better, are in ADR 0002's second follow-up. An env override still
# wins, which is what lets the loop below be exercised at another bound.
MAX_LAYERS="${MAX_LAYERS:-6}"
MIN_BYTES="${MIN_BYTES:-1048576}"
ARCH="${ARCH:-amd64}"

# Digest of the ARCH manifest inside an OCI index. The attestation manifests
# carry vnd.docker.reference.type and are skipped.
arch_digest() { # $1 = raw index JSON file
  jq -r --arg a "$ARCH" '.manifests[]
      | select(.platform.architecture == $a and .platform.os == "linux")
      | select(.annotations["vnd.docker.reference.type"] == null)
      | .digest' "$1" | head -1
}

# Layer listing as "<digest> <size> <kind>" lines. The kind is what the layer's
# instruction does to the layers below it: a RUN is invalidated by anything
# above it, a COPY --link is not. `argrun` is a RUN with build args in scope —
# the `|N` prefix docker history prints — which the final stage's own RUNs carry
# and the base image's do not, so the first `argrun` is the final stage's apt
# layer. Returns non-zero when either half is unreadable; the caller passes.
layers_of() { # $1 = raw index JSON file, $2 = repo ref, $3 = output file
  local digest ref
  digest=$(arch_digest "$1")
  [ -n "$digest" ] || return 1
  ref="${2}@${digest}"
  docker buildx imagetools inspect "$ref" --raw \
    | jq -r '.layers[] | "\(.digest) \(.size)"' > "${3}.layers" || return 1
  docker buildx imagetools inspect "$ref" --format '{{json .Image}}' \
    | jq -r '.history[] | select(.empty_layer != true) | .created_by
             | if startswith("COPY") then "copy"
               elif startswith("RUN |") then "argrun"
               else "run" end' > "${3}.kinds" || return 1
  [ -s "${3}.layers" ] || return 1
  [ "$(wc -l < "${3}.layers")" = "$(wc -l < "${3}.kinds")" ] || return 1
  paste -d' ' "${3}.layers" "${3}.kinds" > "$3"
}

# The pure half: compare two layer listings and report. Returns 1 when the
# number of counted layers above MIN_BYTES exceeds MAX_LAYERS.
verdict() { # $1 = old listing, $2 = new listing
  local moved canary n excused=0 drift="" mb line
  moved=$(mktemp)
  comm -23 <(sort "$2") <(sort "$1") > "$moved"
  mb=$(awk '{s += $2} END {printf "%.0f", s / 1048576}' "$moved")

  # The final stage's first RUN is its unpinned apt layer — the one instruction
  # no version bump can reach, held there by TestFinalStageFirstRUNHasNoVersionARG.
  # If it moved, the archive moved, and every RUN below it moved for that reason
  # rather than for anything in the diff. Only the --link COPYs still measure
  # ordering, so only those are counted.
  canary=$(awk '$3 == "argrun" { print $1; exit }' "$2")
  if [ -n "$canary" ] && grep -q "^${canary} " "$moved"; then
    drift=1
    excused=$(awk -v m="$MIN_BYTES" '$2 > m && $3 != "copy"' "$moved" | wc -l)
    n=$(awk -v m="$MIN_BYTES" '$2 > m && $3 == "copy"' "$moved" | wc -l)
  else
    n=$(awk -v m="$MIN_BYTES" '$2 > m' "$moved" | wc -l)
  fi

  line="Moved $(wc -l < "$moved") layer(s) of $(wc -l < "$2"), $((n + excused)) above $((MIN_BYTES / 1048576)) MB, ${mb} MB total"
  if [ -n "$drift" ]; then
    line="${line} — ${excused} excused as archive drift, ${n} counted (max ${MAX_LAYERS})"
  fi
  echo "${line}."
  rm -f "$moved"
  if [ "$n" -gt "$MAX_LAYERS" ]; then
    echo "::error::Invalidation Floor regression: ${n} substantial layers moved (max ${MAX_LAYERS}), ${mb} MB pushed to every puller. See docs/adr/0002-layer-ordering-by-invalidation-floor.md"
    return 1
  fi
}

self_test() {
  local dir base healthy regression drift driftbad big i
  dir=$(mktemp -d); trap 'rm -rf "$dir"' RETURN
  base="$dir/base"
  healthy="$dir/healthy"; regression="$dir/regression"
  drift="$dir/drift"; driftbad="$dir/driftbad"

  # One big layer MORE than the bound: MAX_LAYERS is a `-gt` bound, so a
  # regression fixture sitting exactly on the threshold would pass and the
  # self-test would assert nothing. Derived from MAX_LAYERS rather than
  # hardcoded, so raising the threshold cannot silently defuse these fixtures —
  # which is exactly what a hardcoded four would have done at MAX_LAYERS=6.
  big=$((MAX_LAYERS + 1))

  # Shaped like the image: the apt layer first, then a tail of arg-keyed RUNs,
  # then the --link COPYs, then two tiny ones below the size filter.
  printf 'sha256:apt0 90000000 argrun\n' > "$base"
  i=1
  while [ "$i" -le "$big" ]; do
    printf 'sha256:run%02d %d argrun\n' "$i" $((40000000 + i)) >> "$base"
    i=$((i + 1))
  done
  i=1
  while [ "$i" -le "$big" ]; do
    printf 'sha256:cp%02d %d copy\n' "$i" $((40000000 + i)) >> "$base"
    i=$((i + 1))
  done
  printf 'sha256:eee 20000 copy\nsha256:fff 20000 copy\n' >> "$base"

  sed 's/sha256:eee/sha256:ggg/; s/sha256:fff/sha256:hhh/' "$base" > "$healthy"
  sed 's/sha256:run/sha256:xrun/'                          "$base" > "$regression"
  sed 's/sha256:apt0/sha256:xapt0/; s/sha256:run/sha256:xrun/' "$base" > "$drift"
  sed 's/sha256:cp/sha256:xcp/' "$drift" > "$driftbad"

  verdict "$base" "$healthy"    >/dev/null || { echo "self-test FAILED: healthy change rejected"; return 1; }
  verdict "$base" "$regression" >/dev/null && { echo "self-test FAILED: regression accepted"; return 1; }
  verdict "$base" "$base"       >/dev/null || { echo "self-test FAILED: identical images rejected"; return 1; }
  # The canary moved, so the whole RUN tail moving with it is upstream drift.
  verdict "$base" "$drift"      >/dev/null || { echo "self-test FAILED: archive drift rejected"; return 1; }
  # Same drift, plus a real regression among the COPYs the excuse does not cover.
  verdict "$base" "$driftbad"   >/dev/null && { echo "self-test FAILED: regression under drift accepted"; return 1; }
  echo "self-test OK (healthy passes, regression fails, identical passes, drift passes, drift+regression fails)"
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

# A baseline that resolves to the manifest we just pushed compares the image
# against itself and reports "Moved 0" — a pass that means nothing happened, not
# that nothing moved. Say so instead of banking it as a green.
if [ "$(arch_digest "$baseline")" = "$(arch_digest "$work/current.json")" ]; then
  echo "::notice::Baseline resolves to the manifest just pushed — no comparison performed."
  exit 0
fi

# Unreadable layer metadata is a notice and a pass, for the same reason a
# missing baseline is: a publish that could not measure must not redden.
# Falling back to an unclassified count would reproduce exactly the
# unactionable red that follow-up 3 removed.
if ! layers_of "$baseline" "${ref%%:*}" "$work/old.txt" \
  || ! layers_of "$work/current.json" "${ref%%:*}" "$work/new.txt"; then
  echo "::notice::Could not read layer metadata — nothing to compare."
  exit 0
fi

verdict "$work/old.txt" "$work/new.txt"
