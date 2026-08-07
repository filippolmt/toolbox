#!/bin/bash
# Claude Code statusLine — pretty
# ┊ cwd ┊ repo:branch[*↑↓] ⑂wt ┊ PR ┊ model ⚡effort [FAST] ┊ badge ┊ agent ┊ vim ┊ style ┊ mode ┊ ctx ▰▰▱▱▱ ┊ duration ┊ rate-limits
# Perf: single jq pass, git cached 5s per session_id (script runs on every tick)
export LC_NUMERIC=C

# Config dir: resolve paths against wherever Claude Code runs, not a fixed env (issue: portability).
CFG="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"

input=$(cat)

# Single jq pass — fields joined with \x1f (unit separator: NOT IFS-whitespace,
# so empty fields are preserved by read instead of collapsing/shifting)
IFS=$'\x1f' read -r cwd model used effort perm_mode dur_ms \
  sid vim_mode out_style fh_pct fh_reset wd_pct wd_reset \
  fast_mode agent_name pr_num pr_url pr_state gwt repo_name < <(
  echo "$input" | jq -r 2>/dev/null '[
    (.cwd // .workspace.current_dir // ""),
    (.model.display_name // ""),
    (.context_window.used_percentage // "" | tostring),
    (.effort.level // ""),
    (.permission_mode // ""),
    (.cost.total_duration_ms   // 0 | tostring),
    (.session_id // ""),
    (.vim.mode // ""),
    (.output_style.name // ""),
    (.rate_limits.five_hour.used_percentage // "" | tostring),
    (.rate_limits.five_hour.resets_at       // "" | tostring),
    (.rate_limits.seven_day.used_percentage // "" | tostring),
    (.rate_limits.seven_day.resets_at       // "" | tostring),
    (.fast_mode // false | tostring),
    (.agent.name // ""),
    (.pr.number // "" | tostring),
    (.pr.url // ""),
    (.pr.review_state // ""),
    (.workspace.git_worktree // ""),
    (.workspace.repo.name // "")
  ] | join("")'
)
cwd="${cwd:-$PWD}"

# Effort fallback: read effortLevel from settings.json
if [ -z "$effort" ]; then
  effort=$(jq -r '.effortLevel // "high"' "$CFG/settings.json" 2>/dev/null)
  effort="${effort:-high}"
fi

# ── Icons (Nerd Font) ─────────────────────────────────────────────────────
I_DIR=$''      # folder-open
I_GIT=$''      # git-branch
I_MODEL=$''    # rocket
I_EFF=$''      # bolt
I_CLOCK=$''    # clock
I_RESET=$''    # refresh
I_PLAN=$''     # pencil-square
I_SHIELD=$''   # shield
I_WARN=$''     # warning
I_WT=$''  # code-fork (nf-fa-code_fork U+F126) — linked worktree
I_PR=$''       # git-pull-request (nf-oct-git_pull_request U+F407)

# ── Palette (256 colours) ──────────────────────────────────────────────────
RST=$'\033[0m'
DIM=$'\033[38;5;240m'      # separators
C_DIR=$'\033[38;5;75m'     # blue
C_GIT=$'\033[38;5;179m'    # amber
C_MODEL=$'\033[38;5;183m'  # lilac
C_EFF=$'\033[38;5;86m'     # aquamarine
C_META=$'\033[38;5;245m'   # grey — session metadata (duration)
C_RL=$'\033[38;5;176m'     # mauve
C_OK=$'\033[38;5;114m'     # green — ahead, PR approved
C_BAD=$'\033[38;5;203m'    # red — dirty, behind, PR changes-requested
C_WT=$'\033[38;5;109m'     # sage — linked worktree
C_PR=$'\033[38;5;110m'     # steel
SEP=" ${DIM}│${RST} "

seg_n=0
seg() {  # seg <already-coloured text> — prepends the separator from the 2nd segment on
  if (( seg_n++ )); then printf '%s' "$SEP"; else printf ' '; fi
  printf '%s' "$1"
}

osc8() {  # osc8 <url> <text> — OSC 8 hyperlink; degrades to plain text where unsupported
  printf '\033]8;;%s\033\\%s\033]8;;\033\\' "$1" "$2"
}

# ── Directory ─────────────────────────────────────────────────────────────
IFS=/ read -ra parts <<< "$cwd"
n=${#parts[@]}
if (( n > 3 )); then
  short="…/${parts[n-2]}/${parts[n-1]}"
else
  short="$cwd"
fi
seg "${C_DIR}${I_DIR} ${short}${RST}"

# ── Git: repo:branch, dirty *, ahead ↑ / behind ↓, ⑂ worktree — cache 5s ──
# repo name and worktree prefer the statusLine JSON (workspace.repo.name /
# workspace.git_worktree) — fewer git spawns per tick — but each keeps its git
# fallback, because both fields are conditional: workspace.repo needs an
# `origin` remote, and workspace.git_worktree needs a Claude Code new enough to
# emit it. Without the fallback the marker would silently vanish rather than
# degrade. The whole segment is cached together; the cache key includes cwd, so
# entering a worktree recomputes immediately instead of waiting out the TTL.
build_git_seg() {
  local branch repo extra ab behind ahead label wt
  branch=$(git -C "$cwd" --no-optional-locks symbolic-ref --short HEAD 2>/dev/null \
    || git -C "$cwd" --no-optional-locks rev-parse --short HEAD 2>/dev/null)
  [ -z "$branch" ] && return
  repo="$repo_name"
  # Fallback: workspace.repo is absent without an `origin` remote.
  [ -z "$repo" ] && repo=$(basename "$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)")
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
  label="$branch"
  [ -n "$repo" ] && label="${repo}${DIM}:${RST}${C_GIT}${branch}"
  printf '%s' "${C_GIT}${I_GIT} ${label}${extra}${RST}"
}

git_seg=""
if [ -n "$sid" ]; then
  # Key the cache on session_id + cwd: the session_id is stable across a `cd`,
  # so keying on it alone would serve another directory's git segment for the
  # 5s TTL. cksum is one spawn; read is a builtin.
  read -r _cwdsum _ < <(cksum <<<"$cwd")
  GIT_CACHE="/tmp/claude-statusline-git-${sid}-${_cwdsum}"
  now=${EPOCHSECONDS:-$(date +%s)}
  if [ -f "$GIT_CACHE" ] && (( now - $(stat -c %Y "$GIT_CACHE" 2>/dev/null || echo 0) < 5 )); then
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

# ── Open PR for this branch — clickable (OSC 8), coloured by review state ──
if [ -n "$pr_num" ]; then
  case "$pr_state" in
    approved)          c="$C_OK"  ;;
    changes_requested) c="$C_BAD" ;;
    pending)           c="$C_GIT" ;;
    draft)             c="$DIM"   ;;
    *)                 c="$C_PR"  ;;
  esac
  pr_txt="${I_PR} #${pr_num}"
  [ -n "$pr_url" ] && pr_txt=$(osc8 "$pr_url" "$pr_txt")
  seg "${c}${pr_txt}${RST}"
fi

# ── Model + effort (+ fast mode) ──────────────────────────────────────────
if [ -n "$model" ]; then
  model_short="${model%% (*}"
  m="${C_MODEL}${I_MODEL} ${model_short}${RST}"
  [ -n "$effort" ] && m="${m} ${C_EFF}${I_EFF}${effort}${RST}"
  [ "$fast_mode" = "true" ] && m="${m} "$'\033[1;38;5;220m'"FAST${RST}"
  seg "$m"
fi

# ── Permission mode badge ─────────────────────────────────────────────────
case "$perm_mode" in
  plan)              seg $'\033[1;38;5;141m'"${I_PLAN} PLAN${RST}" ;;
  acceptEdits)       seg $'\033[1;38;5;179m'"${I_SHIELD} ACCEPT${RST}" ;;
  bypassPermissions) seg $'\033[1;38;5;203m'"${I_WARN} BYPASS${RST}" ;;
  dontAsk|auto)      seg $'\033[1;38;5;80m'"${I_SHIELD} ${perm_mode^^}${RST}" ;;
  default|'')        ;;  # normal mode: no badge
  *)                 seg $'\033[38;5;250m'"[$perm_mode]${RST}" ;;
esac

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

# ── Context: bar ▰▰▰▱▱ + % (green→yellow→red) ────────────────────────────
if [ -n "$used" ]; then
  pct=$(printf '%.0f' "$used" 2>/dev/null)
  if [[ "$pct" =~ ^[0-9]+$ ]]; then
    if   (( pct >= 70 )); then c=$'\033[38;5;203m'
    elif (( pct >= 40 )); then c=$'\033[38;5;179m'
    else                       c=$'\033[38;5;114m'; fi
    fill=$(( (pct + 10) / 20 )); (( fill > 5 )) && fill=5
    bar=""
    for ((i=0; i<5; i++)); do
      if (( i < fill )); then bar+="▰"; else bar+="▱"; fi
    done
    seg "${c}${bar} ${pct}%${RST}"
  fi
fi

# ── Session duration ───────────────────────────────────────────────────────
if (( ${dur_ms:-0} >= 60000 )); then
  mins=$(( dur_ms / 60000 ))
  if (( mins >= 60 )); then
    dur_fmt="$(( mins / 60 ))h$(( mins % 60 ))m"
  else
    dur_fmt="${mins}m"
  fi
  seg "${C_META}${I_CLOCK} ${dur_fmt}${RST}"
fi

# ── Rate limits (absent for API-key users — hidden when null) ───────────
rl_out=""

if [ -n "$fh_pct" ]; then
  fh_pct_fmt=$(printf '%.0f' "$fh_pct")
  fh_time=""
  if [ -n "$fh_reset" ] && [ "$fh_reset" != "0" ]; then
    fh_time=$(date -d "@${fh_reset}" +%H:%M 2>/dev/null || date -r "${fh_reset}" +%H:%M 2>/dev/null)
  fi
  rl_out="5h ${fh_pct_fmt}%"
  [ -n "$fh_time" ] && rl_out="${rl_out} ${I_RESET}${fh_time}"
fi

if [ -n "$wd_pct" ]; then
  wd_pct_fmt=$(printf '%.0f' "$wd_pct")
  wd_time=""
  if [ -n "$wd_reset" ] && [ "$wd_reset" != "0" ]; then
    wd_time=$(date -d "@${wd_reset}" +"%d/%m %H:%M" 2>/dev/null || date -r "${wd_reset}" +"%d/%m %H:%M" 2>/dev/null)
  fi
  seg7="7d ${wd_pct_fmt}%"
  [ -n "$wd_time" ] && seg7="${seg7} ${I_RESET}${wd_time}"
  if [ -n "$rl_out" ]; then
    rl_out="${rl_out} ${DIM}·${RST}${C_RL} ${seg7}"
  else
    rl_out="$seg7"
  fi
fi

[ -n "$rl_out" ] && seg "${C_RL}${rl_out}${RST}"

# Always exit 0: a failing final `[ -n ... ] &&` would exit 1
# and Claude Code discards the statusline on a non-zero exit code
exit 0
