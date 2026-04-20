# toolbox: zsh configuration sourced from /etc/zsh/zshrc.
# Symmetric to bashrc.sh — keeps the two shells behaviourally aligned for the
# bits the user interacts with (alias, history, completions, starship).
# Do not use `set -e`: sourced scripts must not crash the shell if a tool is
# missing or a completion fails.

# -- Security: allow world-writable dirs (host UID mapping forces a+rwX) ----
# MUST come BEFORE `source $ZSH/oh-my-zsh.sh` so OMZ calls `compinit -u` and
# silences the "Insecure completion-dependent directories" warning. Layer 21
# of the Dockerfile runs `chmod -R a+rwX /home/toolbox` so non-baked UIDs can
# write; that same world-writability is what zsh's compaudit complains about.
export ZSH_DISABLE_COMPFIX=true
export DISABLE_AUTO_UPDATE=true
export DISABLE_MAGIC_FUNCTIONS=true

# -- Persistent, deduplicated history (ZSH-06) -------------------------------
# Distinct from bash_history — zsh and bash serialisation formats are not
# compatible, so each owns its own file under the same ~/.toolbox/state mount.
if [ -n "${HOME:-}" ]; then
    _toolbox_hist_dir="${HOME}/.toolbox-state"
    mkdir -p "${_toolbox_hist_dir}" 2>/dev/null || true
    if [ -w "${_toolbox_hist_dir}" ]; then
        export HISTFILE="${_toolbox_hist_dir}/zsh_history"
    fi
    unset _toolbox_hist_dir
fi
export HISTSIZE=50000
export SAVEHIST=50000
setopt HIST_IGNORE_ALL_DUPS
setopt HIST_FIND_NO_DUPS
setopt HIST_SAVE_NO_DUPS
setopt SHARE_HISTORY
setopt HIST_REDUCE_BLANKS
setopt INC_APPEND_HISTORY

# -- Oh-My-Zsh ---------------------------------------------------------------
export ZSH="${HOME}/.oh-my-zsh"
ZSH_THEME=""  # intentionally blank — starship owns the prompt.

# Plugin load order — DO NOT REORDER WITHOUT READING THE COMMENTS.
#
# The upstream consensus (Zim framework, fzf-tab README, zsh-autosuggestions
# README) locks the following rule: any plugin that wraps ZLE widgets must
# see a stable set of predecessors, so `zsh-autosuggestions` — which wraps
# every previously-defined widget — MUST be the last entry. Within the ZLE-
# wrapping trio the order is:
#
#   zsh-completions          (adds new _<tool> files to fpath, BEFORE compinit)
#   (OMZ runs compinit here, internally)
#   fzf-tab                  (after compinit, before any widget wrappers)
#   zsh-syntax-highlighting  (after completions)
#   history-substring-search (after syntax-highlighting)
#   zsh-autosuggestions      (LAST — wraps every widget above)
#
# Regular bundled plugins can go in any order; we put infra first.
plugins=(
    # Infrastructure (bundled — silent-skip when binary missing)
    git docker docker-compose kubectl helm kubectx
    terraform opentofu gh gcloud fzf direnv
    # DX (bundled)
    aliases colored-man-pages
    # Custom — completion definitions (adds _<tool> files to fpath)
    zsh-completions
    # ZLE widget hooks in strict order:
    fzf-tab
    zsh-syntax-highlighting
    history-substring-search
    zsh-autosuggestions
)

source "$ZSH/oh-my-zsh.sh"

# -- Alias (ZSH-05) — AFTER `source $ZSH/oh-my-zsh.sh` so our overrides win ---
# OMZ's terraform plugin sets `alias tf=terraform` unconditionally. We do NOT
# install terraform (tofu is the replacement), so we override `tf=tofu` AFTER
# sourcing OMZ. The other aliases are either redundant (`k=kubectl` is set by
# omz kubectl) or new (`d`, `g`, `ll`, `la`, `l`) — harmless to reassert.
alias k=kubectl
alias tf=tofu
alias h=helm
alias g=git
alias d=docker
alias ll='ls -la'
alias la='ls -A'
alias l='ls -CF'

# -- zoxide (ZSH-07) — after compinit (which ran inside oh-my-zsh.sh) ---------
# Registers the `z` function and the `__zoxide_z` ZLE widget.
if command -v zoxide >/dev/null 2>&1; then
    eval "$(zoxide init zsh)"
fi

# -- Starship prompt (LAST — overrides PROMPT/PS1) ---------------------------
# Starship doesn't depend on getpwuid(), so it works even with
# --user $(id -u):$(id -g) where whoami prints "I have no name!".
if command -v starship >/dev/null 2>&1; then
    eval "$(starship init zsh)"
fi

return 0
