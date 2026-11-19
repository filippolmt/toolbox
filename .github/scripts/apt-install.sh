#!/usr/bin/env bash
# Install apt packages on a CI runner, riding out an Ubuntu mirror outage.
# A stalled mirror holds a plain apt-get for minutes (a connect timeout per
# mirror, per package), past the job's own timeout. So the network half
# (update, download) runs under a per-step time limit and is retried until a
# deadline, and the install runs offline from the cache: never killed
# mid-dpkg. An attempt starts only if its worst case fits the deadline, so
# the network retry stays inside it.
set -euo pipefail

if (( $# == 0 )); then
  echo 'Usage: apt-install.sh PACKAGE...' >&2
  exit 2
fi
deadline=${APT_INSTALL_DEADLINE:-300}
step=${APT_INSTALL_STEP_TIMEOUT:-60}
pause=${APT_INSTALL_PAUSE:-15}
kill_after=10 # a step ignoring SIGTERM is killed this much later
apt=(sudo apt-get -o Acquire::Retries=1)
start=$SECONDS
for (( attempt = 1; ; attempt++ )); do
  if timeout -k "$kill_after" "$step" "${apt[@]}" update &&
     timeout -k "$kill_after" "$step" "${apt[@]}" install -y --download-only "$@"; then
    exec "${apt[@]}" install -y --no-download "$@"
  fi
  if (( SECONDS - start + pause + 2 * (step + kill_after) > deadline )); then
    echo "apt-install: giving up after $attempt attempts (${deadline}s deadline)" >&2
    exit 1
  fi
  echo "apt-install: attempt $attempt failed; retrying in ${pause}s" >&2
  sleep "$pause"
done
