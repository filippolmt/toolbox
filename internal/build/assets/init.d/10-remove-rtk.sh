#!/usr/bin/env bash
set -euo pipefail

# Permanent migration for installations that once bundled RTK. Keep this
# idempotent: users may skip any number of image releases before reopening a
# persisted agent home.
_clean_hooks_json() {
    local file=$1
    [ -f "$file" ] || return 0
    if ! python3 - "$file" <<'PY'
import json
import os
import re
import sys
import tempfile

path = sys.argv[1]
pattern = re.compile(r"(^|[ /])rtk hook (claude|codex)$|rtk-rewrite\.(sh|json)$")


def clean(value):
    if isinstance(value, list):
        out = []
        removed = False
        for original in value:
            item, child_removed = clean(original)
            if isinstance(item, dict) and pattern.search(str(item.get("command", ""))):
                removed = True
                continue
            if child_removed and isinstance(item, dict) and "hooks" in item and not item["hooks"]:
                removed = True
                continue
            out.append(item)
            removed = removed or child_removed
        return out, removed
    if isinstance(value, dict):
        out = {}
        removed = False
        for key, original in value.items():
            out[key], child_removed = clean(original)
            removed = removed or child_removed
        return out, removed
    return value, False


try:
    with open(path, encoding="utf-8") as source:
        data, removed = clean(json.load(source))
    if not removed:
        sys.exit(0)
    hooks = data.get("hooks") if isinstance(data, dict) else None
    if isinstance(hooks, dict):
        if hooks.get("PreToolUse") == []:
            hooks.pop("PreToolUse")
        if not hooks:
            data.pop("hooks")
    mode = os.stat(path).st_mode
    fd, temporary = tempfile.mkstemp(dir=os.path.dirname(path), prefix=".rtk-remove-")
    with os.fdopen(fd, "w", encoding="utf-8") as target:
        json.dump(data, target, indent=2)
        target.write("\n")
    os.chmod(temporary, mode)
    os.replace(temporary, path)
except Exception as error:
    print(error, file=sys.stderr)
    sys.exit(1)
PY
    then
        echo "  rtk removal: kept unreadable JSON $file"
    fi
}

_drop_rtk_reference() {
    local file=$1 tmp
    [ -f "$file" ] || return 0
    tmp=$(mktemp "${file}.tmp.XXXXXX") || return 1
    awk '!/^[[:space:]]*@([^[:space:]]*\/)?RTK\.md[[:space:]]*$/' "$file" >"$tmp"
    chmod --reference="$file" "$tmp" 2>/dev/null || true
    mv "$tmp" "$file"
}

# Claude settings share a file with other parallel init scripts.
_claude_lock="$HOME/.toolbox-state/.claude-settings.lock"
mkdir -p "$(dirname "$_claude_lock")"
(
    flock 200
    _clean_hooks_json "$HOME/.claude/settings.json"
) 200>"$_claude_lock"
rm -f "$HOME/.claude/RTK.md" \
      "$HOME/.claude/hooks/rtk-rewrite.sh" \
      "$HOME/.claude/hooks/rtk-rewrite.json" \
      "$HOME/.claude/hooks/.rtk-hook.sha256"
_drop_rtk_reference "$HOME/.claude/CLAUDE.md"

_clean_hooks_json "$HOME/.codex/hooks.json"
rm -f "$HOME/.codex/RTK.md"
_drop_rtk_reference "$HOME/.codex/AGENTS.md"

# Pi's extension may contain user edits. Match the stock payload hashes that
# upstream's own uninstaller recognises; anything else is preserved and named.
_pi_extension="$HOME/.pi/agent/extensions/rtk.ts"
if [ -f "$_pi_extension" ]; then
    _pi_hash=$(tr -d '\r' <"$_pi_extension" | awk '{ lines[NR]=$0 } END { last=NR; while (last > 0 && lines[last] ~ /^[[:space:]]*$/) last--; for (i=1; i<=last; i++) printf "%s%s", lines[i], (i < last ? "\n" : "") }' | sha256sum | awk '{print $1}')
    case "$_pi_hash" in
        5e80e811e689adc9d5ae5a59d1d5702060ca0c10320fea7cffd83c659026f1c5|\
        2cbb2a7a9081275d6eda140d9e375f6772b5c354e7fe931c554c371ad8836c6e|\
        94e80d1a5c159ea38ba8913f7c5b9d9b5c89bf7c204f1e583bfac2ed7fc40ab9|\
        b63e3f6eeaeec23837df5a7c4024fe16dca1f8a49fb1743f8a877cc136ebc2d9|\
        c30d4f4774c59bf25b50b70ab8a7dcb1b8287074592af1598dc09962fa1c7137|\
        5ad230679294dc8dce09546fa25101fd3d0949f454cc8b72e04664fa1bd45ed7|\
        be251e44747e6d09e5ca56ecaeddd8f4861c35a57500cd8b2bf9c39afe5795e8|\
        eb56dd08b8d5f4704906d037d70b357d84d827abe1063135cc7c998efe6cf7f2|\
        628308173ae41c488b76bcf90eafbd4c0c72435927645d81cdbec652eac4b107|\
        3eb16108f51a29c2a62a453d5c97a6ea2da8aea1061da34c50fdcfaa32dc0ff7)
            rm -f "$_pi_extension" "$HOME/.pi/agent/extensions/.rtk-agents"
            ;;
        *) echo "  rtk removal: kept modified $_pi_extension" ;;
    esac
else
    rm -f "$HOME/.pi/agent/extensions/.rtk-agents"
fi

# These are bind mounts under older host CLIs and ordinary home directories
# under newer ones, so clear their contents first and remove the directory only
# when it is not a live mount point.
for _rtk_dir in "$HOME/.config/rtk" "$HOME/.local/share/rtk"; do
    [ -d "$_rtk_dir" ] || continue
    find "$_rtk_dir" -mindepth 1 -delete
    rmdir "$_rtk_dir" 2>/dev/null || true
done

unset _claude_lock _pi_extension _pi_hash _rtk_dir
