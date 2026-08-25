#!/usr/bin/env bash
set -e

IMAGE="${1:-ghcr.io/filippolmt/toolbox:latest}"

echo "=== Toolbox Smoke Test ==="
echo "Image: ${IMAGE}"
echo ""

docker run --rm "${IMAGE}" bash -c '
set -e

PASS=0
FAIL=0
SKIP=0

# Required tools — fail if absent. These are baked unconditionally (base apt
# layer + node from the base image).
check_required() {
    local name="$1"
    shift
    if output=$("$@" 2>&1); then
        echo "OK: ${name} — ${output}"
        PASS=$((PASS+1))
    else
        echo "FAILED: ${name}"
        FAIL=$((FAIL+1))
    fi
}

# Tools that may not yet be installed on a partial build — skip gracefully
# when absent. All CLIs are installed unconditionally in the canonical image,
# so this branch should not fire in production builds.
check_optional() {
    local name="$1"
    local bin="$2"
    shift 2
    if ! command -v "${bin}" >/dev/null 2>&1; then
        echo "SKIP: ${name} (not installed)"
        SKIP=$((SKIP+1))
        return
    fi
    if output=$("$@" 2>&1); then
        echo "OK: ${name} — ${output}"
        PASS=$((PASS+1))
    else
        echo "FAILED: ${name}"
        FAIL=$((FAIL+1))
    fi
}

