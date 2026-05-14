# toolbox: aliases + completions for interactive shells.
# Sourced from /etc/bash.bashrc (see Dockerfile).
# Do not use `set -e`: sourced scripts must not crash the shell if a tool
# is missing or a completion fails.

# -- Persistent shell history ----------------------------------------
# HISTFILE points at the ~/.toolbox/state mount, so every toolbox shell
# across every project writes to and reads from the same file.
# histappend + "history -a; history -n" in PROMPT_COMMAND make concurrent
# shells see each other's commands without stomping the file on exit.
if [ -n "${HOME:-}" ]; then
    _toolbox_hist_dir="${HOME}/.toolbox-state"
    mkdir -p "${_toolbox_hist_dir}" 2>/dev/null || true
    if [ -w "${_toolbox_hist_dir}" ]; then
        export HISTFILE="${_toolbox_hist_dir}/bash_history"
        export HISTSIZE=10000
        export HISTFILESIZE=20000
        export HISTCONTROL=ignoredups:erasedups
        shopt -s histappend
        history -c 2>/dev/null || true
        history -r "${HISTFILE}" 2>/dev/null || true
        PROMPT_COMMAND="history -a; history -n${PROMPT_COMMAND:+; $PROMPT_COMMAND}"
    fi
    unset _toolbox_hist_dir
fi

# -- Alias (D-04) -----------------------------------------------------
alias k=kubectl
alias tf=tofu
alias h=helm
alias g=git
alias d=docker
alias ll='ls -la'
alias la='ls -A'
alias l='ls -CF'

if command -v bat >/dev/null 2>&1; then
    alias cat=bat
fi

# -- Completions (D-06) -----------------------------------------------
if [ -f /usr/share/bash-completion/bash_completion ]; then
    source /usr/share/bash-completion/bash_completion
fi

# Subcommand-generated completions are cached on the state volume. Eager
# `source <(tool completion bash)` for 6 tools costs 200–500ms per shell
# (fork+exec + parse). Cache key includes binary mtime+size so image rebuilds
# invalidate stale dumps automatically. Tofu (`complete -C`) and gcloud
# (static file) stay eager — they don't pay the fork cost.
_toolbox_completion_cache="${HOME}/.toolbox-state/completions"
mkdir -p "$_toolbox_completion_cache" 2>/dev/null || true

_toolbox_load_completion() {
    local tool="$1" gen="$2" bin key cache
    bin=$(command -v "$tool" 2>/dev/null) || return 0
    key=$(stat -c '%Y-%s' "$bin" 2>/dev/null || echo nokey)
    cache="${_toolbox_completion_cache}/${tool}-${key}.bash"
    if [ ! -s "$cache" ]; then
        eval "$gen" >"$cache" 2>/dev/null || { rm -f "$cache"; return 0; }
    fi
    # shellcheck disable=SC1090
    source "$cache" 2>/dev/null || true
}

_toolbox_load_completion kubectl 'kubectl completion bash'
complete -o default -F __start_kubectl k 2>/dev/null || true

_toolbox_load_completion helm 'helm completion bash'
complete -o default -F __start_helm h 2>/dev/null || true

if command -v tofu >/dev/null 2>&1; then
    complete -C /usr/local/bin/tofu tofu 2>/dev/null || true
    complete -C /usr/local/bin/tofu tf   2>/dev/null || true
fi

_toolbox_load_completion gh   'gh completion -s bash'
_toolbox_load_completion glab 'glab completion -s bash'
_toolbox_load_completion yq   'yq shell-completion bash'

# gcloud ships completion as a static file rather than a subcommand, so guard
# on the file (absent when INSTALL_GCLOUD=false).
if [ -f /opt/google-cloud-sdk/completion.bash.inc ]; then
    source /opt/google-cloud-sdk/completion.bash.inc 2>/dev/null || true
fi

_toolbox_load_completion docker 'docker completion bash'
complete -o default -F __start_docker d 2>/dev/null || true

unset -f _toolbox_load_completion
unset _toolbox_completion_cache

# Git completion is lazy-loaded by bash-completion; source it explicitly so
# the `g` alias can reuse __git_main without requiring the user to tab on
# `git` first.
if command -v git >/dev/null 2>&1; then
    if [ -f /usr/share/bash-completion/completions/git ]; then
        source /usr/share/bash-completion/completions/git 2>/dev/null || true
    fi
    if declare -F __git_main >/dev/null 2>&1; then
        complete -o bashdefault -o default -o nospace -F __git_main g 2>/dev/null || true
    fi
fi

# -- fzf keybindings ---------------------------------------------------
# Debian's fzf package ships shell integration files but does NOT auto-source
# them — without these lines `fzf` works as a binary but Ctrl-R / Ctrl-T /
# Alt-C bindings stay dormant. Loaded after completions so fzf's `complete -F`
# wrappers register against tools whose completions are already in place.
# fzf binds Ctrl-T (paste path) and Alt-C (cd into subdir); Ctrl-R is bound
# here too but the atuin init below MUST run AFTER this block so atuin's
# Ctrl-R widget rebinds over fzf's.
if command -v fzf >/dev/null 2>&1; then
    [ -f /usr/share/doc/fzf/examples/key-bindings.bash ] && \
        source /usr/share/doc/fzf/examples/key-bindings.bash 2>/dev/null || true
    [ -f /usr/share/doc/fzf/examples/completion.bash ] && \
        source /usr/share/doc/fzf/examples/completion.bash 2>/dev/null || true
fi

# -- Prompt (D-05) -----------------------------------------------------
# Starship does not depend on getpwuid(), so it works even with
# --user $(id -u):$(id -g) where whoami prints "I have no name!".
#
# Must come BEFORE atuin in bash: starship's bash init installs a DEBUG
# trap for cmd_duration, and upstream docs warn that hooking DEBUG AFTER
# `starship init` breaks the module. atuin's bash init uses bash-preexec,
# which cooperatively chains DEBUG traps when sourced AFTER starship —
# the opposite order (atuin first) silently breaks cmd_duration.
if command -v starship >/dev/null 2>&1; then
    eval "$(starship init bash)"
fi

# -- atuin (SQLite-backed history; Ctrl-R fuzzy search) ----------------
# Order: AFTER fzf (Ctrl-R rebind wins, last-bind-wins) + AFTER starship
# (bash-preexec chains DEBUG trap instead of clobbering it; see starship
# block above). `--disable-up-arrow` preserves bash's native Up-Arrow
# single-shell history navigation; atuin only owns Ctrl-R.
if command -v atuin >/dev/null 2>&1; then
    eval "$(atuin init bash --disable-up-arrow)" 2>/dev/null || true
fi

return 0
