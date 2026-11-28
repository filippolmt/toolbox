package build

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pruneDeadRepo builds a throwaway repository whose branches all look
// abandoned the only way git can express it — an upstream configured but no
// remote-tracking ref, i.e. `[gone]`. That state is what a squash-merged PR
// and an abandoned one leave behind alike, which is the whole reason the
// script has to ask the forge. originURL decides which host the script sees;
// both git transports are dead-ended below so no case reaches the network.
func pruneDeadRepo(t *testing.T, originURL string, branches ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) { gitIn(t, dir, args...) }
	run("init", "--initial-branch=main", "--quiet")
	run("config", "remote.origin.url", originURL)
	run("config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	// Both transports the script's own `git fetch` might take are dead-ended,
	// so no case reaches the network: a closed port for https, a failing
	// command for the ssh/scp-form remotes.
	run("config", "http.proxy", "http://127.0.0.1:1")
	run("config", "core.sshCommand", "false")
	run("commit", "--allow-empty", "--quiet", "-m", "root")
	for _, b := range branches {
		run("branch", b)
		// Upstream recorded, remote-tracking ref absent → [gone].
		run("config", "branch."+b+".remote", "origin")
		run("config", "branch."+b+".merge", "refs/heads/"+b)
	}
	return dir
}

// fakeForgeBin writes stand-in `gh` and `glab` binaries, the seam that makes
// the script's decision observable without a network or an account. Only the
// CLI named by $AUTHED_CLI holds a session — on every host, or only on the
// space-separated $AUTHED_HOSTS when set — and it answers from the branch
// name: "merged-*" has a merged PR, "closed-*" has none, "boom-*" makes the
// query itself fail. A merged PR's head commit is the forge's view of what was
// merged: the oids in <git-dir>/forge-<branch>, one per merged PR, when a test
// writes them (a file, not a ref, so it can name an object the repository
// lacks), else the local tip. Every answer is the JSON array shape the
// script's --jq filter produces. Every call, auth checks included, is
// appended to $FORGE_CALLS when set.
func fakeForgeBin(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	const script = `#!/bin/sh
me=$(basename "$0")
echo "$me $*" >> "${FORGE_CALLS:-/dev/null}"
if [ "${1:-}" = "auth" ]; then
	[ "${AUTHED_CLI:-}" = "$me" ] || exit 1
	case " $* " in
	*" --json "* | *" --all "*)
		# The session listing, in each CLI's own JSON shape: gh groups the
		# accounts by host, glab lists one entry per host.
		list=
		# glab also lists, here first, a host whose session fails, with
		# the ssh_host its config names: $FAKE_EXPIRED_HOSTS
		# ("host=sshhost ...").
		if [ "$me" = glab ]; then
			for kv in ${FAKE_EXPIRED_HOSTS:-}; do
				e="{\"state\":\"error\",\"host\":\"${kv%%=*}\",\"ssh_host\":\"${kv#*=}\"}"
				list="$list${list:+,}$e"
			done
		fi
		for h in ${AUTHED_HOSTS:-}; do
			e="{\"state\":\"success\",\"host\":\"$h\""
			# $FAKE_GLAB_SSH_HOSTS ("host=sshhost ...") gives a host the
			# ssh_host glab's config names for it.
			for kv in ${FAKE_GLAB_SSH_HOSTS:-}; do
				[ "$me" = glab ] && [ "${kv%%=*}" = "$h" ] && e="$e,\"ssh_host\":\"${kv#*=}\""
			done
			e="$e}"
			[ "$me" = gh ] && e="\"$h\":[$e]"
			list="$list${list:+,}$e"
		done
		if [ "$me" = gh ]; then echo "{\"hosts\":{$list}}"; else echo "{\"hosts\":[$list]}"; fi
		exit 0 ;;
	esac
	# auth status --hostname H: any host unless $AUTHED_HOSTS names them.
	case " ${AUTHED_HOSTS:-${4:-}} " in *" ${4:-} "*) ;; *) exit 1 ;; esac
	echo "$me: logged in to ${4:-}"
	exit 0
fi
# api --hostname H <endpoint>: the repository's SSH clone URL that
# $FAKE_SSH_URLS ("host=url ...") gives H, under the field each forge names it
# by, from a host holding a session.
if [ "${1:-}" = "api" ]; then
	[ "${AUTHED_CLI:-}" = "$me" ] || exit 1
	case " ${AUTHED_HOSTS:-} " in *" ${3:-} "*) ;; *) exit 1 ;; esac
	url=
	for kv in ${FAKE_SSH_URLS:-}; do
		case "$kv" in "${3:-}="*) url=${kv#*=} ;; esac
	done
	field=ssh_url_to_repo
	[ "$me" = gh ] && field=ssh_url
	echo "{\"$field\":\"$url\"}"
	exit 0
fi
for a in "$@"; do
	case "$a" in
	merged-*)
		f="$(git rev-parse --git-dir)/forge-$a"
		if [ -f "$f" ]; then
			list=
			while IFS= read -r h; do
				case "$h" in null) ;; *) h="\"$h\"" ;; esac # a JSON null stays bare
				list="$list${list:+,}$h"
			done < "$f"
		else
			list="\"$(git rev-parse --verify -q "refs/heads/$a")\""
		fi
		echo "[$list]"; exit 0 ;;
	closed-*) echo '[]'; exit 0 ;;
	boom-*) exit 1 ;;
	esac
done
echo '[]'
`
	for _, name := range []string{"gh", "glab"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(fakeForgejoCurl), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	return bin
}

// fakeForgejoCurl stands in for curl against a Forgejo instance at
// $FORGEJO_HOST; any other host fails the way an unreachable or non-Forgejo
// one does. It records its argv and, apart, the credential its --config
// stdin carried, so a test can tell the two apart. Pull lists answer from the
// head= branch name, as the gh/glab fakes do, in the JSON shape of Forgejo's
// GET /repos/{owner}/{repo}/pulls: "merged-*" one merged PR of that branch,
// "closed-*" one closed unmerged, "boom-*" a failed request, "forkonly-*"
// merged PRs that prove nothing (a fork's same-named branch, a deleted head
// repository, another branch an old server returns because it ignores
// head=), "paged-*" its merged PR on page 2 behind a short page 1, "live-*"
// a merged PR whose branch still exists on the server (head.sha is then that
// branch's current tip, not what was merged), "nullpage-*" a JSON null where
// a list belongs, "boom2-*" a failure on page 2. $FAKE_FORGEJO_VERSION, when
// set, replaces the version probe's body; $FAKE_FORGEJO_SIGNIN makes every
// anonymous request fail, as REQUIRE_SIGNIN_VIEW does. A merged PR's head is
// the first oid in <git-dir>/forge-<branch> when a test wrote one (see
// forgeHeads), else the local tip. A head= value is URL-decoded the way the
// server would.
const fakeForgejoCurl = `#!/bin/sh
calls=${FORGE_CALLS:-/dev/null}
echo "curl-argv $*" >> "$calls"
url= cfg=
while [ $# -gt 0 ]; do
	case "$1" in
	--config) [ "$2" = - ] && cfg=$(cat); shift ;;
	--cacert) echo "curl --cacert $2" >> "$calls"; shift ;;
	http*) url=$1 ;;
	esac
	shift
done
auth=none
case "$cfg" in
*'header = "Authorization: token '*) auth=${cfg#*token }; auth="token ${auth%%\"*}" ;;
*'user = "'*) auth=${cfg#*user = \"}; auth="user ${auth%%\"*}" ;;
esac
echo "curl $url auth=$auth" >> "$calls"
host=${url#*://}
host=${host%%/*}
[ -n "${FORGEJO_HOST:-}" ] && [ "$host" = "$FORGEJO_HOST" ] || exit 22
# An instance with REQUIRE_SIGNIN_VIEW answers nothing anonymously, the
# version endpoint included.
[ -n "${FAKE_FORGEJO_SIGNIN:-}" ] && [ "$auth" = none ] && exit 22
case "$url" in
*/api/forgejo/v1/version) printf '%s\n' "${FAKE_FORGEJO_VERSION-{\"version\":\"17.0.2+gitea-1.22.0\"}}"; exit 0 ;;
esac
b= page=1
IFS='&'
for kv in ${url#*\?}; do
	case "$kv" in
	head=*) b=$(printf '%s' "${kv#head=}" | sed 's/%2F/\//g') ;;
	page=*) page=${kv#page=} ;;
	esac
done
unset IFS
tip=$(git rev-parse --verify -q "refs/heads/$b")
f="$(git rev-parse --git-dir)/forge-$b"
[ -f "$f" ] && tip=$(head -n 1 "$f")
pr() { printf '{"merged":%s,"head":{"label":"%s","ref":"%s","sha":"%s","repo_id":%s},"base":{"repo_id":1}}' "$1" "$2" "${5:-refs/pull/1/head}" "$3" "$4"; }
case "$page:$b" in
*:boom-*) exit 22 ;;
2:boom2-*) exit 22 ;;
1:boom2-*) echo "[$(pr true other "$tip" 1)]"; exit 0 ;;
1:nullpage-*) echo null; exit 0 ;;
1:live-*) echo "[$(pr true "$b" "$tip" 1 "$b")]"; exit 0 ;;
1:livetip-*) echo "[$(pr true "$b" "$tip" 1 "$b"),$(pr true "$b" 0000000000000000000000000000000000000000 1)]"; exit 0 ;;
1:mixed-*) echo "[$(pr true "$b" 0000000000000000000000000000000000000000 1 "$b"),$(pr true "$b" "$tip" 1)]"; exit 0 ;;
1:merged-*) echo "[$(pr true "$b" "$tip" 1)]"; exit 0 ;;
1:closed-*) echo "[$(pr false "$b" "$tip" 1)]"; exit 0 ;;
1:forkonly-*) echo "[$(pr true "$b" "$tip" 2),$(pr true "$b" "$tip" -1),$(pr true other "$tip" 1)]"; exit 0 ;;
1:paged-*) echo "[$(pr true other "$tip" 1)]"; exit 0 ;;
2:paged-*) echo "[$(pr true "$b" "$tip" 1)]"; exit 0 ;;
esac
echo '[]'
`

// forgeHeads sets the head commits the fake forge reports for branch's merged
// PRs, one per PR; an empty oid is a PR whose answer carries no head, and
// "null" is sent as a JSON null, what --jq projects for a missing field.
func forgeHeads(t *testing.T, dir, branch string, oids ...string) {
	t.Helper()
	gitDir := gitIn(t, dir, "rev-parse", "--absolute-git-dir")
	body := strings.Join(oids, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "forge-"+branch), []byte(body), 0o644); err != nil {
		t.Fatalf("write forge head: %v", err)
	}
}