# Bundled zsh stack (ZSH-01..07).
#
# Each sub-check is a named function (`_zsh_<name>_check`); the `_zsh_assert`
# reporter invokes it and feeds PASS/FAIL into the outer counters. This
# replaces an earlier nested `sh -c` design that was fragile under quote
# escaping (especially for $HISTFILE / $plugins probes).
check_zsh() {
    if ! command -v zsh >/dev/null 2>&1; then
        echo "SKIP: zsh bundle (not installed)"
        SKIP=$((SKIP+1))
        return
    fi

    _zsh_assert() {
        local name="$1"
        local fn="$2"
        if output=$("$fn" 2>&1); then
            echo "OK: zsh/${name} — ${output}"
            PASS=$((PASS+1))
        else
            echo "FAILED: zsh/${name} — ${output}"
            FAIL=$((FAIL+1))
        fi
    }

    # a. zsh binary is executable at /usr/bin/zsh or /bin/zsh
    _zsh_binary_check() {
        test -x /usr/bin/zsh || test -x /bin/zsh
    }

    # b. oh-my-zsh.sh entrypoint is a file
    _zsh_omz_sh_check() {
        test -f /home/toolbox/.oh-my-zsh/oh-my-zsh.sh
    }

    # c-helper for each of the 4 custom plugins (see loop below)
    _zsh_plugin_check() {
        test -f "/home/toolbox/.oh-my-zsh/custom/plugins/$1/$1.plugin.zsh"
    }

    # c2. the clone is world-writable. fetch-omz sets this itself, because its
    # COPY lands BELOW the user-setup layer, where "chmod -R a+rwX
    # /home/toolbox" runs (ADR 0002) — that chmod can no longer reach it.
    # A regression here is
    # invisible on a green build and only breaks for a host UID other than 1000,
    # which is why it is asserted instead of assumed. Sampling the tree root,
    # the deepest shipped dir and a file is enough: the fetch stage applies one
    # recursive chmod, so a failure is never partial.
    _zsh_omz_perms_check() {
        for p in /home/toolbox/.oh-my-zsh \
                 /home/toolbox/.oh-my-zsh/custom/plugins/fzf-tab \
                 /home/toolbox/.oh-my-zsh/oh-my-zsh.sh; do
            perms=$(stat -c '%A' "$p") || return 1
            case "$perms" in *w*w*w*) ;; *) echo "    not world-writable: $p ($perms)"; return 1 ;; esac
        done
    }

    # d. .git retained, HEAD is a 40-char SHA (R-05 / SPEC ZSH-02)
    _zsh_omz_git_check() {
        head=$(git -c safe.directory=/home/toolbox/.oh-my-zsh -C /home/toolbox/.oh-my-zsh rev-parse HEAD 2>&1) || return 1
        printf "%s" "$head" | grep -Eq "^[a-f0-9]{40}$"
    }

    # e. fzf + zoxide in PATH
    _zsh_fzf_check()    { command -v fzf    >/dev/null; }
    _zsh_zoxide_check() { command -v zoxide >/dev/null; }

    # f. system-wide /etc/zsh/zshrc has the compfix env var
    _zsh_zshrc_compfix_check() {
        test -f /etc/zsh/zshrc && grep -q ZSH_DISABLE_COMPFIX /etc/zsh/zshrc
    }

    # g. interactive shell starts clean — no [oh-my-zsh] warnings on stderr
    _zsh_interactive_clean_check() {
        zsh -i -c : 2>/tmp/zsh_stderr || return 1
        ! grep -q "\[oh-my-zsh\]" /tmp/zsh_stderr
    }

    # h. HISTFILE resolves to the expected path (zsh-interactive probe)
    _zsh_histfile_check() {
        result=$(zsh -i -c "echo \$HISTFILE" 2>/dev/null)
        [ "$result" = "/home/toolbox/.toolbox-state/zsh_history" ]
    }

    # i. `plugins` array has exactly 18 entries after zshrc is sourced
    # (19 in the original plan; fzf + direnv removed — see zshrc.sh rationale.
    # Then `aliases` dropped — we override every OMZ alias below the OMZ source
    # line so the helper plugin paid load cost for nothing. `extract` and
    # `dirhistory` added for `x <file>` extraction + Alt-←/→ dir history,
    # net delta +1).
    _zsh_plugin_count_check() {
        count=$(zsh -i -c "echo \$plugins | wc -w" 2>/dev/null)
        [ "$count" -eq 18 ]
    }

    # j. R-01 / Pitfall 3 — our tf=tofu override wins over OMZ terraform plugin
    _zsh_alias_tf_check() {
        zsh -i -c "alias tf" 2>/dev/null | grep -q "tf=tofu"
    }

    # k. zoxide registered its `z` function
    _zsh_z_function_check() {
        zsh -i -c "type z" 2>/dev/null | grep -q function
    }

    # l. vendor-completions populated. Three sources feed the directory: the
    # final-stage precompute layer (<tool> completion zsh > _<tool>), fetch
    # stages that extract a prebuilt _<tool> from a release tarball or repo,
    # and apt packages that ship their own. Both Dockerfile sources run under
    # `set -eux`, so a broken generator fails the build — this floor only
    # catches net drops. The literal below is derived from the Dockerfile by
    # TestSmokeTestVendorCompletionsFloor; let that test tell you the number.
    _zsh_vendor_completions_check() {
        count=$(ls /usr/share/zsh/vendor-completions 2>/dev/null | wc -l)
        if [ "$count" -ge 20 ]; then
            echo "${count} files"
            return 0
        fi
        # Name the survivors on failure: a bare count cannot tell you which
        # generator stopped shipping.
        echo "only ${count} files: $(ls /usr/share/zsh/vendor-completions 2>/dev/null | tr "\n" " ")"
        return 1
    }

    # m. image locale is UTF-8 (ENV LANG=C.UTF-8 in the Dockerfile) — under
    # the debian-slim POSIX default, ZLE renders UTF-8 bytes as `<ffffffff>`.
    _zsh_locale_check() {
        [ "$(locale charmap 2>/dev/null)" = "UTF-8" ]
    }

    # n. image timezone is Europe/Rome (ENV TZ + tzdata in the Dockerfile).
    # bookworm-slim defaults to UTC; assert we left it. `!= UTC` is robust to
    # the CET/CEST DST swing without pinning the abbreviation.
    _zsh_tz_check() {
        [ -f /usr/share/zoneinfo/Europe/Rome ] && [ "$(date +%Z 2>/dev/null)" != "UTC" ]
    }

    # o. SHELL is in the image env (ENV SHELL=/bin/zsh in the Dockerfile).
    # Assert the environ of PID 1 (tini), not $SHELL: this body runs under
    # bash -c and bash fabricates SHELL from /etc/passwd when unset, which would
    # green a dropped ENV. Without it, tools that resolve their shell from
    # $SHELL alone (herdr panes, tmux) fall back to /bin/sh. No apostrophes or
    # single quotes here — this body lives inside a single-quoted bash -c.
    _zsh_shell_env_check() {
        # -z: /proc/*/environ is NUL-separated, so -z makes one entry one line
        # and -x anchors the match to the whole entry.
        grep -qzx "SHELL=/bin/zsh" /proc/1/environ 2>/dev/null
    }

    # Run the assertions in order. The per-plugin loop expands to 4 entries.
    _zsh_assert "binary"                       _zsh_binary_check
    _zsh_assert "oh-my-zsh.sh"                 _zsh_omz_sh_check
    for p in zsh-autosuggestions zsh-syntax-highlighting zsh-completions fzf-tab; do
        _plugin_named_check() { _zsh_plugin_check "$p"; }
        _zsh_assert "plugin/${p}"              _plugin_named_check
    done
    _zsh_assert "omz world-writable"           _zsh_omz_perms_check
    _zsh_assert "omz .git retained"            _zsh_omz_git_check
    _zsh_assert "fzf binary"                   _zsh_fzf_check
    _zsh_assert "zoxide binary"                _zsh_zoxide_check
    _zsh_assert "/etc/zsh/zshrc ZSH_DISABLE_COMPFIX" _zsh_zshrc_compfix_check
    _zsh_assert "interactive shell clean"      _zsh_interactive_clean_check
    _zsh_assert "HISTFILE"                     _zsh_histfile_check
    _zsh_assert "18 plugins loaded"            _zsh_plugin_count_check
    _zsh_assert "alias tf=tofu"                _zsh_alias_tf_check
    _zsh_assert "zoxide z function"            _zsh_z_function_check
    _zsh_assert "vendor-completions floor"     _zsh_vendor_completions_check
    _zsh_assert "locale charmap UTF-8"         _zsh_locale_check
    _zsh_assert "timezone Europe/Rome"         _zsh_tz_check
    _zsh_assert "SHELL=/bin/zsh image env"     _zsh_shell_env_check
}

