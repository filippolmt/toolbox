#!/usr/bin/env bash
set -euo pipefail

# Three responsibilities, gated together on `command -v glab`:
#   1. Credential probe — same configured/not-configured surface the other
#      cred scripts emit, so all five providers report uniformly through
#      the Init Sequence.
#   2. git credential helper — when glab is authenticated, register
#      `!glab auth git-credential` in the SYSTEM gitconfig for every host in
#      glab's config (host ~/.gitconfig is a RO mount — must not be edited).
#      Covers private GitLab HTTPS clones, e.g. `brew tap` of a private tap.
#      Non-fatal: on failure SSH remotes keep working via the RO ~/.ssh mount.
#   3. `glab skills install` (EXPERIMENTAL upstream) — non-fatal on failure.
#      Two passes: Claude Code reads only ~/.claude/skills; Codex reads
#      only ~/.agents/skills (cross-agent USER scope per agentskills.io).
command -v glab >/dev/null 2>&1 || exit 0

if glab auth status >/dev/null 2>&1; then
    echo "  glab: configured"
    _glab_config="${HOME}/.config/glab-cli/config.yml"
    if command -v yq >/dev/null 2>&1 && [ -f "${_glab_config}" ]; then
        _glab_hosts=$(yq '.hosts | keys | .[]' "${_glab_config}" 2>/dev/null) || \
            echo "toolbox: glab config parse failed — no credential helper registered (non-fatal — SSH remotes via the RO ~/.ssh mount still work)"
        # flock: init.d scripts run backgrounded in parallel (entrypoint.sh) —
        # serialize /etc/gitconfig writes so a future script touching the
        # system gitconfig can't race git's own <file>.lock. Lock file is
        # NOT /etc/gitconfig.lock (that name is git's internal lockfile).
        #
        # Absolute glab path: `brew tap` clones run git under Homebrew's
        # scrubbed PATH (/usr/bin:/bin:…, no /usr/local/bin), so a bare
        # `!glab …` helper silently fails to exec and git falls back to a
        # terminal prompt that fatals in non-interactive clones. Resolving
        # the binary here keeps the helper working under any caller PATH.
        _glab_bin=$(command -v glab)
        for _glab_host in ${_glab_hosts}; do
            sudo flock /tmp/toolbox-gitconfig.lock \
                git config --system "credential.https://${_glab_host}.helper" "!${_glab_bin} auth git-credential" || \
                echo "toolbox: glab credential helper for ${_glab_host} failed (non-fatal — SSH remotes via the RO ~/.ssh mount still work)"
        done
    else
        echo "toolbox: yq or glab config missing — no credential helper registered (non-fatal — SSH remotes via the RO ~/.ssh mount still work)"
    fi
else
    echo "  glab: not configured"
fi

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
