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
export ZSH="${HOME:-/home/toolbox}/.oh-my-zsh"
ZSH_THEME=""  # intentionally blank — starship owns the prompt.

# Persist compdump on the state volume so first-shell compinit cost
# (50-200ms typical) is paid once per image rebuild, not every container
# boot. ZSH_VERSION keys the filename so a zsh upgrade auto-invalidates.
# Must be set BEFORE `source $ZSH/oh-my-zsh.sh` — OMZ reads ZSH_COMPDUMP
# when it calls compinit internally.
if [ -n "${HOME:-}" ] && [ -d "${HOME}/.toolbox-state" ]; then
    export ZSH_COMPDUMP="${HOME}/.toolbox-state/zcompdump-${ZSH_VERSION}"
fi

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
    terraform opentofu gh gcloud
    # DX (bundled) — `aliases` plugin dropped: we override all OMZ aliases
    # below and never use its acs/asl helpers; sourcing it cost ~5-10ms.
    # `extract` exposes the `x <file>` universal extractor (tar/zip/7z/
    # bz2/gz/xz/zst) — zero init cost (function only loads on call).
    # `dirhistory` adds Alt-←/→ dir back/forward bindings; ~5ms ZLE setup.
    colored-man-pages
    extract
    dirhistory
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

if command -v bat >/dev/null 2>&1; then
    alias cat=bat
fi

# -- zoxide (ZSH-07) — after compinit (which ran inside oh-my-zsh.sh) ---------
# Registers the `z` function and the `__zoxide_z` ZLE widget.
if command -v zoxide >/dev/null 2>&1; then
    eval "$(zoxide init zsh)"
fi

# -- fzf keybindings ---------------------------------------------------------
# Debian's fzf package ships shell integration files but does NOT auto-source
# them — without these lines `fzf` works as a binary but Ctrl-R / Ctrl-T /
# Alt-C bindings stay dormant. fzf-tab (loaded in `plugins=(...)` above) only
# replaces TAB completion; key bindings are a separate concern. Source AFTER
# OMZ so compinit is done and AFTER zsh-autosuggestions so its widget wraps
# fzf's `fzf-history-widget` cleanly. fzf binds Ctrl-T + Alt-C; Ctrl-R is
# bound here but the atuin init below MUST run AFTER this block so atuin's
# Ctrl-R widget wins (last-bind-wins under ZLE).
if command -v fzf >/dev/null 2>&1; then
    [ -f /usr/share/doc/fzf/examples/key-bindings.zsh ] && \
        source /usr/share/doc/fzf/examples/key-bindings.zsh 2>/dev/null || true
    [ -f /usr/share/doc/fzf/examples/completion.zsh ] && \
        source /usr/share/doc/fzf/examples/completion.zsh 2>/dev/null || true
fi

# -- atuin (SQLite-backed history; Ctrl-R fuzzy search) ----------------------
# Must come AFTER fzf so atuin's Ctrl-R widget wins. `--disable-up-arrow`
# preserves zsh's native Up-Arrow + history-substring-search bindings; atuin
# only takes over Ctrl-R. The DB + config live under the ~/.toolbox/atuin
# bind-mount (DefaultMounts → ~/.local/share/atuin), so history survives
# `toolbox stop` across every shell across every project.
#
# Belt-and-braces: pre-create ATUIN_CONFIG_DIR. init.d/65-atuin.sh owns
# the canonical mkdir, but if that script aborts (env-var unset on a
# pre-atuin image still carrying the binary, log lost to background
# parallel boot, etc.) atuin's settings loader inside `atuin init zsh`
# would fail with ENOENT on config.toml and dump a multi-line anyhow
# stack to the user's shell. Mkdir here is idempotent + free.
# stderr redirect MUST go inside the subshell — `2>/dev/null` after
# eval only silences the eval'd code, not `atuin init` itself, so
# settings-load errors would otherwise leak through despite the trailer.
if command -v atuin >/dev/null 2>&1; then
    [ -n "${ATUIN_CONFIG_DIR:-}" ] && mkdir -p "$ATUIN_CONFIG_DIR" 2>/dev/null || true
    eval "$(atuin init zsh --disable-up-arrow 2>/dev/null)" 2>/dev/null || true
fi

# -- Starship prompt (LAST — overrides PROMPT/PS1) ---------------------------
# Starship doesn't depend on getpwuid(), so it works even with
# --user $(id -u):$(id -g) where whoami prints "I have no name!".
if command -v starship >/dev/null 2>&1; then
    eval "$(starship init zsh)"
fi

return 0
