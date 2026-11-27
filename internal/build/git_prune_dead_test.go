package build

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// fakeForgeBin writes stand-in `gh`, `glab` and `curl` binaries, the seam that makes
// the script's decision observable without a network or an account. Only the
// CLI named by $AUTHED_CLI holds a session, and it answers from the branch
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
	echo "$me: logged in to ${3:-}"
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
	// curl is the Gitea API: page 1 of the closed PRs is the JSON in
	// $CURL_BODY, any later page the empty one that ends the listing — or the
	// same JSON again when $CURL_EVERY_PAGE is set, a repository with more
	// closed PRs than the script reads. $CURL_FAIL makes every call fail,
	// $CURL_FAIL_PAGE=<n> only page n. Its stdin, where the token travels, is
	// appended to $CURL_STDIN. jq is not faked: the real one runs the
	// script's projection over these pages.
	const curl = `#!/bin/sh
echo "curl $*" >> "${FORGE_CALLS:-/dev/null}"
cat >> "${CURL_STDIN:-/dev/null}"
[ -z "${CURL_FAIL:-}" ] || exit 22
case "$*" in *"&page=${CURL_FAIL_PAGE:-none}") exit 22 ;; esac
case "$*" in
*'&page=1') cat "${CURL_BODY:-/dev/null}" ;;
*) if [ -n "${CURL_EVERY_PAGE:-}" ]; then cat "$CURL_BODY"; else echo '[]'; fi ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(curl), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	return bin
}

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
	cmd.Env = append(append(os.Environ(), "PATH="+path), env...)
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
			"--repo example/repo", // not whatever remote gh would have picked
			"select(.isCrossRepository == false)",
			"--json headRefOid,", // the field the projection below reads
			"| .headRefOid]",
		}},
		{"glab scp form", "glab", "git@git.example.internal:team/repo.git", []string{
			"--repo team/repo",
			"select(.source_project_id != null and .source_project_id == .project_id)",
			"| .sha]",
		}},
		// A self-hosted forge often serves SSH or HTTPS on its own port: the
		// port is part of the host, never the first segment of the
		// repository path. The trailing space ends the value, so a slug
		// with a leftover suffix fails too.
		{"glab ssh with port", "glab", "ssh://git@git.example.internal:2222/team/repo.git", []string{
			"--repo team/repo ",
		}},
		{"gh https with port", "gh", "https://git.example.internal:8443/team/repo.git", []string{
			"--repo team/repo ",
		}},
		// The host is what the session is looked up by: a bracketed IPv6
		// literal stays whole, colons and all.
		{"glab ipv6 with port", "glab", "ssh://git@[fd00::1]:2222/team/repo.git", []string{
			"auth status --hostname [fd00::1]\n",
			"--repo team/repo ",
		}},
		// scp form with an absolute path, and a trailing slash after .git:
		// both spell the same repository.
		{"glab scp form with absolute path", "glab", "git@git.example.internal:/team/repo.git", []string{
			"--repo team/repo ",
		}},
		{"gh trailing slash", "gh", "https://git.example.internal/team/repo.git/", []string{
			"--repo team/repo ",
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
	// not being logged in.
	out := runPruneDead(t, dir, forgePath(t), "AUTHED_CLI=")

	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("with no logged-in CLI no branch may be deleted:\n%s", out)
	}
	if !strings.Contains(out, "auth login") {
		t.Errorf("the run must say how to log in, got:\n%s", out)
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

// giteaPath is forgePath plus the real jq: the Gitea projection is the
// filter that keeps a stranger's merge from force-deleting local work, so it
// runs for real rather than being pinned by its text.
func giteaPath(t *testing.T) string {
	t.Helper()
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Fatalf("jq is needed to run the Gitea projection (in gotest.Dockerfile and the CI runner): %v", err)
	}
	return forgePath(t) + string(os.PathListSeparator) + filepath.Dir(jq)
}

// giteaRepo is a repository on a Forgejo/Gitea host with a token file
// configured for it, the opt-in that stands in for a logged-in CLI. It
// returns the repository and the token's value.
func giteaRepo(t *testing.T, origin string, branches ...string) (string, string) {
	t.Helper()
	dir := pruneDeadRepo(t, origin, branches...)
	const token = "s3cret-token"
	tokenFile := filepath.Join(t.TempDir(), "token")
	// CRLF, as an editor on another OS might save it.
	if err := os.WriteFile(tokenFile, []byte(token+"\r\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	gitIn(t, dir, "config", "prune-dead.https://git.example.internal.giteaTokenFile", tokenFile)
	return dir, token
}

const giteaOrigin = "ssh://git@git.example.internal:222/team/repo.git"

// giteaPR is one closed PR as the Gitea API lists it, reduced to the fields
// the projection reads. headRepo 0 is a head repository the API reports as
// null (a deleted fork).
func giteaPR(merged bool, ref, label, sha string, headRepo int) string {
	repo := "null"
	if headRepo != 0 {
		repo = strconv.Itoa(headRepo)
	}
	return fmt.Sprintf(`{"merged":%t,"head":{"ref":%q,"label":%q,"sha":%q,"repo_id":%s},"base":{"ref":"main","label":"main","repo_id":5}}`,
		merged, ref, label, sha, repo)
}

// writeGiteaPage writes the JSON array the fake curl serves as page 1.
func writeGiteaPage(t *testing.T, prs ...string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "page1.json")
	if err := os.WriteFile(f, []byte("["+strings.Join(prs, ",")+"]\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	return f
}

// Forgejo and Gitea have no CLI to hold a session, so a configured token
// file is what lets the script ask — and it decides on the same proof as for
// gh and glab: a merged PR of this repository whose head is the local tip.
// Each branch below meets that proof but for one field, run through the real
// projection.
func TestGitPruneDeadAsksAGiteaHostWithAConfiguredToken(t *testing.T) {
	names := []string{"deleted-head", "live-head", "closed-one", "fork-label", "fork-repo", "gone-fork", "absent"}
	dir, _ := giteaRepo(t, giteaOrigin, names...)
	tip := gitIn(t, dir, "rev-parse", "main")
	page := writeGiteaPage(t,
		// Forgejo rewrites head.ref once the branch is deleted on merge; the
		// name survives in head.label.
		giteaPR(true, "refs/pull/52/head", "deleted-head", tip, 5),
		giteaPR(true, "live-head", "live-head", tip, 5),
		giteaPR(false, "closed-one", "closed-one", tip, 5),
		// A fork's PR: label owner:branch, head in another repository.
		giteaPR(true, "refs/pull/7/head", "stranger:fork-label", tip, 9),
		// Same label, other repository: the repo_id check alone rejects it.
		giteaPR(true, "fork-repo", "fork-repo", tip, 9),
		giteaPR(true, "gone-fork", "gone-fork", tip, 0),
	)

	out := runPruneDead(t, dir, giteaPath(t), "AUTHED_CLI=", "CURL_BODY="+page)

	left := branches(t, dir)
	for _, gone := range []string{"deleted-head", "live-head"} {
		if strings.Contains(left, gone+"\n") {
			t.Errorf("%s has a merged PR of this repository at its tip, still present:\n%s\n%s", gone, left, out)
		}
	}
	for _, kept := range []string{"closed-one", "fork-label", "fork-repo", "gone-fork", "absent"} {
		if !strings.Contains(left, kept+"\n") {
			t.Errorf("%s has no merged PR of this repository and must survive:\n%s\n%s", kept, left, out)
		}
		if !strings.Contains(out, "kept "+kept+" (no merged PR on gitea") {
			t.Errorf("%s must be named with its reason, got:\n%s", kept, out)
		}
	}
	if !strings.Contains(out, "merged according to gitea on git.example.internal") {
		t.Errorf("the run must name the forge and host it asked, got:\n%s", out)
	}
}

// The token travels on curl's stdin, never in an argv a process listing
// shows, and without the CRLF its file was saved with; the CA is what git
// trusts, GIT_SSL_CAINFO over http.<url>.sslCAInfo; and the PR list is read
// once for every branch.
func TestGitPruneDeadGiteaQueryShape(t *testing.T) {
	for _, tc := range []struct {
		name, env, ca string
	}{
		{"CA from git config", "", "/etc/forge-ca.pem"},
		{"CA from GIT_SSL_CAINFO", "GIT_SSL_CAINFO=/env/ca.pem", "/env/ca.pem"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, token := giteaRepo(t, giteaOrigin, "merged-one", "closed-one")
			gitIn(t, dir, "config", "http.https://git.example.internal.sslCAInfo", "/etc/forge-ca.pem")
			calls := filepath.Join(t.TempDir(), "calls")
			stdin := filepath.Join(t.TempDir(), "stdin")

			runPruneDead(t, dir, giteaPath(t), "AUTHED_CLI=", "FORGE_CALLS="+calls, "CURL_STDIN="+stdin,
				"CURL_BODY="+writeGiteaPage(t, giteaPR(false, "other", "other", "", 5)), tc.env)

			argv, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("no forge call recorded: %v", err)
			}
			if strings.Contains(string(argv), token) {
				t.Errorf("the token must never reach argv, got:\n%s", argv)
			}
			if got, _ := os.ReadFile(stdin); !strings.Contains(string(got), "Authorization: token "+token+"\"\n") {
				t.Errorf("the token must reach curl on stdin, CR stripped, got %q", got)
			}
			var curls []string
			for _, line := range strings.Split(string(argv), "\n") {
				if strings.HasPrefix(line, "curl ") {
					curls = append(curls, line)
				}
			}
			// One listed page and the empty page that ends the listing, for
			// both branches together.
			if len(curls) != 2 {
				t.Fatalf("want the PR list read once (2 pages), got %d calls:\n%s", len(curls), argv)
			}
			for _, want := range []string{
				"--cacert " + tc.ca + " ",
				"https://git.example.internal/api/v1/repos/team/repo/pulls?state=closed&sort=recentupdate&limit=50&page=1",
			} {
				if !strings.Contains(curls[0], want) {
					t.Errorf("curl call %q is missing %q", curls[0], want)
				}
			}
		})
	}
}

// An HTTPS origin names the web server itself, port included: that is the
// API base, the base the token key is matched against — git's urlmatch reads
// no port as 443, never as any port — and so the key the hint names.
func TestGitPruneDeadGiteaBaseFollowsAnHTTPSOrigin(t *testing.T) {
	t.Run("hint before the key is set", func(t *testing.T) {
		dir := pruneDeadRepo(t, "https://git.example.internal:3000/team/repo.git", "merged-one")

		out := runPruneDead(t, dir, giteaPath(t), "AUTHED_CLI=")

		if !strings.Contains(out, "prune-dead.https://git.example.internal:3000.giteaTokenFile") {
			t.Errorf("the hint must name the key with the origin's port, got:\n%s", out)
		}
	})

	dir := pruneDeadRepo(t, "https://git.example.internal:3000/team/repo.git", "merged-one")
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("tok\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	gitIn(t, dir, "config", "prune-dead.https://git.example.internal:3000.giteaTokenFile", tokenFile)
	calls := filepath.Join(t.TempDir(), "calls")

	runPruneDead(t, dir, giteaPath(t), "AUTHED_CLI=", "FORGE_CALLS="+calls, "CURL_BODY="+writeGiteaPage(t))

	argv, _ := os.ReadFile(calls)
	if want := "https://git.example.internal:3000/api/v1/repos/team/repo/pulls?"; !strings.Contains(string(argv), want) {
		t.Errorf("want the API asked at %q, got:\n%s", want, argv)
	}
}

// An API that cannot answer — a revoked token, an untrusted CA, a host down —
// means "don't know": every branch stays, named with the reason.
func TestGitPruneDeadKeepsEverythingWhenTheGiteaAPIFails(t *testing.T) {
	dir, _ := giteaRepo(t, giteaOrigin, "merged-one")

	out := runPruneDead(t, dir, giteaPath(t), "AUTHED_CLI=", "CURL_FAIL=1")

	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("a failed API call must delete nothing:\n%s", out)
	}
	if !strings.Contains(out, "kept merged-one (merge state unverified — gitea could not answer)") {
		t.Errorf("the kept branch must be named with the reason, got:\n%s", out)
	}
}

// A failure on any page, not only the first, leaves the list partial: a
// merge on the missing page would read as "no merged PR", so every branch
// is unverified instead.
func TestGitPruneDeadKeepsEverythingWhenALaterGiteaPageFails(t *testing.T) {
	dir, _ := giteaRepo(t, giteaOrigin, "merged-one")
	tip := gitIn(t, dir, "rev-parse", "main")
	page := writeGiteaPage(t, giteaPR(true, "merged-one", "merged-one", tip, 5))

	out := runPruneDead(t, dir, giteaPath(t), "AUTHED_CLI=", "CURL_BODY="+page, "CURL_FAIL_PAGE=2")

	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("a partial listing must delete nothing, even a branch it shows as merged:\n%s", out)
	}
	if !strings.Contains(out, "kept merged-one (merge state unverified — gitea could not answer)") {
		t.Errorf("the kept branch must be named with the reason, got:\n%s", out)
	}
}

// A repository with more closed PRs than the script reads stops at the page
// cap rather than paging forever, and a branch not found in what was read is
// kept with that reason, not as "closed, or never opened".
func TestGitPruneDeadStopsAtTheGiteaPageCap(t *testing.T) {
	dir, _ := giteaRepo(t, giteaOrigin, "old-merge")
	calls := filepath.Join(t.TempDir(), "calls")
	page := writeGiteaPage(t, giteaPR(true, "other", "other", "", 5))

	out := runPruneDead(t, dir, giteaPath(t), "AUTHED_CLI=", "CURL_BODY="+page, "CURL_EVERY_PAGE=1", "FORGE_CALLS="+calls)

	argv, _ := os.ReadFile(calls)
	if n := strings.Count(string(argv), "curl "); n != 20 {
		t.Errorf("want the listing capped at 20 pages, got %d calls", n)
	}
	if !strings.Contains(branches(t, dir), "old-merge") {
		t.Errorf("a branch not found within the cap must survive:\n%s", out)
	}
	if !strings.Contains(out, "kept old-merge (no merged PR among the most recent closed PRs on gitea") {
		t.Errorf("the reason must say older PRs were not read, got:\n%s", out)
	}
}

// Without a configured token the Gitea API is never called, even with curl
// and jq on PATH, and the hint names the key that enables it.
func TestGitPruneDeadLeavesAnUnconfiguredHostAlone(t *testing.T) {
	dir := pruneDeadRepo(t, giteaOrigin, "merged-one")
	calls := filepath.Join(t.TempDir(), "calls")

	out := runPruneDead(t, dir, giteaPath(t), "AUTHED_CLI=", "FORGE_CALLS="+calls)

	if argv, _ := os.ReadFile(calls); strings.Contains(string(argv), "curl ") {
		t.Errorf("no API call without a configured token, got:\n%s", argv)
	}
	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("with no forge to ask no branch may be deleted:\n%s", out)
	}
	if !strings.Contains(out, "prune-dead.https://git.example.internal.giteaTokenFile") {
		t.Errorf("the hint must name the config key, got:\n%s", out)
	}
}

// A token configured but unusable — here its file is missing — must not
// send the developer to set the key they already set.
func TestGitPruneDeadNamesAnUnusableGiteaToken(t *testing.T) {
	dir := pruneDeadRepo(t, giteaOrigin, "merged-one")
	gitIn(t, dir, "config", "prune-dead.https://git.example.internal.giteaTokenFile", filepath.Join(t.TempDir(), "missing"))

	out := runPruneDead(t, dir, giteaPath(t), "AUTHED_CLI=")

	if !strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("with no usable forge no branch may be deleted:\n%s", out)
	}
	if !strings.Contains(out, "a giteaTokenFile is set for 'git.example.internal'") || strings.Contains(out, "auth login") {
		t.Errorf("the run must say the configured token is unusable, not ask for a login, got:\n%s", out)
	}
}