// forgePath is a PATH with the fake forge CLIs ahead of git and sh.
func forgePath(t *testing.T) string {
	t.Helper()
	return fakeForgeBin(t) + string(os.PathListSeparator) + toolPath(t)
}

// toolPath is a PATH holding just the interpreters the script itself needs —
// git and sh — and deliberately no forge CLI, so a gh installed on the machine
// running the tests cannot leak into a case that must see none.
func toolPath(t *testing.T) string {
	t.Helper()
	dirs := map[string]bool{}
	for _, tool := range []string{"git", "sh"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("look up %s: %v", tool, err)
		}
		dirs[filepath.Dir(p)] = true
	}
	var out []string
	for d := range dirs {
		out = append(out, d)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// runPruneDead runs the embedded script (the artefact the image actually
// ships) against dir, under exactly the PATH given plus any extra env.
func runPruneDead(t *testing.T, dir, path string, env ...string) string {
	t.Helper()
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/git-prune-dead")
	if err != nil {
		t.Fatalf("read embedded git-prune-dead: %v", err)
	}
	script := filepath.Join(t.TempDir(), "git-prune-dead")
	if err := os.WriteFile(script, body, 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cmd := exec.Command("sh", script)
	cmd.Dir = dir
	// The machine's git config could carry a credential helper or CA, and
	// a real curl on PATH would reach the network: neither may leak in.
	cmd.Env = append(append(os.Environ(), "PATH="+path,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"http_proxy=http://127.0.0.1:1", "https_proxy=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1", "NO_PROXY=", "no_proxy="), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git-prune-dead: %v\n%s", err, out)
	}
	return string(out)
}

func branches(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("for-each-ref: %v", err)
	}
	return string(out)
}

// A gone upstream is not proof of a merge: a squash-merged PR and one closed
// without merging both leave exactly that state behind, and the second still
// holds the only copy of its commits. So the script deletes on a merged PR
// and on nothing else.
func TestGitPruneDeadDeletesOnlyProvablyMergedBranches(t *testing.T) {
	dir := pruneDeadRepo(t, "https://github.com/example/repo.git", "merged-one", "closed-one", "boom-one")

	out := runPruneDead(t, dir, forgePath(t), "AUTHED_CLI=gh")

	left := branches(t, dir)
	if strings.Contains(left, "merged-one") {
		t.Errorf("a branch whose PR was merged must go, still present:\n%s\n%s", left, out)
	}
	// Closed without merging: the commits exist nowhere else.
	if !strings.Contains(left, "closed-one") {
		t.Errorf("a branch with no merged PR must survive:\n%s\n%s", left, out)
	}
	// A failed query means "don't know", never "not merged" — and never a
	// deletion. An expired token must not empty the repository.
	if !strings.Contains(left, "boom-one") {
		t.Errorf("a branch whose merge state could not be read must survive:\n%s\n%s", left, out)
	}
	// Silence would leave the user believing the branch was pruned.
	if !strings.Contains(out, "closed-one") || !strings.Contains(out, "boom-one") {
		t.Errorf("every kept branch must be named with its reason, got:\n%s", out)
	}
}

// Neither CLI scopes its branch filter to the head repository: on cli/cli one
// branch name returns merged PRs from thirty different fork owners. Reading
// that as proof would force-delete local work, so the query must carry both
// the repository it means and a filter rejecting merges that happened on a
// fork, and it must ask for the PR's head commit — on GitLab `sha`, not the
// merge or squash commit, which a local tip never equals. The fakes answer on
// the branch name alone and cannot catch a wrong flag or field, so the argv
// itself is what gets pinned here.
func TestGitPruneDeadScopesTheQueryToThisRepository(t *testing.T) {
	for _, tc := range []struct {
		name, cli, origin string
		want              []string
	}{
		{"gh", "gh", "https://github.com/example/repo.git", []string{
			// Not whatever remote gh would have picked, and on the host whose
			// session was checked: a bare OWNER/REPO goes to the CLI's
			// default host instead.
			"--repo github.com/example/repo ",
			"select(.isCrossRepository == false)",
			"--json headRefOid,", // the field the projection below reads
			"| .headRefOid]",
		}},
		{"glab scp form", "glab", "git@git.example.internal:team/repo.git", []string{
			"--repo https://git.example.internal/team/repo ",
			"select(.source_project_id != null and .source_project_id == .project_id)",
			"| .sha]",
		}},
		// A self-hosted forge often serves SSH or HTTPS on its own port: the
		// port is part of the host, never the first segment of the
		// repository path. The trailing space ends the value, so a slug
		// with a leftover suffix fails too.
		{"glab ssh with port", "glab", "ssh://git@git.example.internal:2222/team/repo.git", []string{
			"--repo https://git.example.internal/team/repo ",
		}},
		{"gh https with port", "gh", "https://git.example.internal:8443/team/repo.git", []string{
			"--repo git.example.internal/team/repo ",
		}},
		// The host is what the session is looked up by: a bracketed IPv6
		// literal stays whole, colons and all.
		{"glab ipv6 with port", "glab", "ssh://git@[fd00::1]:2222/team/repo.git", []string{
			"auth status --hostname [fd00::1]\n",
			"--repo https://[fd00::1]/team/repo ",
		}},
		// scp form with an absolute path, and a trailing slash after .git:
		// both spell the same repository.
		{"glab scp form with absolute path", "glab", "git@git.example.internal:/team/repo.git", []string{
			"--repo https://git.example.internal/team/repo ",
		}},
		{"gh trailing slash", "gh", "https://git.example.internal/team/repo.git/", []string{
			"--repo git.example.internal/team/repo ",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := pruneDeadRepo(t, tc.origin, "merged-one")
			calls := filepath.Join(t.TempDir(), "calls")

			runPruneDead(t, dir, forgePath(t), "AUTHED_CLI="+tc.cli, "FORGE_CALLS="+calls)

			argv, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("no forge call recorded: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(argv), want) {
					t.Errorf("forge call %q is missing %q", strings.TrimSpace(string(argv)), want)
				}
			}
		})
	}
}