# playwright-cli per-repo skill install (functional, offline). `playwright-cli
# install --skills` copies the skill templates bundled in the package into
# `$CWD/.claude/skills/playwright-cli/`. The generic *.md weight-prune in the
# Dockerfile would empty those templates unless it spares them, so this asserts
# the install lands a non-empty SKILL.md + populated references in a throwaway
# CWD — guarding both the prune exclusion and the per-repo install path. No
# single quotes — this body lives inside a single-quoted bash -c.
_pw_skill_check() {
    local d skill refs n
    d=$(mktemp -d) || return 1
    ( cd "$d" && playwright-cli install --skills claude ) >/dev/null 2>&1 || { rm -rf "$d"; return 1; }
    skill="$d/.claude/skills/playwright-cli/SKILL.md"
    refs="$d/.claude/skills/playwright-cli/references"
    test -s "$skill" || { rm -rf "$d"; return 1; }
    n=$(find "$refs" -type f 2>/dev/null | wc -l)
    rm -rf "$d"
    [ "$n" -ge 1 ] || return 1
    echo "install --skills populated SKILL.md + ${n} references in CWD"
}

check_required "node"       node --version
check_required "npm"        npm --version
# NODE_OPTIONS=--dns-result-order=ipv4first must make a localhost listen bind
# IPv4 127.0.0.1 (not ::1), or OAuth CLIs (cf/wrangler/codex) are unreachable
# through the IPv4-only loopback bridge. See docs/commands.md#loopback-bridge.
# This whole body runs inside `docker run ... bash -c '...'` (single-quoted), so
# this line must use only escaped double quotes — a literal single quote would
# close that block (do not "simplify" the quoting).
check_required "node localhost binds IPv4" node -e "const s=require(\"net\").createServer();s.listen(0,\"localhost\",()=>{const a=s.address();s.close();console.log(a.address);process.exit(a.family===\"IPv4\"?0:1)})"
check_required "socat"      sh -c "socat -V 2>&1 | head -n1"
check_required "python3"    python3 --version
check_required "git"        git --version
check_required "rg"         rg --version
check_required "make"       make --version
check_required "dig"        dig -v
check_required "telnet"     telnet --version
check_required "tini"       /usr/bin/tini --version
check_required "vi"         vi --version
check_required "certutil"   sh -c "command -v certutil"
check_required "xterm-ghostty terminfo" sh -c "infocmp xterm-ghostty >/dev/null && echo present"
# The shared transport every bridge shim sources; the state-dir constant
# lives here (not in the shims), so this is where the marker is asserted.
check_required "bridge-lib" sh -c "test -r /usr/local/lib/toolbox/bridge-lib.sh && grep -q /home/toolbox/.toolbox/browser /usr/local/lib/toolbox/bridge-lib.sh && grep -q unix-socket /usr/local/lib/toolbox/bridge-lib.sh && echo present"
check_required "xdg-open wrapper" sh -c "test -x /usr/local/bin/xdg-open && head -n1 /usr/local/bin/xdg-open | grep -q '"'"'^#!/bin/sh'"'"' && grep -q bridge-lib.sh /usr/local/bin/xdg-open && echo present"
check_required "xdg-open symlinks" sh -c "test -L /usr/local/bin/open && test -L /usr/local/bin/x-www-browser && test -L /usr/local/bin/sensible-browser && test -L /usr/local/bin/gnome-open && test -L /usr/local/bin/www-browser && echo present"
# Editor shims must be the bridge wrappers (bridge-lib marker), not a real
# code/codium binary that shadowed them — a dropped COPY fails here too.
check_required "editor shims" sh -c "test -x /usr/local/bin/code && test -L /usr/local/bin/codium && grep -q bridge-lib.sh /usr/local/bin/code && grep -q bridge-lib.sh /usr/local/bin/codium && echo present"
# Proximo shim must be the bridge wrapper (bridge-lib marker) — proximo never
# ships as a real binary in the image; a dropped COPY fails here too.
check_required "proximo shim" sh -c "test -x /usr/local/bin/proximo && grep -q bridge-lib.sh /usr/local/bin/proximo && echo present"
# proximo-hosts is the runtime /etc/hosts sync (no flag invocation — running it
# would mutate /etc/hosts). Assert it ships executable and references the
# proximo.hosts label it discovers, so a dropped COPY fails here too.
check_required "proximo-hosts" sh -c "test -x /usr/local/bin/proximo-hosts && grep -q proximo.hosts /usr/local/bin/proximo-hosts && echo present"
# entrypoint auto-starts the watcher (background, gated on the proximo CA mount)
# so the /etc/hosts sync is automatic — assert the wiring is present.
check_required "proximo-hosts watcher wired" sh -c "grep -q proximo-hosts.--watch /usr/local/bin/entrypoint && echo present"
# entrypoint carries the generic CA-trust block (gated on /etc/toolbox/certs,
# beside the proximo block) — assert the wiring is present. No init.d bijection
# or completion count edit: it ties to no catalog tool.
check_required "global CA trust wired" sh -c "grep -q /etc/toolbox/certs /usr/local/bin/entrypoint && echo present"
# git-credential-toolbox is the bridge credential helper (bridge-lib marker) —
# forwards the git credential protocol to the host store; a dropped COPY fails
# here too.
check_required "git-credential-toolbox shim" sh -c "test -x /usr/local/bin/git-credential-toolbox && grep -q bridge-lib.sh /usr/local/bin/git-credential-toolbox && echo present"
# entrypoint registers the credential helper in the system gitconfig when the
# bridge is installed — assert the wiring is present.
check_required "git credential helper wired" sh -c "grep -q 'credential.helper' /usr/local/bin/entrypoint && grep -q git-credential-toolbox /usr/local/bin/entrypoint && echo present"
# Host-only credential-helper names alias to the bridge shim so a host
# ~/.gitconfig naming osxkeychain/manager/libsecret resolves with no warning.
# Must be symlinks to git-credential-toolbox — NEVER shadow the built-in
# store/cache helpers, so those two are deliberately absent here.
check_required "git credential helper stubs" sh -c "for h in osxkeychain manager manager-core libsecret; do test -L /usr/local/bin/git-credential-\$h && test \"\$(readlink /usr/local/bin/git-credential-\$h)\" = /usr/local/bin/git-credential-toolbox || exit 1; done; ! test -L /usr/local/bin/git-credential-store && ! test -L /usr/local/bin/git-credential-cache && echo present"
# git-prune-dead is a `git prune-dead` subcommand helper; assert it ships
# executable on PATH (no flag invocation — running it would prune branches).
check_required "git-prune-dead" sh -c "test -x /usr/local/bin/git-prune-dead && command -v git-prune-dead >/dev/null && echo present"
# Update poller ships executable and runs clean. Invoke with the opt-out set so
# the smoke run exercises the script body (parse, gates, exit) without any
# network round-trip to GHCR / GitHub.
check_required "toolbox-update-check" sh -c "test -x /usr/local/bin/toolbox-update-check && TOOLBOX_NO_UPDATE_CHECK=1 toolbox-update-check && echo present"
check_required "BROWSER env"       sh -c "test \"\$BROWSER\" = xdg-open && echo present"
check_required "sudo setuid"       sh -c "command -v sudo >/dev/null && test -u \"\$(command -v sudo)\" && echo present"

