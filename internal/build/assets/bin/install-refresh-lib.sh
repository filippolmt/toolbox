# shellcheck shell=bash
# Workspace Install Refresh, in one place. Sourced by every init.d member that
# re-runs a tool's per-repo installer (30-graphify.sh, 31-codegraph.sh,
# 40-playwright-cli.sh). Rationale, and what each half protects against:
# docs/adr/0001-workspace-install-refresh.md and the CONTEXT.md entry.
#
# Not executable and not on PATH: like bridge-lib.sh it is a library, sourced
# by absolute path so a member never depends on PATH order.

# toolbox_install_refresh <stamp-key> <artefact> <version> <install-cmd…>
#
# Runs the install only when the bundled version moved away from the stamp OR
# the artefact that install should have written is missing; stamps the version
# on success and warns, non-fatally, on failure. Returns 0 when the gate opened
# (whatever the install then did) so a caller can hang its own post-install work
# off the same decision, and 1 when it stayed shut.
#
# The stamp is toolbox-owned and lives outside the workspace, keyed by
# (workspace, stamp-key); $PWD is hashed so an arbitrarily deep workspace path
# still yields a valid name. One key per pass, never one per tool: a tool that
# installs a separate copy per agent needs each pass to carry its own, or the
# first pass stamps the version and the rest never run.
toolbox_install_refresh() {
    local key="$1" artefact="$2" ver="$3"
    shift 3

    local stamp stamped=""
    stamp="$HOME/.toolbox-state/install-refresh/$(printf '%s' "$PWD" | sha256sum | cut -c1-16)-$key"
    if [ -f "$stamp" ]; then
        read -r stamped < "$stamp" 2>/dev/null || true
    fi

    # The -n guard scopes to the version half ONLY. If the version probe ever
    # breaks upstream, an unreadable version must not read as "differs from the
    # stamp" — that would reopen the gate on every shell and hand back exactly
    # the churn this gate removes. Guarding the whole condition instead would be
    # the opposite bug: a deleted install would stop self-healing, silently.
    { [ -n "$ver" ] && [ "$stamped" != "$ver" ]; } || [ ! -f "$artefact" ] || return 1

    if "$@" >/dev/null 2>&1; then
        mkdir -p "$(dirname "$stamp")"
        printf '%s' "$ver" > "$stamp"
    else
        echo "toolbox: $key refresh failed (non-fatal — run \`$*\` manually to retry)"
    fi
    return 0
}
