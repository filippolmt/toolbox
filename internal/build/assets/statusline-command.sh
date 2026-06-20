#!/bin/bash
# Claude Code statusLine — pretty
# ┊ cwd ┊ repo:branch[*↑↓] ┊ model ⚡effort ┊ badge ┊ vim ┊ style ┊ ctx ▰▰▱▱▱ [⚠] ┊ tokens ┊ +/- righe ┊ durata ┊ mode ┊ rate-limits
# Perf: jq singolo, git in cache 5s per session_id (script gira a ogni tick)
export LC_NUMERIC=C

# Config dir: resolve paths against wherever Claude Code runs, not a fixed env (issue: portability).
CFG="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"

input=$(cat)

# Single jq pass — fields joined with \x1f (unit separator: NOT IFS-whitespace,
# so empty fields are preserved by read instead of collapsing/shifting)
IFS=$'\x1f' read -r cwd model used effort perm_mode tok_in tok_out lines_add lines_del dur_ms \
  sid vim_mode out_style over200k fh_pct fh_reset wd_pct wd_reset < <(
  echo "$input" | jq -r 2>/dev/null '[
    (.cwd // .workspace.current_dir // ""),
    (.model.display_name // ""),
    (.context_window.used_percentage // "" | tostring),
    (.effort.level // ""),
    (.permission_mode // ""),
    (.context_window.total_input_tokens  // 0 | tostring),
    (.context_window.total_output_tokens // 0 | tostring),
    (.cost.total_lines_added   // 0 | tostring),
    (.cost.total_lines_removed // 0 | tostring),
    (.cost.total_duration_ms   // 0 | tostring),
    (.session_id // ""),
    (.vim.mode // ""),
    (.output_style.name // ""),
    (.exceeds_200k_tokens // false | tostring),
    (.rate_limits.five_hour.used_percentage // "" | tostring),
    (.rate_limits.five_hour.resets_at       // "" | tostring),
    (.rate_limits.seven_day.used_percentage // "" | tostring),
    (.rate_limits.seven_day.resets_at       // "" | tostring)
  ] | join("")'
)
cwd="${cwd:-$PWD}"

# Effort fallback: read effortLevel from settings.json
if [ -z "$effort" ]; then
  effort=$(jq -r '.effortLevel // "high"' "$CFG/settings.json" 2>/dev/null)
  effort="${effort:-high}"
fi

# ── Icone (Nerd Font) ─────────────────────────────────────────────────────
I_DIR=$''      # folder-open
I_GIT=$''      # git-branch
I_MODEL=$''    # rocket
I_EFF=$''      # bolt
I_TOK=$''      # microchip
I_CLOCK=$''    # clock
I_RESET=$''    # refresh
I_PLAN=$''     # pencil-square
I_SHIELD=$''   # shield
I_WARN=$''     # warning

# ── Palette (256 colori) ──────────────────────────────────────────────────
RST=$'\033[0m'
DIM=$'\033[38;5;240m'      # separatori
C_DIR=$'\033[38;5;75m'     # azzurro
C_GIT=$'\033[38;5;179m'    # ambra
C_MODEL=$'\033[38;5;183m'  # lilla
C_EFF=$'\033[38;5;86m'     # acquamarina
C_TOK=$'\033[38;5;245m'    # grigio
C_RL=$'\033[38;5;176m'     # malva
C_ADD=$'\033[38;5;114m'    # verde
C_DEL=$'\033[38;5;203m'    # rosso
SEP=" ${DIM}│${RST} "

seg_n=0
seg() {  # seg <testo già colorato> — antepone separatore dal 2° segmento in poi
  if (( seg_n++ )); then printf '%s' "$SEP"; else printf ' '; fi
  printf '%s' "$1"
}

fmt_tok() {  # notazione compatta k/M senza spawn di awk
  local t=$1
  if   (( t >= 1000000 )); then printf '%d.%dM' $(( t / 1000000 )) $(( (t % 1000000) / 100000 ))
  elif (( t >= 1000 ));    then printf '%d.%dk' $(( t / 1000 ))    $(( (t % 1000) / 100 ))
  else printf '%d' "$t"; fi
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

# ── Git: repo:branch, dirty *, ahead ↑ / behind ↓ — cache 5s ─────────────
build_git_seg() {
  local branch repo extra ab behind ahead label
  branch=$(git -C "$cwd" --no-optional-locks symbolic-ref --short HEAD 2>/dev/null \
    || git -C "$cwd" --no-optional-locks rev-parse --short HEAD 2>/dev/null)
  [ -z "$branch" ] && return
  repo=$(basename "$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null)")
  extra=""
  [ -n "$(git -C "$cwd" --no-optional-locks status --porcelain 2>/dev/null | head -1)" ] && extra+=$'\033[38;5;203m*'
  ab=$(git -C "$cwd" rev-list --left-right --count '@{upstream}...HEAD' 2>/dev/null)
  if [ -n "$ab" ]; then
    read -r behind ahead <<< "$ab"
    (( ahead  > 0 )) && extra+=$'\033[38;5;114m'"↑${ahead}"
    (( behind > 0 )) && extra+=$'\033[38;5;203m'"↓${behind}"
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

# ── Model + effort ────────────────────────────────────────────────────────
if [ -n "$model" ]; then
  model_short="${model%% (*}"
  m="${C_MODEL}${I_MODEL} ${model_short}${RST}"
  [ -n "$effort" ] && m="${m} ${C_EFF}${I_EFF}${effort}${RST}"
  seg "$m"
fi

# ── Permission mode badge ─────────────────────────────────────────────────
case "$perm_mode" in
  plan)              seg $'\033[1;38;5;141m'"${I_PLAN} PLAN${RST}" ;;
  acceptEdits)       seg $'\033[1;38;5;179m'"${I_SHIELD} ACCEPT${RST}" ;;
  bypassPermissions) seg $'\033[1;38;5;203m'"${I_WARN} BYPASS${RST}" ;;
  dontAsk|auto)      seg $'\033[1;38;5;80m'"${I_SHIELD} ${perm_mode^^}${RST}" ;;
  default|'')        ;;  # modalità normale: nessun badge
  *)                 seg $'\033[38;5;250m'"[$perm_mode]${RST}" ;;
esac

# ── Vim mode (solo se attivo) ─────────────────────────────────────────────
if [ -n "$vim_mode" ]; then
  seg "${DIM}vim:${vim_mode^^}${RST}"
fi

# ── Output style (solo se non default) ────────────────────────────────────
if [ -n "$out_style" ] && [ "$out_style" != "default" ]; then
  seg $'\033[38;5;110m'"✎ ${out_style}${RST}"
fi

# ── Context: barra ▰▰▰▱▱ + % (verde→giallo→rosso), ⚠ oltre 200k ──────────
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
    over=""
    [ "$over200k" = "true" ] && over=$' \033[1;38;5;203m'"${I_WARN}200k+"
    seg "${c}${bar} ${pct}%${over}${RST}"
  fi
fi

# ── Token count (input + output, compact k/M notation) ────────────────────
total_tok=$(( ${tok_in:-0} + ${tok_out:-0} ))
if (( total_tok > 0 )); then
  seg "${C_TOK}${I_TOK} $(fmt_tok "$total_tok")${RST}"
fi

# ── Righe scritte da Claude nella sessione, non git diff (✎ +verde −rosso) ─
if (( ${lines_add:-0} > 0 || ${lines_del:-0} > 0 )); then
  seg "${C_TOK}✎${RST} ${C_ADD}+${lines_add}${RST} ${C_DEL}−${lines_del}${RST}"
fi

# ── Durata sessione ───────────────────────────────────────────────────────
if (( ${dur_ms:-0} >= 60000 )); then
  mins=$(( dur_ms / 60000 ))
  if (( mins >= 60 )); then
    dur_fmt="$(( mins / 60 ))h$(( mins % 60 ))m"
  else
    dur_fmt="${mins}m"
  fi
  seg "${C_TOK}${I_CLOCK} ${dur_fmt}${RST}"
fi

# ── Badge modalità comportamentale (ponytail/caveman — auto-gated, niente se inattiva) ─
emit_mode_badge() {  # $1 = glob; esegue solo il primo script trovato
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

# ── Rate limits (assenti per utenti API key — nascosti se null) ───────────
rl_out=""

if [ -n "$fh_pct" ] && [ "$fh_pct" != "null" ]; then
  fh_pct_fmt=$(printf '%.0f' "$fh_pct")
  fh_time=""
  if [ -n "$fh_reset" ] && [ "$fh_reset" != "null" ] && [ "$fh_reset" != "0" ]; then
    fh_time=$(date -d "@${fh_reset}" +%H:%M 2>/dev/null || date -r "${fh_reset}" +%H:%M 2>/dev/null)
  fi
  rl_out="5h ${fh_pct_fmt}%"
  [ -n "$fh_time" ] && rl_out="${rl_out} ${I_RESET}${fh_time}"
fi

if [ -n "$wd_pct" ] && [ "$wd_pct" != "null" ]; then
  wd_pct_fmt=$(printf '%.0f' "$wd_pct")
  wd_time=""
  if [ -n "$wd_reset" ] && [ "$wd_reset" != "null" ] && [ "$wd_reset" != "0" ]; then
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

# Exit 0 sempre: l'ultimo `[ -n ... ] &&` fallito farebbe uscire 1
# e Claude Code scarta la statusline su exit code non-zero
exit 0
