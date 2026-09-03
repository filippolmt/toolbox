# toolbox: zsh configuration sourced from /etc/zsh/zshrc.
# zsh is the only interactive login shell supported by toolbox; bash was
# removed as an interactive option but the binary is still present for
# init.d shebangs and smoke-test.sh.
# Do not use `set -e`: sourced scripts must not crash the shell if a tool is
# missing or a completion fails.

# -- TERM upgrade: Ghostty host sending bare "xterm" -------------------------
# Ghostty defaults TERM to "xterm-ghostty"; some users override to plain
# "xterm" in their Ghostty config for SSH compatibility. Plain xterm terminfo
# lacks capabilities ZLE relies on when redrawing across a multi-line Starship
# prompt — backspace leaves residual glyphs because ZLE emits fallback erase
# sequences Ghostty doesn't interpret as the real terminfo would.
# Gate on TERM_PROGRAM so this only fires inside Ghostty (other terminals
# sending TERM=xterm legitimately stay untouched). The xterm-ghostty entry
# is shipped by Layer 18 of the Dockerfile (/usr/share/terminfo/x/).
if [ "${TERM:-}" = "xterm" ] && [ "${TERM_PROGRAM:-}" = "ghostty" ]; then
    export TERM=xterm-ghostty
fi

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
# Toolbox-specific: `cdw` jumps to the fixed workspace mount; `reload` re-execs
# the shell to pick up config edits without leaving the container.
alias cdw='cd /workspace'
alias reload='exec zsh'

# Per-repo opt-in initialisers. Each `*-init` alias runs the one-time command
# that opts the CURRENT repo into its tool's agent integration (never global);
# the matching init.d/ script then refreshes that repo on every shell, gated on
# a per-repo marker dir, leaving un-opted-in repos untouched. `codegraph-init`
# and `pwcli-init` also create that marker (`.codegraph/` resp.
# `.claude/skills/playwright-cli/`); `graphify-init` installs the project-scoped
# `/graphify` skill (`.claude/skills/graphify/`) plus the CLAUDE.md section + hooks,
# and `graphify-out/` (its gate marker) appears once the graph is first
# built (by the hooks on first use, or `graphify update .`).
# → docs/internals/shell-start.md (per-repo skill / code-graph sections)
if command -v playwright-cli >/dev/null 2>&1; then
    alias pwcli-init='playwright-cli install --skills claude'
fi
if command -v graphify >/dev/null 2>&1; then
    alias graphify-init='graphify install --project --platform claude'
fi
if command -v codegraph >/dev/null 2>&1; then
    alias codegraph-init='codegraph install --target=claude --location=local --yes'
fi

if command -v bat >/dev/null 2>&1; then
    alias cat=bat
fi

if command -v eza >/dev/null 2>&1; then
    alias ls=eza
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

# -- Homebrew (Linuxbrew) -----------------------------------------------------
# PATH already carries the brew bins via image ENV; shellenv is idempotent
# (prepends only when missing) and fills in HOMEBREW_PREFIX/MANPATH/INFOPATH,
# which the static ENV can't. Guard pattern matches atuin above: brew refuses
# to run as root (e.g. `docker exec -u 0 … zsh`) with a multi-line stderr
# error — suppress inside the subshell so it never reaches the prompt.
if [ -x /home/linuxbrew/.linuxbrew/bin/brew ]; then
    eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv 2>/dev/null)" 2>/dev/null || true
fi

# -- Starship prompt (LAST — overrides PROMPT/PS1) ---------------------------
# Starship doesn't depend on getpwuid(), so it works even with
# --user $(id -u):$(id -g) where whoami prints "I have no name!".
if command -v starship >/dev/null 2>&1; then
    eval "$(starship init zsh)"
fi

