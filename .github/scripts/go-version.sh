#!/usr/bin/env bash
# Print GO_VERSION, the Go release the runtime image is built with: go.mod's
# last `toolchain` line without its "go" prefix, else its first `go` line.
# One source for the Makefile and the image workflows, so CI never passes an
# empty build arg that overrides the Dockerfile's default with nothing.
# Usage: go-version.sh [go.mod path, or - for stdin; default go.mod]
set -euo pipefail

awk '/^toolchain /{v=substr($2,3)} /^go /{if(!g)g=$2} END{if(!(v||g))exit 1; print (v?v:g)}' "${1:-go.mod}" ||
  { echo "go-version.sh: no toolchain or go line in ${1:-go.mod}" >&2; exit 1; }