check_optional  "pnpm"      pnpm     pnpm --version
check_optional  "bun"       bun      bun --version
check_optional  "claude"    claude   claude --version
# Wrapper strips image-wide DO_NOT_TRACK for claude only (Statsig feature
# flags / Remote Control) — see the claude install layer in the Dockerfile.
# NB: no nested sh -c and no single quotes — the whole script body lives
# inside a single-quoted bash -c (see header comment above check_zsh).
check_optional  "claude DO_NOT_TRACK wrapper" claude grep -c "env -u DO_NOT_TRACK" /usr/local/bin/claude
check_optional  "codex"     codex    codex --version
check_optional  "codegraph" codegraph codegraph --version
check_optional  "pyright"   pyright-langserver pyright --version
check_optional  "typescript-language-server" typescript-language-server typescript-language-server --version
check_optional  "tsc"       tsc      tsc --version
check_optional  "playwright" playwright playwright --version
check_optional  "playwright-cli" playwright-cli playwright-cli --version
check_optional  "playwright-cli skill install" playwright-cli _pw_skill_check
check_optional  "uv"        uv       uv --version
check_optional  "kubectl"   kubectl  kubectl version --client
check_optional  "kubectx"   kubectx  kubectx --version
check_optional  "kubens"    kubens   kubens --version
check_optional  "helm"      helm     helm version --short
check_optional  "herdr"     herdr    herdr --version
check_optional  "tofu"      tofu     tofu version
check_optional  "gh"        gh       gh --version
check_optional  "glab"      glab     glab --version
check_optional  "glab flock shim" glab grep -c "flock -w 10 9" /usr/local/bin/glab
check_optional  "glab real binary" glab test -x /usr/local/libexec/glab
check_optional  "gws"       gws      gws --version
check_optional  "atuin"     atuin    atuin --version
check_optional  "docker"    docker   docker --version
check_optional  "compose"   docker   docker compose version
check_optional  "gcloud"    gcloud   gcloud --version
check_optional  "gke-gcloud-auth-plugin" gke-gcloud-auth-plugin gke-gcloud-auth-plugin --version
check_optional  "azure"     az       az --version
check_optional  "oci"       oci      oci --version
check_optional  "graphify"  graphify sh -c '"'"'graphify --help >/dev/null && pip show graphifyy 2>/dev/null | grep ^Version:'"'"'
check_optional  "cf"        cf       cf --version
check_optional  "wrangler"  wrangler wrangler --version
check_optional  "sonar"     sonar    sonar --version
check_optional  "jq"        jq       jq --version
check_optional  "yq"        yq       yq --version
check_optional  "go"        go       go version
check_optional  "gopls"     gopls    gopls version
check_optional  "goimports" goimports sh -c '"'"'echo "" | goimports'"'"'
check_optional  "starship"  starship starship --version
check_optional  "bat"       bat      bat --version
check_optional  "brew"      brew     brew --version
check_optional  "fd"        fd       fd --version
check_optional  "eza"       eza      eza --version
check_optional  "shellcheck" shellcheck shellcheck --version
check_optional  "shfmt"     shfmt    shfmt --version
check_optional  "rtk"       rtk      rtk --version
check_optional  "tmux"      tmux     tmux -V

