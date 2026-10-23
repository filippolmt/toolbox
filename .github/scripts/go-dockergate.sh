#!/usr/bin/env bash
# Run daemon gates with host-visible fixtures and the caller's identity.
set -euo pipefail

if (( $# < 5 )); then
  echo 'Usage: go-dockergate.sh HOST_SRC RUNTIME_IMAGE RUNNER_IMAGE RUN PACKAGE...' >&2
  exit 2
fi
source=$1
runtime=$2
runner=$3
pattern=$4
shift 4
uid=$(id -u)
gid=$(id -g)
if [[ $uid == 0 ]]; then
  echo 'Run dockergates as an unprivileged user so fixture permission failures remain observable.' >&2
  exit 1
fi
if [[ $source != /* || $source == *:* ]]; then
  echo 'HOST_SRC must be an absolute host path without colons.' >&2
  exit 2
fi
# Peer gates own this global anchor; never run them over a developer's anchor.
if ! anchors=$(docker ps -aq --filter 'name=^toolbox-peer-anchor$'); then
  echo 'Cannot inspect the peer anchor; refusing to run gates against an unknown daemon state.' >&2
  exit 1
fi
if [[ -n $anchors ]]; then
  echo 'An existing peer anchor prevents a safe gate run; use a dedicated daemon or close peer sessions first.' >&2
  exit 1
fi
socket_gid=$(docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
  --entrypoint stat "$runtime" -c '%g' /var/run/docker.sock)
if [[ ! $socket_gid =~ ^[0-9]+$ ]]; then
  echo 'Could not resolve the daemon socket group.' >&2
  exit 1
fi
umask 077
mkdir -p "dist/.scratch/dockergate/home-$uid" "dist/.scratch/dockergate/tmp-$uid"
home="$source/dist/.scratch/dockergate/home-$uid"
tmp="$source/dist/.scratch/dockergate/tmp-$uid"
exec docker run --rm --user "$uid:$gid" --group-add "$socket_gid" \
  -v "$source:$source" -v /var/run/docker.sock:/var/run/docker.sock -w "$source" \
  -e "HOME=$home" -e "TMPDIR=$tmp" -e GOPATH= -e GOCACHE= \
  -e GOFLAGS='-mod=mod -buildvcs=false' -e "IMAGE_TAG=$runtime" \
  "$runner" go test -tags dockergate -count=1 -run "$pattern" "$@"