// With no CLI logged in to origin's host, nothing is provable — so nothing is
// deleted. The alternative (falling back to deleting every gone upstream)
// makes the same command silently destructive exactly when it cannot check,
// which is the trap this script exists to avoid. An expired token is the
// everyday way to land here, so the message has to name the way out.
func TestGitPruneDeadKeepsEverythingWhenNoCLIIsLoggedIn(t *testing.T) {
	dir := pruneDeadRepo(t, "https://github.com/example/repo.git", "merged-one")

	// The fakes are on PATH but neither holds a session: being installed is
	// not being logged in. jq comes explicitly: the Forgejo hint below needs
	// it, and toolPath finds it only where it shares git's directory.
	out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=")

	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("with no logged-in CLI no branch may be deleted:\n%s", out)
	}
	if !strings.Contains(out, "auth login") {
		t.Errorf("the run must say how to log in, got:\n%s", out)
	}
	// gh and glab are not the only way out: a Forgejo instance needs no CLI.
	if !strings.Contains(out, "on a Forgejo instance: its API must answer /api/forgejo/v1/version on github.com") {
		t.Errorf("the run must name what a Forgejo instance needs, got:\n%s", out)
	}
	// Named here too: with no forge to ask, a branch that survives in silence
	// reads as one that was pruned.
	if !strings.Contains(out, "merged-one") {
		t.Errorf("each kept branch must still be named without a forge, got:\n%s", out)
	}
}

