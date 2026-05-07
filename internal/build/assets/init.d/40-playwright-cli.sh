#!/usr/bin/env bash
# Phase 10 placeholder: real body lands in Plan 05 (playwright-cli install
# extraction). This file exists to satisfy TestCatalogInitDBijection (Plan 01)
# while per-tool extraction commits land verbatim from entrypoint.sh.
set -euo pipefail
command -v playwright-cli >/dev/null 2>&1 || exit 0
exit 0
