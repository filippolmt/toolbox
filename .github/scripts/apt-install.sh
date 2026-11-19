#!/usr/bin/env bash
# Install apt packages on a CI runner, riding out an Ubuntu mirror outage.
# apt's own Acquire::Retries retries at once; the outages seen here last
# minutes, so the whole update+install is retried with a growing pause.
set -euo pipefail

if (( $# == 0 )); then
  echo 'Usage: apt-install.sh PACKAGE...' >&2
  exit 2
fi
attempts=${APT_INSTALL_ATTEMPTS:-4}
pause=${APT_INSTALL_PAUSE:-30}
for (( attempt = 1; ; attempt++ )); do
  if sudo apt-get -o Acquire::Retries=3 update && sudo apt-get -o Acquire::Retries=3 install -y "$@"; then
    exit 0
  fi
  if (( attempt >= attempts )); then
    echo "apt-install: giving up after $attempt attempts" >&2
    exit 1
  fi
  echo "apt-install: attempt $attempt failed; retrying in $(( attempt * pause ))s" >&2
  sleep $(( attempt * pause ))
done