// The forge is not guessed from the domain name — a self-hosted GitLab has no
// "gitlab" in its host, and a GitHub Enterprise host has no "github". What
// settles it is which CLI holds a session for that exact host, which is also
// the only thing that makes the query work at all.
func TestGitPruneDeadPicksTheCLILoggedInToThatHost(t *testing.T) {
	dir := pruneDeadRepo(t, "git@git.example.internal:team/repo.git", "merged-one")

	out := runPruneDead(t, dir, forgePath(t), "AUTHED_CLI=glab")

	if strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("glab is logged in to this host, so its merged branch must go:\n%s", out)
	}
	if !strings.Contains(out, "git.example.internal") {
		t.Errorf("the host asked must be named in the output, got:\n%s", out)
	}
}

// A merged PR proves its head was merged, not the local tip. Each case sets
// the forge's view of what was merged against the local branch and checks
// the one outcome the script may reach: deleted only when a merged head is the
// tip or contains it, otherwise kept and named with its reason.
func TestGitPruneDeadProofMustCoverTheLocalTip(t *testing.T) {
	// child writes a commit on top of parent without moving any branch.
	child := func(t *testing.T, dir, parent, msg string) string {
		t.Helper()
		tree := gitIn(t, dir, "rev-parse", parent+"^{tree}")
		return gitIn(t, dir, "commit-tree", "-p", parent, "-m", msg, tree)
	}
	// unrelated writes a root commit sharing no history with branch: the head
	// of an older PR that once had the same name.
	unrelated := func(t *testing.T, dir, branch string) string {
		t.Helper()
		tree := gitIn(t, dir, "rev-parse", branch+"^{tree}")
		return gitIn(t, dir, "commit-tree", "-m", "the older PR with this name", tree)
	}
	// The reasons the script prints when no merged head covers the tip, and
	// when a merged PR came back without one: gh did answer, so not "could
	// not answer".
	const (
		uncovered = "no merged PR on gh contains its tip — commits after the merge, a reused name, or a merged head not fetched here"
		headless  = "merge state unverified — a merged PR on gh reports no head"
	)
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir, branch string)
		kept  string // reason prefix the run must print; "" means deleted
	}{{
		// A follow-up never pushed, or pushed to a remote branch deleted on
		// merge: the only copy of those commits.
		name: "commits after the merged head",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, gitIn(t, dir, "rev-parse", branch))
			gitIn(t, dir, "update-ref", "refs/heads/"+branch, child(t, dir, branch, "after the merge"))
		},
		kept: uncovered,
	}, {
		// More was pushed to the PR from elsewhere before it merged: every
		// local commit is part of what the forge merged.
		name: "tip behind the merged head",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, child(t, dir, branch, "pushed from elsewhere"))
		},
	}, {
		// Absent locally and different from the tip: never fetched to find out.
		name: "merged head missing locally",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, strings.Repeat("0123456789", 4))
		},
		kept: uncovered,
	}, {
		// A name reused after an older PR merged: its head is still here but
		// shares no history with the new work.
		name: "reused name with an unrelated merged head",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, unrelated(t, dir, branch))
		},
		kept: uncovered,
	}, {
		// A reused name whose latest PR merged with exactly this tip: one
		// covering head among several merged PRs is proof enough.
		name: "one of several merged heads is the tip",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, unrelated(t, dir, branch), gitIn(t, dir, "rev-parse", branch))
		},
	}, {
		// A field the forge stopped sending says nothing about local commits.
		name: "merged PR without a head",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, "")
		},
		kept: headless,
	}, {
		// The shape --jq gives when the field is missing altogether.
		name: "merged PR whose head is null",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, "null")
		},
		kept: headless,
	}, {
		// The headless PR might be the one that covers the tip, so an
		// unrelated head beside it cannot settle the question either way.
		name: "headless merged PR beside an unrelated head",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, "", unrelated(t, dir, branch))
		},
		kept: headless,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			const branch = "merged-one"
			dir := pruneDeadRepo(t, "https://github.com/example/repo.git", branch)
			tc.setup(t, dir, branch)

			out := runPruneDead(t, dir, forgePath(t), "AUTHED_CLI=gh")

			survived := strings.Contains(branches(t, dir), branch)
			if tc.kept == "" {
				if survived {
					t.Errorf("a tip a merged head covers must go, still present:\n%s", out)
				}
				return
			}
			if !survived {
				t.Fatalf("a tip no merged head covers must survive:\n%s", out)
			}
			if want := "kept " + branch + " (" + tc.kept; !strings.Contains(out, want) {
				t.Errorf("want %q in the output, got:\n%s", want, out)
			}
		})
	}
}

