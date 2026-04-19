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

# -- Completions (D-06) -----------------------------------------------
if [ -f /usr/share/bash-completion/bash_completion ]; then
    source /usr/share/bash-completion/bash_completion
fi

if command -v kubectl >/dev/null 2>&1; then
    source <(kubectl completion bash) 2>/dev/null || true
    complete -o default -F __start_kubectl k 2>/dev/null || true
fi

if command -v helm >/dev/null 2>&1; then
    source <(helm completion bash) 2>/dev/null || true
    complete -o default -F __start_helm h 2>/dev/null || true
fi

if command -v tofu >/dev/null 2>&1; then
    complete -C /usr/local/bin/tofu tofu 2>/dev/null || true
    complete -C /usr/local/bin/tofu tf   2>/dev/null || true
fi

if command -v gh >/dev/null 2>&1; then
    source <(gh completion -s bash) 2>/dev/null || true
fi

if command -v glab >/dev/null 2>&1; then
    source <(glab completion -s bash) 2>/dev/null || true
fi

if command -v yq >/dev/null 2>&1; then
    source <(yq shell-completion bash) 2>/dev/null || true
fi

if command -v docker >/dev/null 2>&1; then
    source <(docker completion bash) 2>/dev/null || true
    complete -o default -F __start_docker d 2>/dev/null || true
fi

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

# -- Prompt (D-05) -----------------------------------------------------
# Starship does not depend on getpwuid(), so it works even with
# --user $(id -u):$(id -g) where whoami prints "I have no name!".
if command -v starship >/dev/null 2>&1; then
    eval "$(starship init bash)"
fi

return 0