check_zsh

echo ""
echo "=== Results: ${PASS} passed, ${FAIL} failed, ${SKIP} skipped ==="
[ "$FAIL" -eq 0 ] || exit 1
'

echo ""
echo "=== Signal handling check (SIGTERM propagates via tini) ==="
# Regression guard for the "docker stop hangs 10s then SIGKILLs" class of bugs:
# interactive zsh ignores SIGTERM when it owns the TTY, so without a proper
# PID-1 init (tini) the shell only dies via SIGKILL fallback. tini -g forwards
# signals to the process group so the shell exits clean.
cid=$(docker run -d "${IMAGE}" sleep 3600)
trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' EXIT
start=$(date +%s)
docker stop -t 10 "$cid" >/dev/null
elapsed=$(( $(date +%s) - start ))
if [ "$elapsed" -lt 5 ]; then
    echo "OK: container stopped in ${elapsed}s (no SIGKILL fallback)"
else
    echo "FAILED: container took ${elapsed}s — SIGTERM likely ignored (tini missing or misconfigured)"
    exit 1
fi
docker rm -f "$cid" >/dev/null 2>&1 || true
trap - EXIT

echo ""
echo "=== UID mapping check (runtime UID not baked in image) ==="
# Simulates macOS host UID (501) to verify the entrypoint injects /etc/passwd.
# Without the injection, ssh aborts with "No user exists for uid 501" and every
# git-over-ssh operation fails.
docker run --rm --user 501:20 "${IMAGE}" bash -c '
set -e
getent passwd "$(id -u)" >/dev/null || { echo "FAILED: no passwd entry for uid $(id -u)"; exit 1; }
ssh -V 2>&1 | grep -q OpenSSH || { echo "FAILED: ssh missing"; exit 1; }
ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=1 nonexistent.invalid true 2>&1 \
    | grep -q "No user exists for uid" && { echo "FAILED: ssh still reports missing uid"; exit 1; }
