# sdd-helpers.sh — sourced by entrypoint.sh (and by smoke-test.sh for
# regression coverage) inside the SDD bootstrap block. Defines bash helpers
# that are too big to keep inline without obscuring the surrounding control
# flow.
#
# Layout contract: this file MUST only define functions and MUST NOT have
# side effects at source time. The entrypoint sources it conditionally; the
# smoke-test sources it standalone and unit-tests each function.

# _sdd_regen_gitignore_fence <skill_key> <manifests_csv> <extras_nl>
#
# Rewrites the fenced block "# >>> sdd-managed/<key> (toolbox)" ...
# "# <<< sdd-managed/<key> (toolbox)" in /workspace/.gitignore with entries
# derived from per-skill JSON manifests (one line per `files` key, prefixed
# with the manifest directory) plus skill-declared ExtraGitignoreEntries
# (newline-separated). Output is sorted/deduped. Idempotent: same input
# always produces byte-identical output.
#
# Inputs are passed as positional args (NOT env) so the function can be
# called from smoke-test fixtures with synthetic manifests + a temporary
# /workspace override (set TOOLBOX_SDD_WORKSPACE=/tmp/foo before calling).
_sdd_regen_gitignore_fence() {
    local key="$1" manifests="$2" extras="$3"
    local ws="${TOOLBOX_SDD_WORKSPACE:-/workspace}"
    local gi="$ws/.gitignore"
    local fence_start="# >>> sdd-managed/${key} (toolbox)"
    local fence_end="# <<< sdd-managed/${key} (toolbox)"
    local tmp entries _mfs _mf _dir current

    tmp="$(mktemp)" || return 1

    entries="$(
        IFS=',' read -ra _mfs <<< "$manifests"
        for _mf in "${_mfs[@]}"; do
            [ -z "$_mf" ] && continue
            [ -f "$ws/$_mf" ] || continue
            _dir="$(dirname "$_mf")"
            node -e '
                const m = require(process.argv[1]);
                const dir = process.argv[2];
                for (const p of Object.keys(m.files || {})) {
                    console.log(dir === "." ? p : dir + "/" + p);
                }
            ' "$ws/$_mf" "$_dir" 2>/dev/null
        done
        printf '%s\n' "$extras"
    )"

    entries="$(printf '%s\n' "$entries" | LC_ALL=C sort -u | sed '/^$/d')"

    {
        printf '%s\n' "$fence_start"
        printf '%s\n' "$entries"
        printf '%s\n' "$fence_end"
    } > "$tmp"

    current=""
    if [ -f "$gi" ]; then
        current="$(awk -v s="$fence_start" -v e="$fence_end" '
            $0 == s { skip = 1; next }
            $0 == e { skip = 0; next }
            !skip   { print }
        ' "$gi")"
    fi
    {
        if [ -n "$current" ]; then
            printf '%s\n' "$current"
        fi
        cat "$tmp"
    } > "$gi.new" || { rm -f "$tmp" "$gi.new"; return 1; }
    mv "$gi.new" "$gi"
    rm -f "$tmp"
}
