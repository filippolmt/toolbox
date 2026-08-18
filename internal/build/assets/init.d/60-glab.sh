#!/usr/bin/env bash
set -euo pipefail

# Three responsibilities, gated together on `command -v glab`:
#   1. Credential probe — same three-way surface the other cred scripts emit,
#      so all five providers report uniformly through the Init Sequence.
#      Three-way outcome (decision D-08-creds-tristate):
#        configured        — every configured host answers.
#        auth check failed — hosts are configured but at least one is rejected
#                            live (an expired OAuth refresh token is the
#                            recurring case). Never collapsed onto "not
#                            configured": that lumping is what the decision
#                            removed, because it hid expired credentials.
#        not configured    — no host configured at all.
#   2. git credential helper — for every host in glab's config that is
#      authenticated, register `!glab auth git-credential` in the SYSTEM
#      gitconfig (host ~/.gitconfig is a RW host-synced mount — must not be
#      polluted, so writes go to the container-local SYSTEM gitconfig).
#      Covers private GitLab HTTPS clones, e.g. `brew tap` of a private tap.
#      Non-fatal: on failure SSH remotes keep working via the RO ~/.ssh mount.
#   3. `glab skills install` (EXPERIMENTAL upstream) — non-fatal on failure.
#      Two passes: Claude Code reads only ~/.claude/skills; Codex reads
#      only ~/.agents/skills (cross-agent USER scope per agentskills.io).
command -v glab >/dev/null 2>&1 || exit 0

_glab_config="${HOME}/.config/glab-cli/config.yml"
_glab_hosts=""
_glab_parse_failed=""
if command -v yq >/dev/null 2>&1 && [ -f "${_glab_config}" ]; then
    _glab_hosts=$(yq '.hosts | keys | .[]' "${_glab_config}" 2>/dev/null) || {
        _glab_hosts=""
        _glab_parse_failed=1
    }
fi

_glab_authed=""
_glab_broken=""
if [ -n "${_glab_hosts}" ]; then
    if glab auth status >/dev/null 2>&1; then
        # Fast path, and the common one: every configured host answers, so one
        # glab process settles it. That matters beyond speed — glab rewrites
        # the whole config.yml when a probe refreshes an expired token, and
        # that file is a single host-shared mount, so each extra glab process
        # is another unlocked read-modify-write racing the other containers.
        _glab_authed="${_glab_hosts}"
    else
        # Something is rejected. Only now pay one probe per host, to learn
        # WHICH: a bare `glab auth status` exits non-zero when ANY host fails,
        # so gating the block on it costs every *healthy* host its credential
        # helper — and reports neither the broken host nor the reason.
        for _glab_host in ${_glab_hosts}; do
            if glab auth status --hostname "${_glab_host}" >/dev/null 2>&1; then
                _glab_authed="${_glab_authed}${_glab_host} "
            else
                _glab_broken="${_glab_broken}${_glab_host} "
            fi
        done
    fi
fi

if [ -n "${_glab_broken}" ]; then
    echo "  glab: auth check failed for ${_glab_broken% } (try \`glab auth login --hostname ${_glab_broken%% *}\`)"
elif [ -n "${_glab_authed}" ]; then
    echo "  glab: configured"
elif glab auth status >/dev/null 2>&1; then
    # Authenticated, but the host list is unenumerable, so there is nothing to
    # register — name which of the two reasons it was.
    echo "  glab: configured"
    if [ -n "${_glab_parse_failed}" ]; then
        echo "toolbox: glab config parse failed — no credential helper registered (non-fatal — SSH remotes via the RO ~/.ssh mount still work)"
    else
        echo "toolbox: yq or glab config missing — no credential helper registered (non-fatal — SSH remotes via the RO ~/.ssh mount still work)"
    fi
else
    echo "  glab: not configured"
fi

# Absolute glab path: `brew tap` clones run git under Homebrew's scrubbed PATH
# (/usr/bin:/bin:…, no /usr/local/bin), so a bare `!glab …` helper silently
# fails to exec and git falls back to a terminal prompt that fatals in
# non-interactive clones. Resolving the binary here keeps the helper working
# under any caller PATH.
_glab_bin=$(command -v glab)
# flock: init.d scripts run backgrounded in parallel (entrypoint.sh) —
# serialize /etc/gitconfig writes so a future script touching the system
# gitconfig can't race git's own <file>.lock. Lock file is NOT
# /etc/gitconfig.lock (that name is git's internal lockfile).
#
# Empty _glab_authed simply skips the loop: no host authenticated, nothing to
# register.
for _glab_host in ${_glab_authed}; do
    sudo flock /tmp/toolbox-gitconfig.lock \
        git config --system "credential.https://${_glab_host}.helper" "!${_glab_bin} auth git-credential" || \
        echo "toolbox: glab credential helper for ${_glab_host} failed (non-fatal — SSH remotes via the RO ~/.ssh mount still work)"
done

_install() {
    local label="$1"; shift
    glab skills install "$@" --force >/dev/null 2>&1 || \
        echo "toolbox: glab skills install ($label) failed (non-fatal — retry: \`glab skills install $* --force\`)"
}

if command -v claude >/dev/null 2>&1 && [ -d "$HOME/.claude" ]; then
    _install claude --path "$HOME/.claude/skills"
fi

if command -v codex >/dev/null 2>&1; then
    _install codex --global
fi