echo "OK: passwd entry injected for uid $(id -u)"
'

echo ""
echo "=== SSH host-key trust (git-over-ssh, issue #366) ==="
# The baked /etc/ssh/ssh_config.d/10-toolbox.conf must make `ssh -G` report
# StrictHostKeyChecking=accept-new (no first-contact prompt, still rejects
# CHANGED keys) and point UserKnownHostsFile at the writable, persistent state
# mount. A dropped COPY or a base image without the ssh_config.d Include fails
# here.
docker run --rm "${IMAGE}" bash -c '
set -e
cfg=$(ssh -G github.com)
echo "$cfg" | grep -qx "stricthostkeychecking accept-new" \
    || { echo "FAILED: StrictHostKeyChecking not accept-new (ssh_config.d drop-in not loaded?)"; exit 1; }
echo "$cfg" | grep -qiE "^userknownhostsfile .*toolbox-state" \
    || { echo "FAILED: UserKnownHostsFile not on the state mount"; exit 1; }
echo "OK: ssh accept-new + known_hosts on state mount"
'

echo ""
echo "=== init.d bijection + executability ==="
# Shell-side counterpart of TestCatalogInitDBijection: confirms the
# Dockerfile COPY → in-image direction restored exec bits despite embed.FS
# stripping them.
docker run --rm "${IMAGE}" bash -c '
set -e
INIT_D=/usr/local/lib/toolbox/init.d
if [ ! -d "$INIT_D" ]; then
    echo "FAILED: $INIT_D missing inside image"
    exit 1
