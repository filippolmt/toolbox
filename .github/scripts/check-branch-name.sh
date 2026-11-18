#!/usr/bin/env bash
# Branch-name policy, one rule for two callers:
#   - ci.yml runs it on every PR head branch (positional arguments);
#   - .claude/settings.json runs it as a Claude Code PreToolUse hook on Bash
#     (--hook), refusing to create or push a branch CI would reject before
#     the round trip through a red PR.
set -euo pipefail

rule="must match feat/* or fix/* (a dependency bump takes fix/); Renovate owns renovate/*"

valid_branch() {
    case "$1" in
        feat/?*|fix/?*) return 0 ;;
        renovate/?*) [ "$2" = 'renovate[bot]' ] ;;
        *) return 1 ;;
    esac
}

usage() {
    echo "usage: $0 <branch> <actor> <head-repository> <base-repository> | --self-test | --hook" >&2
    exit 2
}

# Reads the hook's JSON on stdin and prints, one per line, every branch the
# command creates or pushes. Words come from a shell tokenizer, so a branch
# named only inside quotes, a heredoc body or a comment is not a command.
# Unparseable input prints nothing: the hook fails open, CI stays the gate.
# shortcut: commands nested in $(…), backticks or `bash -c` are not seen,
# `git push` without a refspec is read as pushing HEAD, a `cd` holds for the
# rest of the command even inside a subshell, and after a `cd -`, a
# `cd "$var"`, a popd or any pushd but `pushd DIR` the git calls go
# unchecked; parse them if an off-policy branch gets through that way.
# shellcheck disable=SC2016 # Python source, not shell
read -r -d '' parse_command <<'PY' || true
import json, os, re, shlex, subprocess, sys

OPERATORS = ';&|<>()\n'

def strip_heredocs(cmd):
    lines, out, i = cmd.split('\n'), [], 0
    while i < len(lines):
        line = lines[i]
        out.append(line)
        i += 1
        for tabs, delim in re.findall(r"(?<!<)<<(?!<)(-?)\s*['\"]?([A-Za-z_]\w*)['\"]?", line):
            while i < len(lines) and (lines[i].lstrip('\t') if tabs else lines[i]) != delim:
                i += 1
            i += 1
    return '\n'.join(out)

def simple_commands(cmd):
    lex = shlex.shlex(strip_heredocs(cmd), posix=True, punctuation_chars=OPERATORS)
    lex.whitespace, lex.commenters, lex.whitespace_split = ' \t\r', '', True
    seg, comment, redirect = [], False, False
    for tok in lex:
        if tok and all(c in OPERATORS for c in tok):
            if '<' in tok or '>' in tok:
                if seg and seg[-1].isdigit():
                    seg.pop()
                redirect = True
                continue
            if tok == '\n' or not comment:
                comment = False
                if seg:
                    yield seg
                seg = []
            continue
        if redirect:
            redirect = False
        elif not comment:
            if not seg and tok.startswith('#'):
                comment = True
            else:
                seg.append(tok)
    if seg:
        yield seg

def git(d, *args):
    return subprocess.run(['git', '-C', d, *args], capture_output=True, text=True)

def value_after(args, flags):
    for j, a in enumerate(args):
        if a == '--':
            break
        for f in flags:
            if a == f and j + 1 < len(args):
                return [args[j + 1]]
            if f.startswith('--') and a.startswith(f + '='):
                return [a[len(f) + 1:]]
            if not f.startswith('--') and a.startswith(f) and len(a) > len(f):
                return [a[len(f):]]
    return []

BRANCH_QUERY = {'-d', '-D', '--delete', '-l', '--list', '-a', '--all', '-r', '--remotes',
                '-v', '-vv', '--verbose', '--show-current', '--contains', '--no-contains',
                '--merged', '--no-merged', '-u', '--set-upstream-to', '--unset-upstream',
                '--edit-description', '--points-at', '--format', '--sort'}