// origin/HEAD is only refreshed by `git remote set-head`, so it can name a
// branch fetch --prune has removed — before the run, or during the script's
// own fetch, the sequence right after a remote default moves. It can also
// name a branch with no local copy. `git branch --merged` on any of those
// names dies with "malformed object name", so the default must fall back as
// if origin/HEAD were unset.
func TestGitPruneDeadFallsBackFromAnUnusableOriginHEAD(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{{
		name: "dangling before the run",
		setup: func(t *testing.T, dir string) {
			gitIn(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/old-default")
		},
	}, {
		// origin/HEAD dangles but a local branch of that name survives:
		// only the origin/HEAD resolve check keeps it from becoming the
		// default, which the forge loop would then skip.
		name: "dangling, with a local branch of that name",
		setup: func(t *testing.T, dir string) {
			gitIn(t, dir, "branch", "old-default")
			gitIn(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/old-default")
		},
	}, {
		name: "pruned by the run's own fetch",
		setup: func(t *testing.T, dir string) {
			remote := filepath.Join(t.TempDir(), "remote.git")
			gitIn(t, dir, "clone", "--quiet", "--bare", dir, remote)
			gitIn(t, remote, "branch", "old-default", "main")
			gitIn(t, dir, "remote", "set-url", "origin", remote)
			gitIn(t, dir, "fetch", "--quiet", "origin")
			gitIn(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/old-default")
			// The remote's default moves on and the old one is deleted; this
			// clone still resolves origin/HEAD until the script fetches.
			gitIn(t, remote, "branch", "-D", "old-default")
		},
	}, {
		name: "no local branch of that name",
		setup: func(t *testing.T, dir string) {
			gitIn(t, dir, "update-ref", "refs/remotes/origin/develop", "main")
			gitIn(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := pruneDeadRepo(t, "https://github.com/example/repo.git")
			gitIn(t, dir, "branch", "worktree-agent-done")
			tc.setup(t, dir)
			gitIn(t, dir, "switch", "--quiet", "-c", "work")

			out := runPruneDead(t, dir, toolPath(t))

			if strings.Contains(out, "malformed object name") {
				t.Errorf("an unusable origin/HEAD must not reach git as a revision, got:\n%s", out)
			}
			if strings.Contains(branches(t, dir), "worktree-agent-done") {
				t.Errorf("a worktree-agent-* leftover merged into main must go:\n%s", out)
			}
			if !strings.Contains(out, "kept main, current work") {
				t.Errorf("the default must fall back to main, got:\n%s", out)
			}
		})
	}
}

// A local path is not the scp shorthand: git reads host:path only when a
// colon comes before the first slash, so ../repo has no host and no forge
// to ask. Asking one anyway — about a host named ".." — could only answer
// for some other repository.
func TestGitPruneDeadAsksNoForgeForALocalRemote(t *testing.T) {
	for _, origin := range []string{"../repo", "/srv/git/repo.git", "./a:b/repo", "file:///srv/git/repo.git"} {
		t.Run(origin, func(t *testing.T) {
			dir := pruneDeadRepo(t, origin, "merged-one")
			calls := filepath.Join(t.TempDir(), "calls")

			out := runPruneDead(t, dir, forgePath(t), "AUTHED_CLI=gh", "FORGE_CALLS="+calls)

			if argv, err := os.ReadFile(calls); err == nil {
				t.Errorf("no forge may be asked about a local remote, got calls:\n%s", argv)
			}
			if !strings.Contains(branches(t, dir), "merged-one") {
				t.Errorf("with no forge to ask no branch may be deleted:\n%s", out)
			}
			// Not "log in": no login would make a local path answerable.
			if !strings.Contains(out, "origin is a local path") || strings.Contains(out, "auth login") {
				t.Errorf("the run must say origin is a local path, not ask for a login, got:\n%s", out)
			}
		})
	}
}

// When origin/HEAD names a branch with no local copy, the remote default is
// still what a worktree-agent-* leftover was merged into: measuring it
// against a fallback main instead keeps every leftover main has not caught
// up with.
func TestGitPruneDeadSweepsAgainstARemoteOnlyDefault(t *testing.T) {
	dir := pruneDeadRepo(t, "https://github.com/example/repo.git")
	gitIn(t, dir, "switch", "--quiet", "-c", "worktree-agent-done")
	gitIn(t, dir, "commit", "--allow-empty", "--quiet", "-m", "merged on develop only")
	gitIn(t, dir, "update-ref", "refs/remotes/origin/develop", "worktree-agent-done")
	gitIn(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	gitIn(t, dir, "switch", "--quiet", "-c", "work", "main")

	out := runPruneDead(t, dir, toolPath(t))

	if strings.Contains(branches(t, dir), "worktree-agent-done") {
		t.Errorf("a leftover merged into origin's default must go even with no local copy of it:\n%s", out)
	}
}

// forgejoPath is forgePath plus jq, which the Forgejo client filters with.
func forgejoPath(t *testing.T) string {
	t.Helper()
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Fatalf("look up jq: %v", err)
	}
	return forgePath(t) + string(os.PathListSeparator) + filepath.Dir(jq)
}

// Forgejo has no gh or glab session to find, so its API answers instead,
// asked about the same thing: merged PRs whose head is this repository's
// branch. Filtering is the client's job too — a server older than Forgejo
// v16 ignores head= and returns every closed PR, and a fork's same-named
// branch or a deleted head repository proves nothing about local work. A
// page shorter than the limit asked for is not the last one, since an
// instance caps the limit at its own maximum; only an empty page is.
func TestGitPruneDeadAsksForgejoOverItsAPI(t *testing.T) {
	for _, tc := range []struct{ name, origin, host, api string }{
		// The SSH port is not the web one: the API is asked over HTTPS.
		{"ssh with port", "ssh://git@forge.example.internal:222/team/repo.git", "forge.example.internal", "https://forge.example.internal/api/v1"},
		{"https with port", "https://forge.example.internal:8443/team/repo.git", "forge.example.internal:8443", "https://forge.example.internal:8443/api/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := pruneDeadRepo(t, tc.origin, "merged-one", "merged-feat/two", "closed-one", "boom-one",
				"forkonly-one", "paged-one", "live-one", "mixed-one", "livetip-one", "nullpage-one", "boom2-one")
			gitIn(t, dir, "config", "credential.helper", "!f() { echo username=me; echo password=s3cret; }; f")
			calls := filepath.Join(t.TempDir(), "calls")

			// An environment token names no host, so it could reach any
			// instance the probe finds: it is never sent.
			out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=", "FORGEJO_HOST="+tc.host,
				"FORGEJO_TOKEN=envtoken", "FORGE_CALLS="+calls)

			left := branches(t, dir)
			// mixed-one: a live PR of the same name proves nothing either way,
			// so the deleted-branch PR that covers the tip still settles it.
			for _, gone := range []string{"merged-one", "merged-feat/two", "paged-one", "mixed-one"} {
				if strings.Contains(left, gone) {
					t.Errorf("%s has a merged PR on Forgejo and must go:\n%s", gone, out)
				}
			}
			for _, kept := range []string{
				"kept closed-one (no merged PR on forgejo",
				"kept forkonly-one (no merged PR on forgejo",
				"kept boom-one (merge state unverified — forgejo could not answer)",
				// head.sha of a branch still on the server is its tip now,
				// commits after the merge included: no proof of a merge, and
				// said so rather than "never opened".
				"kept live-one (merged on forgejo, but its branch still exists there",
				// A live PR's head is never proof, even when it equals the tip:
				// only the gone branch's head counts, and it does not cover.
				"kept livetip-one (no merged PR on forgejo contains its tip",
				// A failure is never "not merged", wherever it lands.
				"kept nullpage-one (merge state unverified — forgejo could not answer)",
				"kept boom2-one (merge state unverified — forgejo could not answer)",
			} {
				if !strings.Contains(out, kept) {
					t.Errorf("want %q in the output, got:\n%s", kept, out)
				}
			}
			argv, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("no forge call recorded: %v", err)
			}
			want := "curl " + tc.api + "/repos/team/repo/pulls?state=closed&head=merged-one&limit=50&page=1 auth=user me:s3cret"
			if !strings.Contains(string(argv), want) {
				t.Errorf("want %q among the calls, got:\n%s", want, argv)
			}
			// The token goes only to a host already known to run Forgejo.
			probe := "curl " + strings.TrimSuffix(tc.api, "/v1") + "/forgejo/v1/version auth=none"
			if !strings.Contains(string(argv), probe) {
				t.Errorf("want the probe %q, sent with no credential, got:\n%s", probe, argv)
			}
			if strings.Contains(string(argv), "envtoken") {
				t.Errorf("FORGEJO_TOKEN must never be sent, got calls:\n%s", argv)
			}
			// On argv the credential would show in ps to every local user.
			for _, line := range strings.Split(string(argv), "\n") {
				if strings.HasPrefix(line, "curl-argv ") && strings.Contains(line, "s3cret") {
					t.Errorf("the credential must reach curl on stdin, not argv: %s", line)
				}
			}
		})
	}
}