# -- Update-availability banner (session-reload) -----------------------------
# A precmd hook surfaces "a newer runtime image / CLI is available" without
# ever blocking the prompt: it reads ONLY a local cache file, and renders it.
# Nothing here touches the network — the host CLI is the single detector and
# owns the probe, the download and this cache (internal/imageprefetch), which
# reaches us through the ~/.toolbox-state bind mount. The banner shows once
# per distinct result, in each shell — "has been told" is a property of a
# session, so its signature is held here and nowhere else. Opt out by exporting
# TOOLBOX_NO_UPDATE_CHECK — no banner. Set in `env:` it also stops the
# host-side probe; typed inside a live shell it only stops the rendering.
#
# The host owns the facts, the image owns the words. The copy lives here and
# not in a host-rendered string because the CLI and this image ship on separate
# pipelines: a host that wrote the sentence would eventually tell an old image
# to run a `toolbox-reload` it does not have.
#
# It states a fact and a command, and no longer prescribes an exit. Because the
# reload asks for no confirmation, this line is the only place its cost is
# named before the act — hence the one clause about recreating the container,
# and hence the deliberate silence about the agent, whose resume is
# conditional. `brew upgrade` and the reload CHAIN rather than alternate: the
# reload re-execs whatever CLI is on disk, so upgrading first is what makes one
# act cover both axes, and both-axes therefore renders as a single line.
# → docs/session-reload.md
if [ -z "${TOOLBOX_NO_UPDATE_CHECK:-}" ] && [ -n "${HOME:-}" ]; then
    typeset -g _toolbox_update_cache="${HOME}/.toolbox-state/update-check"
    # The last result THIS shell displayed. A shell variable, never a file on
    # the state mount: parked there it was every session's, and one session's
    # banner muted every session opened after it — on the connect branch,
    # where the start-up refresh is deliberately never offered, that left the
    # new session with no channel at all. → docs/session-reload.md
    typeset -g _toolbox_update_shown=""

    _toolbox_update_precmd() {
        emulate -L zsh
        # Render from the cache, once per distinct result. The signature
        # records the last result this shell displayed (including "nothing to
        # report"), so a stable cache never re-nags on every prompt.
        [[ -r $_toolbox_update_cache ]] || return 0
        local sig
        sig=$(<$_toolbox_update_cache) 2>/dev/null || return 0
        [[ -z $sig ]] && return 0
        # Quoted: the right-hand side of == is a glob pattern, not a literal.
        [[ $sig == "$_toolbox_update_shown" ]] && return 0

        local image_update=0 image_state=none cli_update=0 cli_latest="" line
        while IFS= read -r line; do
            case $line in
                image_update=*) image_update=${line#image_update=} ;;
                image_state=*)  image_state=${line#image_state=} ;;
                cli_update=*)   cli_update=${line#cli_update=} ;;
                cli_latest=*)   cli_latest=${line#cli_latest=} ;;
            esac
        done < $_toolbox_update_cache

        # How this session adopts an image, decided by the one thing the image
        # can know about the CLI driving it: whether the reload marker was
        # injected. Without it the CLI is older than the command, so advising
        # `toolbox-reload` would advise a refusal — the pre-reload wording is
        # still true there, and still works.
        local adopt
        if [[ -n ${TOOLBOX_RELOAD_MARKER:-} ]]; then
            adopt="run %Btoolbox-reload%b to move this session onto it (recreates the container)"
        else
            adopt="exit the shell and run %Btoolbox stop%b, then reopen it"
        fi

        # One reload covers both axes, so both-axes is one line: upgrade the
        # CLI first and the reload picks it up, because it re-execs the binary
        # on disk before it recreates anything.
        if [[ $image_update == 1 && $cli_update == 1 ]]; then
            print -P "%F{yellow}toolbox:%f a newer CLI${cli_latest:+ ($cli_latest)} and runtime image are ready — run %Bbrew upgrade%b on the host, then $adopt."
        elif [[ $image_update == 1 ]]; then
            print -P "%F{yellow}toolbox:%f a newer runtime image is downloaded — $adopt."
        elif [[ $cli_update == 1 ]]; then
            print -P "%F{yellow}toolbox:%f a newer CLI${cli_latest:+ ($cli_latest)} is available — run %Bbrew upgrade%b on the host."
        fi
        # The registry moved but the bytes did not arrive, and have not for at
        # least a full probe cadence — an expired registry credential looks
        # exactly like this, and the host cannot say so mid-session without
        # printing into the middle of your work. Never rendered beside a
        # "ready" line: the host already picked one, and there is nothing to
        # adopt here.
        [[ $image_state == unavailable ]] && \
            print -P "%F{yellow}toolbox:%f a newer runtime image exists but could not be downloaded — check registry access."

        # Mark this result shown even when nothing surfaced, so the comparison
        # above short-circuits until the cached result actually changes.
        _toolbox_update_shown=$sig
    }
    autoload -Uz add-zsh-hook
    add-zsh-hook precmd _toolbox_update_precmd
