# toolbox: aliases + completions for interactive shells.
# Sourced from /etc/bash.bashrc (see Dockerfile).
# Do not use `set -e`: sourced scripts must not crash the shell if a tool
# is missing or a completion fails.

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
fi

# -- Prompt (D-05) -----------------------------------------------------
# Starship does not depend on getpwuid(), so it works even with
# --user $(id -u):$(id -g) where whoami prints "I have no name!".
eval "$(starship init bash)"

return 0