BRANCH_RENAME = {'-m', '-M', '--move', '-c', '-C', '--copy'}
PUSH_VALUE = {'-o', '--push-option', '--repo', '--receive-pack', '--exec'}
PUSH_NOT_A_BRANCH = {'-d', '--delete', '--tags', '--all', '--branches', '--mirror', '--prune'}
# `git tag` creates a tag only with these flags; any other (-d, -l,
# --contains, -n, --sort, …) lists, verifies or deletes. Short flags cluster
# (`-am msg`), and a valued one takes the rest of its cluster or the next word.
TAG_VALUE_LONG = {'--message', '--file', '--local-user', '--cleanup', '--trailer'}
TAG_CREATE_LONG = {'--annotate', '--sign', '--no-sign', '--force', '--edit', '--no-edit', '--create-reflog'}
TAG_VALUE_SHORT, TAG_CREATE_SHORT = 'mFu', 'asfe'

def tag_created(rest):
    """The name a `git tag` creates, or None when it does not create one."""
    j = 0
    while j < len(rest):
        a = rest[j]
        if not a.startswith('-'):
            return a
        if a.startswith('--'):
            key = a.split('=')[0]
            if key in TAG_VALUE_LONG:
                j += 1 if '=' in a else 2
            elif key in TAG_CREATE_LONG:
                j += 1
            else:
                return None
            continue
        for k, c in enumerate(a[1:]):
            if c in TAG_VALUE_SHORT:
                j += 1 if a[k + 2:] else 2
                break
            if c not in TAG_CREATE_SHORT:
                return None
        else:
            j += 1
    return None

def branch_args(rest):
    flags = {a.split('=')[0] for a in rest if a.startswith('-')}
    names = [a for a in rest if not a.startswith('-')]
    if flags & BRANCH_QUERY:
        return []
    return names[-1:] if flags & BRANCH_RENAME else names[:1]

def push_args(rest, d, new_tags):
    names, j = [], 0
    while j < len(rest):
        a = rest[j]
        if a == '--':
            names += rest[j + 1:]
            break
        if a in PUSH_VALUE:
            j += 2
            continue
        if a.startswith('-'):
            if a.split('=')[0] in PUSH_NOT_A_BRANCH:
                return []
        else:
            names.append(a)
        j += 1
    out = []
    for spec in names[1:] or ['HEAD']:
        src, colon, dst = spec.lstrip('+').partition(':')
        if colon and not src:
            continue  # `:branch` deletes
        dst = dst or src
        if dst == 'HEAD':
            dst = git(d, 'branch', '--show-current').stdout.strip()
        if dst.startswith('refs/heads/'):
            dst = dst[len('refs/heads/'):]
        elif not dst or dst.startswith('refs/') or ((d, dst) in new_tags and src == dst) or git(d, 'show-ref', '--verify', '-q', 'refs/tags/' + dst).returncode == 0:
            continue
        out.append(dst)
    return out

def cd_target(cwd, args):
    """The directory a `cd` or `pushd DIR` lands in, or None when unknown."""
    if cwd is None:
        return None
    dirs = [a for a in args if a == '-' or not a.startswith('-')]
    if not dirs:
        return os.path.expanduser('~')
    if dirs[0] == '-' or '$' in dirs[0]:
        return None
    return os.path.join(cwd, os.path.expanduser(dirs[0]))

def git_call(seg, cwd):
    """(repo dir, subcommand, its args) for a git command, else None. The dir
    is resolved, so `-C .` and the cwd name the same repo."""
    while seg and (re.match(r'^[A-Za-z_]\w*=', seg[0]) or seg[0] in ('env', 'command', 'exec', 'time')):
        seg = seg[1:]
    if not seg or os.path.basename(seg[0]) != 'git':
        return None
    args, d, i = seg[1:], cwd, 0
    while i < len(args) and args[i].startswith('-'):
        if args[i] in ('-C', '-c', '--git-dir', '--work-tree', '--namespace') and i + 1 < len(args):
            if args[i] == '-C':
                d = os.path.join(d, args[i + 1])
            i += 2
        else:
            i += 1
    if i >= len(args):
        return None
    return os.path.realpath(d), args[i], args[i + 1:]

def branches(d, sub, rest, new_tags):
    if sub == 'switch':
        return value_after(rest, ['-c', '-C', '--create', '--force-create', '--orphan'])
    if sub == 'checkout':
        return value_after(rest, ['-b', '-B', '--orphan'])
    if sub == 'worktree' and rest[:1] == ['add']:
        return value_after(rest[1:], ['-b', '-B'])
    if sub == 'branch':
        return branch_args(rest)
    if sub == 'push':
        return push_args(rest, d, new_tags)
    return []