// The credential git itself would use for the instance is the one to send —
// never prompted for, since the run is not interactive — and none at all
// still reads a public repository. TLS trusts what git trusts for that URL:
// a self-hosted instance's private CA lives in git's http.<url>.sslCAInfo.
func TestGitPruneDeadUsesGitsCredentialAndCAForForgejo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config [][2]string
		want   string
	}{
		// The helper echoes GCM_INTERACTIVE back as the password: what it
		// saw is what the bridge shim turns into no_prompt.
		{"stored credential", [][2]string{{"credential.helper", "!f() { echo username=me; echo password=gcm-$GCM_INTERACTIVE; }; f"}}, "auth=user me:gcm-never"},
		{"no credential", nil, "auth=none"},
		{"private CA", [][2]string{{"http.https://forge.example.internal/.sslCAInfo", "/etc/forge-ca.pem"}}, "curl --cacert /etc/forge-ca.pem"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := pruneDeadRepo(t, "git@forge.example.internal:team/repo.git", "merged-one")
			for _, kv := range tc.config {
				gitIn(t, dir, "config", kv[0], kv[1])
			}
			calls := filepath.Join(t.TempDir(), "calls")

			out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=", "FORGEJO_HOST=forge.example.internal",
				"FORGE_CALLS="+calls)

			if strings.Contains(branches(t, dir), "merged-one") {
				t.Errorf("a merged PR on Forgejo must delete its branch:\n%s", out)
			}
			argv, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("no forge call recorded: %v", err)
			}
			if !strings.Contains(string(argv), tc.want) {
				t.Errorf("want %q among the calls, got:\n%s", tc.want, argv)
			}
		})
	}
}

// Only a JSON answer naming a version marks a Forgejo instance: a catch-all
// web front answers 200 with HTML for any path, and must not be asked about
// pull requests as if it were one.
func TestGitPruneDeadProbesForgejoByItsVersionAnswer(t *testing.T) {
	dir := pruneDeadRepo(t, "git@forge.example.internal:team/repo.git", "merged-one")
	calls := filepath.Join(t.TempDir(), "calls")

	out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=", "FORGEJO_HOST=forge.example.internal",
		"FAKE_FORGEJO_VERSION=<!doctype html><html></html>", "FORGE_CALLS="+calls)

	if strings.Contains(out, "according to forgejo") {
		t.Errorf("an HTML answer must not mark the host as Forgejo, got:\n%s", out)
	}
	if argv, _ := os.ReadFile(calls); strings.Contains(string(argv), "/pulls?") {
		t.Errorf("no pull request may be asked of a host that is not Forgejo, got:\n%s", argv)
	}
	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("with no forge to ask no branch may be deleted:\n%s", out)
	}
}

// Over plain HTTP a credential travels in clear text to whoever answers, so
// an http:// origin is asked anonymously — a public repository still answers.
func TestGitPruneDeadSendsNoForgejoCredentialOverHTTP(t *testing.T) {
	dir := pruneDeadRepo(t, "http://forge.example.internal/team/repo.git", "merged-one")
	gitIn(t, dir, "config", "credential.helper", "!f() { echo username=me; echo password=pw; }; f")
	calls := filepath.Join(t.TempDir(), "calls")

	out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=", "FORGEJO_HOST=forge.example.internal",
		"FORGE_CALLS="+calls)

	argv, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("no forge call recorded: %v", err)
	}
	if strings.Contains(string(argv), "me:pw") {
		t.Errorf("no credential may be sent over http, got calls:\n%s", argv)
	}
	if strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("a public repository still answers anonymously, so merged-one must go:\n%s", out)
	}
}

