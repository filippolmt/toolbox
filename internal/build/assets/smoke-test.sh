#!/usr/bin/env bash
set -e

IMAGE="${1:-toolbox:local}"

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

# Optional tools — skip if the binary is absent (INSTALL_<TOOL>=false at build
# time). CI always builds the full image so every optional check runs there.
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

# Bundled zsh stack (ZSH-01..07). Gate on `command -v zsh` so an
# INSTALL_ZSH=false image SKIPs the whole block without failing.
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

    # i. `plugins` array has exactly 17 entries after zshrc is sourced
    # (19 in the original plan; fzf + direnv removed — see zshrc.sh rationale).
    _zsh_plugin_count_check() {
        count=$(zsh -i -c "echo \$plugins | wc -w" 2>/dev/null)
        [ "$count" -eq 17 ]
    }

    # j. R-01 / Pitfall 3 — our tf=tofu override wins over OMZ terraform plugin
    _zsh_alias_tf_check() {
        zsh -i -c "alias tf" 2>/dev/null | grep -q "tf=tofu"
    }

    # k. zoxide registered its `z` function
    _zsh_z_function_check() {
        zsh -i -c "type z" 2>/dev/null | grep -q function
    }

    # l. vendor-completions populated (expect >= 11 files on default build:
    # kubectl, helm, gh, glab, yq, docker, uv, pnpm, starship, bat, codex)
    _zsh_vendor_completions_check() {
        count=$(ls /usr/share/zsh/vendor-completions 2>/dev/null | wc -l)
        [ "$count" -ge 11 ]
    }

    # Run the assertions in order. The per-plugin loop expands to 4 entries.
    _zsh_assert "binary"                       _zsh_binary_check
    _zsh_assert "oh-my-zsh.sh"                 _zsh_omz_sh_check
    for p in zsh-autosuggestions zsh-syntax-highlighting zsh-completions fzf-tab; do
        _plugin_named_check() { _zsh_plugin_check "$p"; }
        _zsh_assert "plugin/${p}"              _plugin_named_check
    done
    _zsh_assert "omz .git retained"            _zsh_omz_git_check
    _zsh_assert "fzf binary"                   _zsh_fzf_check
    _zsh_assert "zoxide binary"                _zsh_zoxide_check
    _zsh_assert "/etc/zsh/zshrc ZSH_DISABLE_COMPFIX" _zsh_zshrc_compfix_check
    _zsh_assert "interactive shell clean"      _zsh_interactive_clean_check
    _zsh_assert "HISTFILE"                     _zsh_histfile_check
    _zsh_assert "17 plugins loaded"            _zsh_plugin_count_check
    _zsh_assert "alias tf=tofu"                _zsh_alias_tf_check
    _zsh_assert "zoxide z function"            _zsh_z_function_check
    _zsh_assert "vendor-completions >= 10"     _zsh_vendor_completions_check
}

check_required "node"       node --version
check_required "npm"        npm --version
check_required "python3"    python3 --version
check_required "git"        git --version
check_required "rg"         rg --version
check_required "make"       make --version
check_required "tini"       /usr/bin/tini --version

check_optional  "pnpm"      pnpm     pnpm --version
check_optional  "claude"    claude   claude --version
check_optional  "codex"     codex    codex --version
check_optional  "pyright"   pyright-langserver pyright --version
check_optional  "playwright" playwright playwright --version
check_optional  "playwright-cli" playwright-cli playwright-cli --version
check_optional  "uv"        uv       uv --version
check_optional  "kubectl"   kubectl  kubectl version --client
check_optional  "kubectx"   kubectx  kubectx --version
check_optional  "kubens"    kubens   kubens --version
check_optional  "helm"      helm     helm version --short
check_optional  "tofu"      tofu     tofu version
check_optional  "gh"        gh       gh --version
check_optional  "glab"      glab     glab --version
check_optional  "gws"       gws      gws --version
check_optional  "docker"    docker   docker --version
check_optional  "compose"   docker   docker compose version
check_optional  "gcloud"    gcloud   gcloud --version
check_optional  "gke-gcloud-auth-plugin" gke-gcloud-auth-plugin gke-gcloud-auth-plugin --version
check_optional  "azure"     az       az --version
check_optional  "oci"       oci      oci --version
check_optional  "jq"        jq       jq --version
check_optional  "yq"        yq       yq --version
check_optional  "go"        go       go version
check_optional  "gopls"     gopls    gopls version
check_optional  "goimports" goimports sh -c '"'"'echo "" | goimports'"'"'
check_optional  "starship"  starship starship --version
check_optional  "bat"       bat      bat --version
check_optional  "rtk"       rtk      rtk --version

check_zsh

echo ""
echo "=== Results: ${PASS} passed, ${FAIL} failed, ${SKIP} skipped ==="
[ "$FAIL" -eq 0 ] || exit 1
'

echo ""
echo "=== Signal handling check (SIGTERM propagates via tini) ==="
# Regression guard for the "docker stop hangs 10s then SIGKILLs" class of bugs:
# interactive zsh/bash ignore SIGTERM when they own the TTY, so without a
# proper PID-1 init (tini) the shell only dies via SIGKILL fallback. tini -g
# forwards signals to the process group so the shell exits clean.
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