try:
    data = json.load(sys.stdin)
    cmd, cwd = data['tool_input']['command'], data.get('cwd') or '.'
    # (repo dir, name) of every tag a `git tag` earlier in the command creates:
    # it does not exist yet when the hook runs, so show-ref alone would read
    # its push as a branch push.
    names, new_tags = [], set()
    for seg in simple_commands(cmd):
        # A `cd` earlier in the command moves the git calls after it: a push
        # from a worktree beside the checkout pushes the worktree's branch.
        if seg[0] == 'builtin':
            seg = seg[1:]
        if seg and seg[0] == 'cd':
            cwd = cd_target(cwd, seg[1:])
            continue
        if seg and seg[0] in ('pushd', 'popd'):
            # Only `pushd DIR` lands where it names; every other form (bare,
            # -n, ±N) and every popd works the directory stack.
            plain = seg[0] == 'pushd' and len(seg) == 2 and seg[1][:1] not in '-+'
            cwd = cd_target(cwd, seg[1:]) if plain else None
            continue
        if cwd is None:
            continue  # `cd -` and the like: the directory is unknown, fail open
        call = git_call(seg, cwd)
        if not call:
            continue
        d, sub, rest = call
        if sub == 'tag' and (name := tag_created(rest)):
            new_tags.add((d, name))
        names += branches(d, sub, rest, new_tags)
except (ValueError, KeyError, TypeError):
    sys.exit(0)
print('\n'.join(names))
PY

hook() {
    local names branch status=0
    command -v python3 >/dev/null || return 0
    names=$(python3 -c "$parse_command") || return 0
    while IFS= read -r branch; do
        [ -n "$branch" ] || continue
        valid_branch "$branch" human && continue
        echo "branch '$branch' $rule — .github/scripts/check-branch-name.sh" >&2
        status=2
    done <<<"$names"
    return "$status"
}

hook_input() {
    python3 -c 'import json, sys; print(json.dumps({"tool_input": {"command": sys.argv[1]}, "cwd": sys.argv[2]}))' "$1" "$2"
}