// A merged PR proves its head, not commits made locally after it: the tip
// must be that head or behind it, the same check gh and glab answers get.
func TestGitPruneDeadForgejoProofMustCoverTheLocalTip(t *testing.T) {
	dir := pruneDeadRepo(t, "git@forge.example.internal:team/repo.git", "merged-one")
	forgeHeads(t, dir, "merged-one", gitIn(t, dir, "rev-parse", "merged-one"))
	gitIn(t, dir, "switch", "--quiet", "merged-one")
	gitIn(t, dir, "commit", "--allow-empty", "--quiet", "-m", "after the merge")
	gitIn(t, dir, "switch", "--quiet", "main")

	out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=", "FORGEJO_HOST=forge.example.internal")

	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Fatalf("a commit after the merged head must keep the branch:\n%s", out)
	}
	if want := "kept merged-one (no merged PR on forgejo contains its tip"; !strings.Contains(out, want) {
		t.Errorf("want %q in the output, got:\n%s", want, out)
	}
}

// An instance with REQUIRE_SIGNIN_VIEW refuses even its version endpoint to
// an anonymous caller. The probe goes out bare first; only when that fails
// does it retry with the credential git already sends to that host.
func TestGitPruneDeadFindsASignInOnlyForgejo(t *testing.T) {
	dir := pruneDeadRepo(t, "git@forge.example.internal:team/repo.git", "merged-one")
	gitIn(t, dir, "config", "credential.helper", "!f() { echo username=me; echo password=pw; }; f")
	calls := filepath.Join(t.TempDir(), "calls")

	out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=", "FORGEJO_HOST=forge.example.internal",
		"FAKE_FORGEJO_SIGNIN=1", "FORGE_CALLS="+calls)

	if strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("a sign-in-only instance answers with the credential, so merged-one must go:\n%s", out)
	}
	argv, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("no forge call recorded: %v", err)
	}
	// Bare first: a host is sent the credential only after it refused
	// an anonymous caller.
	bare := strings.Index(string(argv), "curl https://forge.example.internal/api/forgejo/v1/version auth=none\n")
	authed := strings.Index(string(argv), "curl https://forge.example.internal/api/forgejo/v1/version auth=user me:pw\n")
	if bare < 0 || authed < 0 || bare > authed {
		t.Errorf("want a bare probe, then one with the credential, got:\n%s", argv)
	}
}

// An instance served under a sub-path shows it in an http(s) origin: a
// Forgejo repository is always owner/repo, so whatever precedes those two
// segments is the instance's web root, and the API lives under it.
func TestGitPruneDeadFindsForgejoUnderASubPath(t *testing.T) {
	dir := pruneDeadRepo(t, "https://forge.example.internal/code/forgejo/team/repo.git", "merged-one")
	calls := filepath.Join(t.TempDir(), "calls")

	out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=", "FORGEJO_HOST=forge.example.internal",
		"FORGE_CALLS="+calls)

	if strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("a sub-path instance is still Forgejo, so merged-one must go:\n%s", out)
	}
	argv, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("no forge call recorded: %v", err)
	}
	want := "curl https://forge.example.internal/code/forgejo/api/v1/repos/team/repo/pulls?state=closed&head=merged-one&"
	if !strings.Contains(string(argv), want) {
		t.Errorf("want %q among the calls, got:\n%s", want, argv)
	}
}

// An instance can serve SSH under a name of its own (git-ssh.acme.test) while
// its API, and the CLI's session, live under another (code.acme.test). No CLI
// holds a session for the SSH name, so each host a CLI is logged in to is
// asked for this repository, and accepted only when the SSH clone URL it
// reports has origin's host and port: proof from the forge, not a guess from
// the name. The merge query then goes to that API host.
func TestGitPruneDeadFindsTheAPIHostBehindADedicatedSSHHost(t *testing.T) {
	for _, tc := range []struct {
		name, cli, origin, sshURL string
		want                      []string
	}{
		{"glab nested group", "glab", "git@git-ssh.acme.test:group/sub/app.git",
			"git@git-ssh.acme.test:group/sub/app.git", []string{
				"glab api --hostname code.acme.test projects/group%2Fsub%2Fapp\n",
				"--repo https://code.acme.test/group/sub/app ",
			}},
		{"gh", "gh", "git@git-ssh.acme.test:group/app.git",
			"git@git-ssh.acme.test:group/app.git", []string{
				"gh api --hostname code.acme.test repos/group/app\n",
				"--repo code.acme.test/group/app ",
			}},
		// SSH's default port, named or not, is the same endpoint.
		{"glab explicit default port", "glab", "ssh://git@git-ssh.acme.test:22/group/app.git",
			"git@git-ssh.acme.test:group/app.git", []string{
				"--repo https://code.acme.test/group/app ",
			}},
		// Any other SSH port is part of the proof: both sides must name it.
		{"glab ssh with port", "glab", "ssh://git@git-ssh.acme.test:2222/group/app.git",
			"ssh://git@git-ssh.acme.test:2222/group/app.git", []string{
				"--repo https://code.acme.test/group/app ",
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := pruneDeadRepo(t, tc.origin, "merged-one", "closed-one")
			calls := filepath.Join(t.TempDir(), "calls")

			// alt.acme.test, asked first, holds the same path but serves it
			// over another SSH host: refused, and the search goes on.
			out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI="+tc.cli,
				"AUTHED_HOSTS=alt.acme.test code.acme.test", "FORGE_CALLS="+calls,
				"FAKE_SSH_URLS=alt.acme.test=git@ssh.alt.test:group/app.git code.acme.test="+tc.sshURL)

			left := branches(t, dir)
			if strings.Contains(left, "merged-one") {
				t.Errorf("the API host proved it serves origin, so the merged branch must go:\n%s", out)
			}
			if !strings.Contains(left, "closed-one") {
				t.Errorf("a branch with no merged PR must survive:\n%s", out)
			}
			if !strings.Contains(out, "on code.acme.test") {
				t.Errorf("the run must name the API host it asked, got:\n%s", out)
			}
			argv, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("no forge call recorded: %v", err)
			}
			for _, want := range append(tc.want, " api --hostname alt.acme.test ") {
				if !strings.Contains(string(argv), want) {
					t.Errorf("want %q among the calls, got:\n%s", want, argv)
				}
			}
		})
	}
}

