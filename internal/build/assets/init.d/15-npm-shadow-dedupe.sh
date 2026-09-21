#!/usr/bin/env bash
set -u

# Heal version drift caused by stale npm-global volume duplicates.
#
# PATH puts ~/.npm-global/bin ahead of /usr/local/bin so the baked self-updating
# agents (claude-code, codex, pi) can update themselves into the volume and keep
# winning. The cost of that ordering is that any volume copy of a package the
# image also bakes shadows the Dockerfile-pinned, Renovate-bumped /usr/local one
# forever — the bump never reaches the user. Observed with pyright, then again
# with pi and codegraph, seeded by a pre-baking volume or by a hand `npm i -g`.
#
# Fix: on every start, drop a volume copy whose baked counterpart is at least as
# new. Directional on the version, never a name list — a name list large enough
# to cover the shadow would roll a self-update back to the image pin on every
# shell start, and one small enough to spare the self-updaters leaves their own
# stale copies winning after Renovate overtakes them. An unreadable version on
# either side keeps the volume copy: removing on a failed probe is the one
# outcome that loses work.
#
# Removal goes through `npm rm -g` because it also unlinks the bin symlinks a
# plain rm would leave dangling. Idempotent, offline, non-fatal (init.d runs
# each script under `if ! bash`, neutralising set -e); after the first heal the
# version comparison short-circuits before npm.
#
# Both roots are arguments so the test can drive the script without writing
# under /usr/local; entrypoint.sh runs it with none and gets the real pair.
_prefix="${1:-${NPM_CONFIG_PREFIX:-$HOME/.npm-global}}"
_baked="${2:-/usr/local/lib/node_modules}"

# A candidate must own a bin symlink under $_prefix/bin. Two reasons, and both
# are the same fact: that directory is what PATH searches, so a package without
# a link there cannot shadow anything in the first place; and npm links bins
# only for a package it was asked to install, never for a dependency it hoisted
# into the same lib/node_modules — which is how the first shadow arrived, and
# which must survive, because its dependent resolves it from exactly there.
# npm keeps no manifest in a global prefix (`npm ls -g --depth=0` just
# enumerates the directory), so the link is the only signal that separates the
# two without a network call.
# Both sides are resolved before matching: readlink -f expands every component
# of the link target, so comparing it against an unresolved prefix would match
# nothing the moment the prefix sits behind a symlink — and matching nothing is
# a silent no-op, not an error.
_modules_real=$(readlink -f "$_prefix/lib/node_modules" 2>/dev/null || printf '%s' "$_prefix/lib/node_modules")
_owns_bin_link() {
    local link target
    for link in "$_prefix"/bin/*; do
        [ -L "$link" ] || continue
        target=$(readlink -f "$link" 2>/dev/null) || continue
        case "$target" in
            "$_modules_real"/"$1"/*) return 0 ;;
        esac
    done
    return 1
}

# Version out of a package.json, without paying for a node start per package.
# Matched anywhere on the line rather than at a fixed indentation, because a
# published tarball may carry the file minified onto one line; the first hit is
# the top-level key, since every nested "version" sits inside a later object.
_pkg_version() {
    grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' "$1/package.json" 2>/dev/null |
        head -1 | sed 's/.*"\([^"]*\)"$/\1/'
}

# `sort -V` reads the dash of a semver prerelease as one more separator, so it
# ranks 2.0.0-beta.1 ABOVE 2.0.0 — the exact inversion that would let a stale
# prerelease shadow the release it led to, forever, on the three agents that
# publish -beta/-rc builds into this volume. A `~` sorts before everything for
# the same comparator, which is the ordering semver asks for, so compare the
# translated strings and keep the originals for the removal itself.
_semver_key() { printf '%s' "${1//-/\~}"; }

# Unscoped and scoped packages. A non-matching glob stays literal and is dropped
# by the package.json test below, which also skips the @scope/ dirs themselves.
for _dir in "$_prefix"/lib/node_modules/*/ "$_prefix"/lib/node_modules/@*/*/; do
    [ -f "$_dir/package.json" ] || continue
    _name=${_dir#"$_prefix"/lib/node_modules/}
    _name=${_name%/}
    [ -f "$_baked/$_name/package.json" ] || continue
    _owns_bin_link "$_name" || continue

    _vol=$(_pkg_version "$_dir")
    _img=$(_pkg_version "$_baked/$_name")
    [ -n "$_vol" ] && [ -n "$_img" ] || continue
    # Keep the volume copy only while it is strictly newer than the baked one.
    _vol_key=$(_semver_key "$_vol")
    _img_key=$(_semver_key "$_img")
    if [ "$_vol_key" != "$_img_key" ] &&
        [ "$(printf '%s\n%s\n' "$_vol_key" "$_img_key" | sort -V | tail -1)" = "$_vol_key" ]; then
        continue
    fi

    npm rm -g "$_name" >/dev/null 2>&1 || true
done
unset _prefix _baked _modules_real _dir _name _vol _img _vol_key _img_key