self_test() {
    local branch
    for branch in feat/example fix/example; do
        valid_branch "$branch" human || { echo "self-test FAILED: rejected $branch" >&2; return 1; }
    done
    valid_branch renovate/example 'renovate[bot]' || { echo "self-test FAILED: rejected Renovate" >&2; return 1; }
    for branch in main feat/ fix/ refactor/example renovate/example; do
        if valid_branch "$branch" human; then
            echo "self-test FAILED: accepted $branch" >&2
            return 1
        fi
    done

    # A scratch repo on an off-policy branch, holding tag v1, answers the
    # push cases that resolve HEAD or a tag.
    local repo
    repo=$(mktemp -d)
    trap 'rm -rf "$repo"' RETURN
    (
        export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 # a signing default needs keys
        git -C "$repo" init -q -b chore/current
        git -C "$repo" -c user.name=t -c user.email=t@t commit -q --allow-empty -m t
        git -C "$repo" tag v1
        # A second checkout on its own branch, as a worktree beside it is.
        git -C "$repo" init -q -b fix/wt wt
        git -C "$repo/wt" -c user.name=t -c user.email=t@t commit -q --allow-empty -m t
    )

    # Pairs: command, then the branches it creates or pushes (comma-joined).
    local cases=(
        'git switch -c chore/x'                          'chore/x'
        "git switch -c 'feat/x'"                         'feat/x'
        'git switch -c "fix/y" origin/main'              'fix/y'
        'git switch --create chore/y'                    'chore/y'
        'git switch --create=fix/y'                      'fix/y'
        'git -c k=v switch -c chore/x'                   'chore/x'
        'git -C ../other checkout -b fix/y'              'fix/y'
        'git checkout -b bad/z; git checkout -b fix/y'   'bad/z,fix/y'
        'git switch -c chore/x>out'                      'chore/x'
        'git switch -c fix/x 2>&1 | tail -n 3'           'fix/x'
        'git fetch -q && git switch -q -c feat/z && git push -u origin HEAD' 'feat/z,chore/current'
        'git -C /repo worktree add ../wt -b fix/w'       'fix/w'
        'git branch chore/b origin/main'                 'chore/b'
        'git branch -m old fix/n'                        'fix/n'
        'git branch -D chore/b'                          ''
        'git branch --list "chore/*"'                    ''
        'git push'                                       'chore/current'
        'git push -u origin HEAD'                        'chore/current'
        'git push origin HEAD:fix/r'                     'fix/r'
        'git push origin chore/x fix/y'                  'chore/x,fix/y'
        'git push origin +refs/heads/fix/q'              'fix/q'
        'git push origin v1'                             ''
        'git tag v2 && git push origin v2'               ''
        'git tag -a v3 -m "rel v3" && git push origin v3' ''
        'git tag -d v4; git push origin v4'              'v4'
        'git tag -mrel -f v5 && git push origin v5'      ''
        'git tag --contains x5 && git push origin x5'    'x5'
        'git tag -n5 x6; git push origin x6'             'x6'
        'git tag v7 && git push origin HEAD:v7'          'v7'
        'git tag -am rel v8 && git push origin v8'       ''
        'git tag -sm rel v9 && git push origin v9:v9'    ''
        'git -C ../other tag v10 && git push origin v10' 'v10'
        'git -C . tag v11 && git push origin v11'        ''
        'git tag v12 && git -C ./ push origin v12'       ''
        'git tag --create-reflog v13 && git push origin v13' ''
        'cd wt && git push'                              'fix/wt'
        'cd wt; git push -u origin HEAD'                 'fix/wt'
        'cd wt && git -C .. push'                        'chore/current'
        'git push; cd wt'                                'chore/current'
        "cd $repo/wt && git push"                        'fix/wt'
        'cd -- wt && git push'                           'fix/wt'
        'cd -P wt && git push'                           'fix/wt'
        'builtin cd wt && git push'                      'fix/wt'
        'pushd wt && git push'                           'fix/wt'
        'cd - && git push'                               ''
        'pushd wt && popd && git push'                   ''
        'cd "$WT" && git push'                           ''
        'pushd +1 && git push'                           ''
        'pushd && git push'                              ''
        'pushd -1 && git push'                           ''
        'pushd -n wt && git push'                        ''
        'git push origin --delete chore/x'               ''
        'git push origin :chore/x'                       ''
        'git log --grep "git switch -c foo/x"'           ''
        'echo "git checkout -B chore/x"'                 ''
        "$(printf 'cat <<EOF\ngit switch -c chore/x\nEOF')" ''
        "$(printf '# git switch -c chore/x\nls')"        ''
        'git commit -m "x; git switch -c chore/x"'      ''
        'git switch main'                                ''
        'git switch -c "unbalanced'                      ''
    )
    local i got
    for ((i = 0; i < ${#cases[@]}; i += 2)); do
        got=$(hook_input "${cases[i]}" "$repo" | python3 -c "$parse_command" | paste -sd, -)
        [ "$got" = "${cases[i + 1]}" ] || {
            echo "self-test FAILED: '${cases[i]}' gave '$got', want '${cases[i + 1]}'" >&2
            return 1
        }
    done

    hook_input 'git switch -c fix/ok' "$repo" | hook || { echo "self-test FAILED: hook refused fix/ok" >&2; return 1; }
    local status=0
    hook_input 'git switch -c chore/no' "$repo" | hook 2>/dev/null || status=$?
    [ "$status" = 2 ] || { echo "self-test FAILED: hook exited $status on chore/no, want 2" >&2; return 1; }
    [ -z "$(echo 'not json' | hook 2>&1)" ] || { echo "self-test FAILED: hook was not silent on bad input" >&2; return 1; }
    echo "self-test OK"
}

case "${1:-}" in
    --self-test) self_test; exit $? ;;
    --hook) hook; exit $? ;;
    "") usage ;;
esac

branch=$1
actor=${2:-}
head_repository=${3:-}
base_repository=${4:-}
[ -n "$actor" ] && [ -n "$head_repository" ] && [ -n "$base_repository" ] || usage

# Fork contributors do not control the source repository's branch policy.
[ "$head_repository" != "$base_repository" ] && exit 0

if ! valid_branch "$branch" "$actor"; then
    echo "::error::branch '$branch' $rule" >&2
    exit 1
fi