// A host the CLI is logged in to proves nothing unless the clone URL it
// reports is origin's: another instance can hold a project at the same path.
// Nothing proven, nothing deleted — and the run says which hosts it asked.
func TestGitPruneDeadKeepsEverythingWhenNoSessionHostServesOrigin(t *testing.T) {
	for _, tc := range []struct{ name, origin, sshURL string }{
		{"another SSH host", "git@git-ssh.acme.test:group/app.git", "git@ssh.elsewhere.test:group/app.git"},
		{"another SSH port", "ssh://git@git-ssh.acme.test:2222/group/app.git", "git@git-ssh.acme.test:group/app.git"},
		{"no answer", "git@git-ssh.acme.test:group/app.git", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := pruneDeadRepo(t, tc.origin, "merged-one")

			out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=glab",
				"AUTHED_HOSTS=code.acme.test", "FAKE_SSH_URLS=code.acme.test="+tc.sshURL)

			if !strings.Contains(branches(t, dir), "merged-one") {
				t.Errorf("no host proved it serves origin, so no branch may go:\n%s", out)
			}
			if !strings.Contains(out, "no CLI logged in to 'git-ssh.acme.test'") {
				t.Errorf("the run must still say no CLI is logged in to origin's host, got:\n%s", out)
			}
			if !strings.Contains(out, "glab on code.acme.test") {
				t.Errorf("the run must name the session hosts it asked, got:\n%s", out)
			}
		})
	}
}

// A CLI logged in to origin's own host answers directly: the session listing
// and the API are never asked, so the common case costs nothing more.
func TestGitPruneDeadSkipsDiscoveryWhenOriginsHostHasASession(t *testing.T) {
	dir := pruneDeadRepo(t, "git@gitlab.example.test:team/repo.git", "merged-one")
	calls := filepath.Join(t.TempDir(), "calls")

	runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=glab", "FORGE_CALLS="+calls)

	argv, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("no forge call recorded: %v", err)
	}
	for _, never := range []string{" --all ", " --json hosts", " api "} {
		if strings.Contains(string(argv), never) {
			t.Errorf("a direct session must skip discovery, but %q was called:\n%s", never, argv)
		}
	}
}

// With no session on any host, the API host behind an SSH name is never looked
// for — and the run says so, rather than leaving it to be guessed.
func TestGitPruneDeadSaysWhenNoSessionHostWasAsked(t *testing.T) {
	dir := pruneDeadRepo(t, "git@git-ssh.acme.test:group/app.git", "merged-one")

	out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=")

	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("with no session no branch may go:\n%s", out)
	}
	for _, want := range []string{
		"no API host looked for behind SSH host 'git-ssh.acme.test': gh and glab list no session on another host",
		// A host that serves only SSH takes no login: the way out names the
		// web host instead.
		"if 'git-ssh.acme.test' serves only SSH, log in to the instance's web host instead",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the output, got:\n%s", want, out)
		}
	}
}

// glab's own config may already name origin's host as the ssh_host of an
// instance whose session has lapsed. That proves nothing about a merge, so
// nothing is deleted, but it is the host to log in to — not the SSH one.
func TestGitPruneDeadNamesTheInstanceWhoseSSHHostIsOrigin(t *testing.T) {
	dir := pruneDeadRepo(t, "git@git-ssh.acme.test:group/app.git", "merged-one")

	out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=glab", "AUTHED_HOSTS=other.acme.test",
		"FAKE_EXPIRED_HOSTS=code.acme.test=git-ssh.acme.test")

	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("a lapsed session proves no merge, so no branch may go:\n%s", out)
	}
	for _, want := range []string{
		"glab's config names 'git-ssh.acme.test' the SSH host of code.acme.test, whose session is not valid",
		"log in with: glab auth login --hostname code.acme.test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the output, got:\n%s", want, out)
		}
	}
	// The SSH host is no place to log in, and gh or Forgejo are not the
	// forge glab just named.
	for _, never := range []string{"auth login --hostname git-ssh.acme.test", "gh auth login", "Forgejo"} {
		if strings.Contains(out, never) {
			t.Errorf("want no %q in the output, got:\n%s", never, out)
		}
	}
}

// Logged in to the instance glab maps origin's SSH host to, yet its API did
// not report this repository there (no access, or not found): logging in
// again changes nothing, so the run says what to check instead.
func TestGitPruneDeadSaysWhenTheMappedInstanceDoesNotConfirm(t *testing.T) {
	dir := pruneDeadRepo(t, "git@git-ssh.acme.test:group/app.git", "merged-one")

	// alt.acme.test names the same ssh_host with a lapsed session, listed
	// first: the instance where glab is logged in still wins.
	out := runPruneDead(t, dir, forgejoPath(t), "AUTHED_CLI=glab", "AUTHED_HOSTS=code.acme.test",
		"FAKE_GLAB_SSH_HOSTS=code.acme.test=git-ssh.acme.test", "FAKE_EXPIRED_HOSTS=alt.acme.test=git-ssh.acme.test")

	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("no host confirmed origin, so no branch may go:\n%s", out)
	}
	want := "but code.acme.test did not report this repository there: check your access to it on code.acme.test"
	if !strings.Contains(out, want) {
		t.Errorf("want %q in the output, got:\n%s", want, out)
	}
	if strings.Contains(out, "auth login") || strings.Contains(out, "serves only SSH") {
		t.Errorf("a login is no fix for a session that already works, got:\n%s", out)
	}
}
