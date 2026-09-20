#!/bin/bash
# Claude Code statusLine — pretty
# ┊ branch[*↑↓] ⑂wt ┊ model ⚡effort [FAST] ┊ agent ┊ vim ┊ style ┊ mode ┊ ctx ▰▰▱▱▱ [1M] ┊ ❄ ┊ 5h/7d/$ NN% ↻eta
# Perf: single jq pass, git cached 5s per session_id (script runs on every tick)
# Four things are deliberately absent because another surface already carries
# them, on screen at the same moment: the open PR and the permission mode
# (Claude Code's own hint line, one row below), the repository name (the herdr
# workspace label), and the cwd (starship's [directory] module). Nothing here
# should repeat what the screen already says.
export LC_NUMERIC=C

# Config dir: resolve paths against wherever Claude Code runs, not a fixed env (issue: portability).
CFG="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"

input=$(cat)

# Single jq pass — fields joined with \x1f (unit separator: NOT IFS-whitespace,
# so empty fields are preserved by read instead of collapsing/shifting)
IFS=$'\x1f' read -r cwd model used effort \
  sid vim_mode out_style fh_pct fh_reset wd_pct wd_reset spend_pct spend_reset \
  cache_warm cache_seen fast_mode agent_name gwt \
  ctx_size < <(
  echo "$input" | jq -r 2>/dev/null '[
    (.cwd // .workspace.current_dir // ""),
    (.model.display_name // ""),
    (.context_window.used_percentage // "" | tostring),
    (.effort.level // ""),
    (.session_id // ""),
    (.vim.mode // ""),
    (.output_style.name // ""),
    (.rate_limits.five_hour.used_percentage // "" | tostring),
    (.rate_limits.five_hour.resets_at       // "" | tostring),
    (.rate_limits.seven_day.used_percentage // "" | tostring),
    (.rate_limits.seven_day.resets_at       // "" | tostring),
    (.rate_limits.spend_limit.used_percentage // "" | tostring),
    (.rate_limits.spend_limit.resets_at       // "" | tostring),
    (.prompt_cache.warm             | tostring),
    (.prompt_cache.caching_observed | tostring),
    (.fast_mode // false | tostring),
    (.agent.name // ""),
    (.workspace.git_worktree // ""),
    (.context_window.context_window_size // "" | tostring)
  ] | join("")'
)
cwd="${cwd:-$PWD}"

# Effort fallback: read effortLevel from settings.json
if [ -z "$effort" ]; then
  effort=$(jq -r '.effortLevel // "high"' "$CFG/settings.json" 2>/dev/null)
  effort="${effort:-high}"
fi

# Explicit UTF-8 bytes, for two reasons that rule out the obvious alternatives.
# A literal glyph loses to any tool that cannot encode it: these are Private Use
# Area characters, and one such tool emptied every assignment here in place,
# silently. A \u escape loses to the locale: bash converts it through LC_CTYPE,
# and under LC_ALL=C it emits the escape as text — which the script cannot undo
# from the inside, because LC_ALL outranks anything it exports. Bytes are both
# pure ASCII in the source and locale-proof.
# What holds this: .claude/rules/image-build.md.
# ── Icons (Nerd Font) ─────────────────────────────────────────────────────
I_GIT=$'\xee\x9c\xa5'      # git-branch (nf-dev-git_branch U+E725)
I_MODEL=$'\xef\x84\xb5'    # rocket (nf-fa-rocket U+F135)
I_EFF=$'\xef\x83\xa7'      # bolt (nf-fa-bolt U+F0E7)
I_RESET=$'\xef\x80\xa1'    # refresh (nf-fa-refresh U+F021)
I_WT=$'\xef\x84\xa6'  # code-fork (nf-fa-code_fork U+F126)
I_COLD=$'\xe2\x9d\x84'   # snowflake (U+2744) — plain Unicode, no Nerd Font needed

# ── Palette (256 colours) ──────────────────────────────────────────────────
RST=$'\033[0m'
DIM=$'\033[38;5;240m'      # separators
C_GIT=$'\033[38;5;179m'    # amber
C_MODEL=$'\033[38;5;183m'  # lilac
C_EFF=$'\033[38;5;86m'     # aquamarine
C_COLD=$'\033[38;5;67m'    # steel blue — cold prompt cache
C_OK=$'\033[38;5;114m'     # green — ahead
C_BAD=$'\033[38;5;203m'    # red — dirty, behind
C_WT=$'\033[38;5;109m'     # sage — linked worktree
SEP=" ${DIM}│${RST} "

seg_n=0
seg() {  # seg <already-coloured text> — prepends the separator from the 2nd segment on
  if (( seg_n++ )); then printf '%s' "$SEP"; else printf ' '; fi
  printf '%s' "$1"
}

pct_color() {  # pct_color <integer %> — sets $pct_c; one green→amber→red scale for every percentage
  # Its own variable, not the `c` the PR segment writes: these two run in the
  # same shell and the only thing keeping them apart today is call order.
  if   (( $1 >= 70 )); then pct_c="$C_BAD"
  elif (( $1 >= 40 )); then pct_c="$C_GIT"   # amber, shared with the git segment
  else                      pct_c="$C_OK"; fi
}

# One clock per tick, shared by the git cache TTL and every reset deadline.
# The %(...)T builtin never forks, unlike the `date` calls this replaced.
printf -v NOW '%(%s)T' -1

fmt_eta() {  # fmt_eta <epoch> — sets $eta to a relative countdown, empty when already past
  local rem=$(( $1 - NOW ))
  if   (( rem <= 0 ));    then eta=""
  elif (( rem < 3600 ));  then eta="$(( rem / 60 ))m"
  elif (( rem < 86400 )); then eta="$(( rem / 3600 ))h$(( rem % 3600 / 60 ))m"
  else                         eta="$(( rem / 86400 ))d"; fi
}

# ── Git: branch, dirty *, ahead ↑ / behind ↓, ⑂ worktree — cache 5s ──────
# The repository name is not here: herdr labels its workspace with it, in the
# sidebar, at the same time. The branch is, because nothing else on screen
# carries it — herdr labels every worktree workspace of one repo identically,
# and starship prints the branch only in the prompt, which has scrolled away by
# the time an agent is working. The worktree name prefers the statusLine JSON
# (workspace.git_worktree) over a git spawn, but keeps its git fallback, since
# that field needs a Claude Code new enough to emit it; without the fallback the
# marker would vanish rather than degrade. The whole segment is cached together,
# and the cache key includes cwd, so entering a worktree recomputes at once
# instead of waiting out the TTL.
build_git_seg() {
  local branch extra ab behind ahead wt
  branch=$(git -C "$cwd" --no-optional-locks symbolic-ref --short HEAD 2>/dev/null \
    || git -C "$cwd" --no-optional-locks rev-parse --short HEAD 2>/dev/null)
  [ -z "$branch" ] && return
  extra=""
  [ -n "$(git -C "$cwd" --no-optional-locks status --porcelain 2>/dev/null | head -1)" ] && extra+="${C_BAD}*"
  ab=$(git -C "$cwd" rev-list --left-right --count '@{upstream}...HEAD' 2>/dev/null)
  if [ -n "$ab" ]; then
    read -r behind ahead <<< "$ab"
    (( ahead  > 0 )) && extra+="${C_OK}↑${ahead}"
    (( behind > 0 )) && extra+="${C_BAD}↓${behind}"
  fi
  # Worktree: name it when the JSON supplies one (truncated — it is free-form and
  # would otherwise stretch the line), else fall back to the canonical offline
  # check, which can only report presence: <repo>/.git/worktrees/<id>.
  wt="${gwt:0:16}"
  [ ${#gwt} -gt 16 ] && wt+="…"
  if [ -n "$wt" ]; then
    extra+=" ${C_WT}${I_WT} ${wt}"
  else
    case "$(git -C "$cwd" rev-parse --absolute-git-dir 2>/dev/null)" in
      */worktrees/*) extra+=" ${C_WT}${I_WT}" ;;
    esac
  fi
  printf '%s' "${C_GIT}${I_GIT} ${branch}${extra}${RST}"
}

git_seg=""
if [ -n "$sid" ]; then
  # Key the cache on session_id + cwd: the session_id is stable across a `cd`,
  # so keying on it alone would serve another directory's git segment for the
  # 5s TTL. cksum is one spawn; read is a builtin.
  read -r _cwdsum _ < <(cksum <<<"$cwd")
  GIT_CACHE="/tmp/claude-statusline-git-${sid}-${_cwdsum}"
  if [ -f "$GIT_CACHE" ] && (( NOW - $(stat -c %Y "$GIT_CACHE" 2>/dev/null || echo 0) < 5 )); then
    git_seg=$(<"$GIT_CACHE")
  else
    git_seg=$(build_git_seg)
    # Atomic publish: write to a PID-suffixed temp then rename, so a concurrent
    # reader never sees a half-written segment ($$ is unique per render process).
    printf '%s' "$git_seg" > "${GIT_CACHE}.$$" && mv -f "${GIT_CACHE}.$$" "$GIT_CACHE"
  fi
else
  git_seg=$(build_git_seg)
fi
[ -n "$git_seg" ] && seg "$git_seg"

# ── Model + effort (+ fast mode) ──────────────────────────────────────────
if [ -n "$model" ]; then
  model_short="${model%% (*}"
  m="${C_MODEL}${I_MODEL} ${model_short}${RST}"
  [ -n "$effort" ] && m="${m} ${C_EFF}${I_EFF}${effort}${RST}"
  [ "$fast_mode" = "true" ] && m="${m} "$'\033[1;38;5;220m'"FAST${RST}"
  seg "$m"
fi

# ── Custom agent (--agent / agent settings) ─────────────────────────────────
if [ -n "$agent_name" ]; then
  seg $'\033[38;5;147m'"@${agent_name}${RST}"
fi

# ── Vim mode (only when active) ─────────────────────────────────────────────
if [ -n "$vim_mode" ]; then
  seg "${DIM}vim:${vim_mode^^}${RST}"
fi

# ── Output style (only when non-default) ────────────────────────────────────
if [ -n "$out_style" ] && [ "$out_style" != "default" ]; then
  seg $'\033[38;5;110m'"✎ ${out_style}${RST}"
fi

# ── Behavioural mode badge (ponytail/caveman — auto-gated, nothing when inactive) ─
emit_mode_badge() {  # $1 = glob; runs only the first script found
  local f out
  for f in $1; do
    [ -f "$f" ] || continue
    out=$(bash "$f" 2>/dev/null)
    [ -n "$out" ] && seg "$out"
    return
  done
}
emit_mode_badge "$CFG/plugins/cache/ponytail/ponytail/*/hooks/ponytail-statusline.sh"
emit_mode_badge "$CFG/plugins/cache/caveman/caveman/*/src/hooks/caveman-statusline.sh"

# The window every model had before the extended ones; anything else is worth
# naming beside the bar, because the same percentage is then worth more tokens.
CTX_DEFAULT_WINDOW=200000

# ── Context: bar ▰▰▰▱▱ + % (green→yellow→red) ────────────────────────────
# The percentage alone says nothing about how much room that is: the same 37%
# is worth five times the tokens on an extended-context model. Name the window
# whenever it is not the ordinary one, and stay quiet when it is.
if [ -n "$used" ]; then
  # printf leaves pct at 0 on a value it cannot read, which would draw an empty
  # bar as though the context were pristine. Blank it and let the guard hide it.
  printf -v pct '%.0f' "$used" 2>/dev/null || pct=""
  if [[ "$pct" =~ ^[0-9]+$ ]]; then
    pct_color "$pct"
    fill=$(( (pct + 10) / 20 )); (( fill > 5 )) && fill=5
    bar=""
    for ((i=0; i<5; i++)); do
      if (( i < fill )); then bar+="▰"; else bar+="▱"; fi
    done
    win=""
    if [[ "$ctx_size" =~ ^[0-9]+$ ]] && (( ctx_size != CTX_DEFAULT_WINDOW )); then
      if (( ctx_size >= 1000000 )); then win=" ${DIM}$(( ctx_size / 1000000 ))M"
      else                               win=" ${DIM}$(( ctx_size / 1000 ))k"; fi
    fi
    seg "${pct_c}${bar} ${pct}%${win}${RST}"
  fi
fi

# ── Prompt cache gone cold — one glyph, nothing at all while warm ──────────
# warm is false on a session that has simply never cached anything yet, so gate
# on caching_observed: the glyph must mean "the cache went cold", not "no cache".
# Both arrive through `tostring` alone, never `// ""`: jq's alternative operator
# treats false as empty, which would collapse the one value being tested for.
if [ "$cache_seen" = "true" ] && [ "$cache_warm" = "false" ]; then
  seg "${C_COLD}${I_COLD}${RST}"
fi

# ── Rate limits (absent for API-key users — hidden when null) ───────────
# Deadlines render as a countdown, never a wall clock: a bare "01:59" is
# ambiguous the moment a window resets past midnight, which the five-hour one
# routinely does, and the weekly one needed a date to disambiguate at all.
# Claude Code drops a window once its resets_at passes — and re-runs this script
# at that instant — so a deadline already in the past is never on screen.
rl_out=""

rl_add() {  # rl_add <label> <used %> <reset epoch> — appends "label NN% ↻eta"
  local pct pct_c eta txt
  [ -n "$2" ] || return 0
  printf -v pct '%.0f' "$2" 2>/dev/null || return 0
  [[ "$pct" =~ ^[0-9]+$ ]] || return 0
  # spend_limit is the one window that reports past 100: clamp the colour there,
  # never the number — how far over it has gone is the whole point.
  if (( pct > 100 )); then pct_c=$'\033[1m'"$C_BAD"; else pct_color "$pct"; fi
  txt="${pct_c}${1} ${pct}%"
  if [[ "$3" =~ ^[0-9]+$ ]] && (( $3 > 0 )); then
    fmt_eta "$3"
    [ -n "$eta" ] && txt="${txt} ${I_RESET}${eta}"
  fi
  [ -n "$rl_out" ] && rl_out="${rl_out} ${DIM}· "
  rl_out="${rl_out}${txt}"
}

rl_add 5h  "$fh_pct" "$fh_reset"
rl_add 7d  "$wd_pct" "$wd_reset"
rl_add '$' "$spend_pct" "$spend_reset"

[ -n "$rl_out" ] && seg "${rl_out}${RST}"

# Always exit 0: a failing final `[ -n ... ] &&` would exit 1
# and Claude Code discards the statusline on a non-zero exit code
exit 0
