#!/usr/bin/env bash
set -euo pipefail

# Shared by the focused Make target and the full smoke suite; test the baked script.
docker run --rm -i --entrypoint bash "${1:?usage: test-graphify-hooks.sh IMAGE}" <<'GRAPHIFY_HOOK_TEST'
set -euo pipefail
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
export HOME="$root/home" CLAUDE_CONFIG_DIR="$root/home/.claude"
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
mkdir -p "$HOME" "$root/repo/graphify-out"
cd "$root/repo"
git init -q
git config user.email smoke@example.invalid
git config user.name Smoke
git config core.hooksPath .githooks
printf 'graphify-out/\n' > .gitignore
graphify hook install >/dev/null
# Empty pins are the repository's portable choice, not a stale installation.
python3 - <<'PY'
from pathlib import Path
import re
for hook in Path('.githooks').glob('post-*'):
    hook.write_text(re.sub(r"^_PINNED='[^']*'$", "_PINNED=''", hook.read_text(), flags=re.M))
Path('.gitattributes').unlink()
PY
git add .gitignore .githooks
git -c core.hooksPath=/dev/null commit -qm fixture
/usr/local/lib/toolbox/init.d/30-graphify.sh
[ -z "$(git status --porcelain)" ] || { git status --short; echo 'FAILED: graphify boot dirtied tracked hooks'; exit 1; }
# A stale payload must still refresh, preserving user code outside the block.
mkdir "$root/expected"
python3 - "$root/expected" <<'PY'
from pathlib import Path
import sys
for hook in Path('.githooks').glob('post-*'):
    current = hook.read_text() + "\n_PINNED='/team/python'\n"
    assert '# Installed by: graphify hook install' in current, hook
    (Path(sys.argv[1]) / hook.name).write_text(current)
    hook.write_text(current.replace('# Installed by: graphify hook install', '# stale graphify payload'))
PY
/usr/local/lib/toolbox/init.d/30-graphify.sh
for hook in .githooks/post-*; do
    cmp "$hook" "$root/expected/${hook##*/}" || { echo 'FAILED: hook refresh lost the payload upgrade or user code'; exit 1; }
done
# Existing attributes belong to the user, even if every line mentions graphify.
for attrs in tracked multiple exact; do
    printf 'graphify-out/graph.json merge=graphify\n' > .gitattributes
    if [ "$attrs" = multiple ]; then printf 'important.json merge=graphify\n' >> .gitattributes; fi
    if [ "$attrs" = tracked ]; then git add .gitattributes; fi
    cp .gitattributes "$root/expected-attributes"
    /usr/local/lib/toolbox/init.d/30-graphify.sh
    cmp .gitattributes "$root/expected-attributes" || { echo 'FAILED: user attributes were changed'; exit 1; }
    if [ "$attrs" = tracked ]; then git rm -qf .gitattributes; else rm .gitattributes; fi
done
# A dangling attributes symlink is existing user content, not an absent file.
ln -s "$root/user-attributes" .gitattributes
/usr/local/lib/toolbox/init.d/30-graphify.sh
[ -L .gitattributes ] || { echo 'FAILED: graphify boot removed a pre-existing attributes symlink'; exit 1; }
rm .gitattributes "$root/user-attributes"
# A tracked graph still needs the generated merge rule, even under an ignored dir.
printf '{}\n' > graphify-out/graph.json
git add -f graphify-out/graph.json
/usr/local/lib/toolbox/init.d/30-graphify.sh
[ -f .gitattributes ] || { echo 'FAILED: merge attributes removed for a tracked graph'; exit 1; }
git rm -qf graphify-out/graph.json
mkdir -p graphify-out
rm .gitattributes
# Husky's wrapper directory redirects graphify to the user-editable parent.
mv .githooks .husky
git config core.hooksPath .husky/_
git add -A
git -c core.hooksPath=/dev/null commit -qm husky-fixture
/usr/local/lib/toolbox/init.d/30-graphify.sh
[ -z "$(git status --porcelain)" ] || { git status --short; echo 'FAILED: graphify boot dirtied tracked Husky hooks'; exit 1; }
# A tracked symlink is still a hook, not a file for the normaliser to replace.
mv .husky/post-commit .husky/shared-commit
ln -s shared-commit .husky/post-commit
git add .husky
git -c core.hooksPath=/dev/null commit -qm symlink-fixture
/usr/local/lib/toolbox/init.d/30-graphify.sh
[ -L .husky/post-commit ] && [ -z "$(git status --porcelain)" ] || { git status --short; echo 'FAILED: graphify boot replaced a hook symlink or dirtied its target'; exit 1; }
# Resolve a hooks-directory symlink before applying graphify's Husky mapping.
ln -s .husky/_ .hooks-link
git add .hooks-link
git config core.hooksPath .hooks-link
git -c core.hooksPath=/dev/null commit -qm hooks-directory-symlink-fixture
/usr/local/lib/toolbox/init.d/30-graphify.sh
[ -z "$(git status --porcelain)" ] || { git status --short; echo 'FAILED: graphify boot dirtied hooks behind a directory symlink'; exit 1; }
# No custom hooks path: keep graphify's interpreter pin in the private hooks.
git config --unset core.hooksPath
/usr/local/lib/toolbox/init.d/30-graphify.sh
python3 - <<'PY'
from pathlib import Path
import re
for name in ('post-commit', 'post-checkout', 'post-merge', 'post-rewrite'):
    hook = Path('.git/hooks') / name
    assert re.search(r"^_PINNED='[^']+'$", hook.read_text(), re.M), hook
PY
[ ! -e .gitattributes ] || { echo 'FAILED: useless attributes survived with default hooks'; exit 1; }
echo 'OK: portable tracked hooks refresh, user code and attributes survive, private hooks keep their pin'
GRAPHIFY_HOOK_TEST