fi
fail=0
count=0
for f in "$INIT_D"/*.sh; do
    if [ ! -x "$f" ]; then
        echo "FAILED: $f is not executable (mode 0755 required)"
        fail=1
    fi
    count=$((count+1))
done
if [ "$count" -ne 17 ]; then
    echo "FAILED: $count init.d/*.sh found, expected exactly 17 (13 catalog InitScripts + 4 system)"
    fail=1
fi
if [ "$fail" -eq 0 ]; then
    echo "OK: $count init.d scripts present and executable"
fi
exit $fail
'

echo ""
echo "=== managed statusline baked ==="
# 35-statusline.sh force-points settings.json at this baked script every boot;
# assert it shipped and parses (a syntax error would break every prompt render).
docker run --rm "${IMAGE}" bash -c '
set -e
SL=/etc/toolbox/statusline-command.sh
[ -f "$SL" ] || { echo "FAILED: $SL missing inside image"; exit 1; }
bash -n "$SL" || { echo "FAILED: $SL has a syntax error"; exit 1; }
echo "OK: managed statusline present and parses"
'

echo ""
echo "=== Workspace Install Refresh (30-graphify.sh, real boot) ==="
# Runtime half of TestWorkspaceInstallRefreshGate, which pins only the static
# invariants. Boot 30-graphify.sh in a throwaway workspace that has opted in
# (graphify-out/ present) and read the result back out of the files it actually
# wrote: the PreToolUse matchers must be the narrowed values, no wide upstream
# matcher may survive `graphify install`, the .graphify-bak must be gone, and a
# version stamp must exist — without the stamp the gate reopens on every shell
# and the churn this whole change removes comes straight back.
#
# The seed carries the wide upstream matchers plus one hand-edited matcher the
# normalisation must NOT touch — that "only the known upstream values" clause is
# what stops the refresh from stomping a user's own hook, and a jq expression
# widened by accident would still pass every other assertion here.
#
# The wide/narrow/kept assertions hold whether or not `graphify install` itself
# succeeds (the seed already carries the wide values); the stamp and the
# graphify-owned-pair assertions additionally require it to have succeeded,
# which is the point — a refresh that cannot install has nothing to stamp, and
# nothing to duplicate either. No single quotes — this body lives inside a
# single-quoted bash -c.
docker run --rm "${IMAGE}" bash -c '
set -e
command -v graphify >/dev/null 2>&1 || { echo "SKIP: graphify not installed"; exit 0; }
command -v claude >/dev/null 2>&1 || { echo "SKIP: claude not installed"; exit 0; }
mkdir -p "$HOME/.claude"
d=$(mktemp -d)
cd "$d"
mkdir -p graphify-out .claude
printf "%s" "{\"hooks\":{\"PreToolUse\":[{\"matcher\":\"Bash|Grep\",\"hooks\":[]},{\"matcher\":\"Read|Glob\",\"hooks\":[]},{\"matcher\":\"Write|Edit\",\"hooks\":[]}]}}" > .claude/settings.json
# Snapshot retention: 12 dated dirs in, 10 out, oldest dropped. Seed distinct
# mtimes because the prune orders by mtime — graphify appends _2, _3 … to
# same-day runs and those do not sort lexically past _10. keeper/ is the control
# for the pattern being anchored: an undated sibling must never be swept.
for i in 01 02 03 04 05 06 07 08 09 10 11 12; do mkdir -p "graphify-out/2026-03-$i"; touch -d "2026-03-$i" "graphify-out/2026-03-$i"; done
mkdir -p graphify-out/keeper
/usr/local/lib/toolbox/init.d/30-graphify.sh >/dev/null 2>&1 || { echo "FAILED: 30-graphify.sh exited non-zero"; exit 1; }
# Exactly one graphify-owned hook per narrowed matcher, whatever else the file
# carries. Counting matchers alone cannot say this: the seed entries end up at
# Grep and Glob too, and they are supposed to. Ownership is the same test
# upstream uses on its own hooks (install.py:1768) — the entry mentions
# graphify — which is precisely the half of that test the rename leaves intact.
assert_gfy_pair() {
    n=$(jq "[.hooks.PreToolUse[] | select((.matcher == \"Grep\" or .matcher == \"Glob\") and (tostring | contains(\"graphify\")))] | length" .claude/settings.json)
    [ "$n" = "2" ] || { echo "FAILED: ${1}: ${n} graphify-owned narrowed hook entr(ies), expected exactly 2 (one Grep, one Glob)"; exit 1; }
}
assert_narrowed() {
    wide=$(jq "[.hooks.PreToolUse[].matcher | select(. == \"Bash|Grep\" or . == \"Read|Glob\")] | length" .claude/settings.json)
    [ "$wide" = "0" ] || { echo "FAILED: ${1}: ${wide} wide PreToolUse matcher(s) survived normalisation"; exit 1; }
    narrow=$(jq "[.hooks.PreToolUse[].matcher | select(. == \"Grep\" or . == \"Glob\")] | length" .claude/settings.json)
    [ "$narrow" -ge 2 ] || { echo "FAILED: ${1}: only ${narrow} narrowed matcher(s), expected Grep and Glob"; exit 1; }
    kept=$(jq "[.hooks.PreToolUse[].matcher | select(. == \"Write|Edit\")] | length" .claude/settings.json)
    [ "$kept" = "1" ] || { echo "FAILED: ${1}: the hand-edited Write|Edit matcher did not survive normalisation"; exit 1; }
    assert_gfy_pair "$1"
}
assert_narrowed "first refresh"
snaps=$(find graphify-out -maxdepth 1 -type d -name "20*" | wc -l)
[ "$snaps" = "10" ] || { echo "FAILED: ${snaps} dated graphify-out snapshots after the prune, expected 10"; exit 1; }
[ ! -d graphify-out/2026-03-02 ] || { echo "FAILED: the prune kept the oldest snapshots instead of the newest"; exit 1; }
[ -d graphify-out/2026-03-12 ] || { echo "FAILED: the prune removed the newest snapshot"; exit 1; }
[ -d graphify-out/keeper ] || { echo "FAILED: the prune swept an undated graphify-out directory"; exit 1; }
[ ! -e .claude/settings.json.graphify-bak ] || { echo "FAILED: settings.json.graphify-bak left in the workspace"; exit 1; }
stamp=$(find "$HOME/.toolbox-state/install-refresh" -name "*-graphify" 2>/dev/null | head -n1)
[ -n "$stamp" ] || { echo "FAILED: no version stamp under ~/.toolbox-state/install-refresh — the gate would reopen every shell"; exit 1; }
# Second refresh, the case the normalisation itself creates: graphify install
# recognises its own hooks by (wide upstream matcher) AND (entry mentions
# graphify), so once we have narrowed the matcher it no longer sees its own work
# and appends a fresh pair on every reopened gate. Deleting the skill reopens the
# gate through the artefact half, which is the real-world trigger too (an image
# upgrade bumps the version).
rm -f .claude/skills/graphify/SKILL.md
/usr/local/lib/toolbox/init.d/30-graphify.sh >/dev/null 2>&1 || { echo "FAILED: 30-graphify.sh exited non-zero on the second refresh"; exit 1; }
# The refresh must have actually re-installed. 30-graphify.sh only warns when
# graphify install fails and this smoke test swallows its output, so without
# this line the whole re-refresh block would pass by installing nothing at all:
# nothing appended, no duplicate possible, assertions vacuous. The stamp cannot
# stand in — a failed install does not rewrite it, so it still holds the value
# the first refresh wrote.
[ -f .claude/skills/graphify/SKILL.md ] || { echo "FAILED: the second refresh did not re-install the graphify skill — the assertions below would prove nothing"; exit 1; }
assert_narrowed "second refresh"
# Compare whole entries, not their serialisations: object key order is not
# significant in JSON, and a tostring-based check would call two equivalent
# entries distinct — weaker than the jq in 30-graphify.sh, which is the thing
# under test here.
dupe_count=$(jq "(.hooks.PreToolUse | length) - (.hooks.PreToolUse | unique | length)" .claude/settings.json)
[ "$dupe_count" = "0" ] || { echo "FAILED: ${dupe_count} duplicate PreToolUse entr(ies) after a second refresh"; exit 1; }
# Third refresh, the case a verbatim dedup could never have caught: a graphify
# upgrade that changes the hook payload. The stale narrowed entry and the freshly
# appended one now differ, so nothing collapses them by equality — only dropping
# by ownership does. Mutating the command here is exactly what an upstream bump
# does to it (install.py appends --strict this way).
jq "(.hooks.PreToolUse[] | select(.matcher == \"Grep\") | .hooks[]?.command) |= . + \" --stale-payload\"" .claude/settings.json > settings.next
mv settings.next .claude/settings.json
rm -f .claude/skills/graphify/SKILL.md
/usr/local/lib/toolbox/init.d/30-graphify.sh >/dev/null 2>&1 || { echo "FAILED: 30-graphify.sh exited non-zero on the third refresh"; exit 1; }
[ -f .claude/skills/graphify/SKILL.md ] || { echo "FAILED: the third refresh did not re-install the graphify skill"; exit 1; }
assert_narrowed "third refresh, changed payload"
stale=$(jq "[.hooks.PreToolUse[] | select(tostring | contains(\"--stale-payload\"))] | length" .claude/settings.json)
[ "$stale" = "0" ] || { echo "FAILED: ${stale} stale-payload PreToolUse entr(ies) survived the refresh that replaced them"; exit 1; }
echo "OK: matchers narrowed, hand-edited matcher kept, no .graphify-bak, one graphify pair across re-refresh and payload change, stamped $(cat "$stamp")"
'