fi

# -- Session reload (session-reload) -----------------------------------------
# `toolbox-reload` moves this session onto whatever runtime image the host has
# already downloaded, without exiting and reopening by hand. Docker cannot swap
# the image of a running container and this command runs inside the very
# container being replaced, so the work belongs to the host-side `toolbox
# shell` process: we only signal it. The signal is a marker file the host reads
# exactly where it already decides whether to tear the session down.
#
# A function, not a script in bin/: a child process cannot make its parent
# shell exit, and the exit is the whole point. That also scopes it to the zsh
# prompt, which is where it belongs — asking for a reload from inside an agent
# would be asking the agent to kill itself.
#
# $TOOLBOX_RELOAD_MARKER is injected by the host. Its PRESENCE is the
# capability: an image shipping this function can meet a CLI too old to read
# the marker (the image is pushed on merge, the CLI released on tag, and
# `brew upgrade` is yours to run), and that CLI would write nothing, notice
# nothing, and tear the session down for good. Refusing here costs a message;
# not refusing costs the session. The refusal deliberately names no required
# version — presence is all this side ever learns.
# → docs/session-reload.md
toolbox-reload() {
    emulate -L zsh
    if [ -z "${TOOLBOX_RELOAD_MARKER:-}" ]; then
        print -u2 -P "%F{yellow}toolbox:%f the toolbox CLI running this session${TOOLBOX_CLI_VERSION:+ ($TOOLBOX_CLI_VERSION)} does not support reload — run %B'brew upgrade toolbox'%b on the host, then exit and reopen this shell once."
        return 1
    fi
    # Atomic: write beside the target and rename over it, so a host reading the
    # marker never sees a half-written working directory.
    local tmp="${TOOLBOX_RELOAD_MARKER}.tmp.$$"
    if ! { print -r -- "$PWD" > "$tmp" && mv -f "$tmp" "$TOOLBOX_RELOAD_MARKER"; } 2>/dev/null; then
        rm -f "$tmp" 2>/dev/null
        print -u2 -P "%F{yellow}toolbox:%f could not write the reload marker ($TOOLBOX_RELOAD_MARKER) — the session is unchanged."
        return 1
    fi
    print -P "%F{yellow}toolbox:%f reloading — the container is recreated, this shell ends and a new one opens."
    exit
}

# -- User customisation (ZSH-08) — survives image rebuilds -------------------
# ~/.zshrc lives in the image layer and the Dockerfile truncates it on every
# rebuild; ~/.toolbox-state is a read-write mount that does not. Sourced last so
# a snippet can override anything above, and with alias expansion OFF: zsh
# expands aliases at parse time, so a snippet defining a function named like a
# shipped alias is a parse error that discards the rest of that file.
# → docs/internals/shell-start.md#user-config-in-zshrcd
if [ -n "${HOME:-}" ] && [ -d "${HOME}/.toolbox-state/zshrc.d" ]; then
    _toolbox_aliases_on=0
    [[ -o aliases ]] && _toolbox_aliases_on=1
    unsetopt aliases
    for _toolbox_rc in "${HOME}"/.toolbox-state/zshrc.d/*.zsh(N); do
        source "$_toolbox_rc"
    done
    (( _toolbox_aliases_on )) && setopt aliases
    unset _toolbox_rc _toolbox_aliases_on
fi

return 0
