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
// CLI named by $AUTHED_CLI holds a session, and it answers from the branch
// name: "merged-*" has a merged PR, "closed-*" has none, "boom-*" makes the
// query itself fail. A merged PR's head commit is the forge's view of what was
// merged: the oid in <git-dir>/forge-<branch> when a test writes one (a file,
// not a ref, so it can name an object the repository lacks), else the local
// tip. Every answer is the JSON array shape the script's --jq filter produces.
func fakeForgeBin(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	const script = `#!/bin/sh
me=$(basename "$0")
if [ "${1:-}" = "auth" ]; then
	[ "${AUTHED_CLI:-}" = "$me" ] || exit 1
	echo "$me: logged in to ${3:-}"
	exit 0
fi
echo "$me $*" >> "${FORGE_CALLS:-/dev/null}"
for a in "$@"; do
	case "$a" in
	merged-*)
		f="$(git rev-parse --git-dir)/forge-$a"
		if [ -f "$f" ]; then read -r head < "$f"; else head=$(git rev-parse --verify -q "refs/heads/$a"); fi
		echo "[\"$head\"]"; exit 0 ;;
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
	return bin
}

// forgeHead sets the head commit the fake forge reports for branch's merged PR.
func forgeHead(t *testing.T, dir, branch, oid string) {
	t.Helper()
	gitDir := gitIn(t, dir, "rev-parse", "--absolute-git-dir")
	if err := os.WriteFile(filepath.Join(gitDir, "forge-"+branch), []byte(oid+"\n"), 0o644); err != nil {
		t.Fatalf("write forge head: %v", err)
	}
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

	out := runPruneDead(t, dir,
		fakeForgeBin(t)+string(os.PathListSeparator)+toolPath(t), "AUTHED_CLI=gh")

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
		cli, origin string
		want        []string
	}{
		{"gh", "https://github.com/example/repo.git", []string{
			"--repo example/repo", // not whatever remote gh would have picked
			"select(.isCrossRepository == false)",
			"| .headRefOid]",
		}},
		{"glab", "git@git.example.internal:team/repo.git", []string{
			"--repo team/repo",
			"select(.source_project_id != null and .source_project_id == .project_id)",
			"| .sha]",
		}},
	} {
		t.Run(tc.cli, func(t *testing.T) {
			dir := pruneDeadRepo(t, tc.origin, "merged-one")
			calls := filepath.Join(t.TempDir(), "calls")

			runPruneDead(t, dir,
				fakeForgeBin(t)+string(os.PathListSeparator)+toolPath(t),
				"AUTHED_CLI="+tc.cli, "FORGE_CALLS="+calls)

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
	out := runPruneDead(t, dir,
		fakeForgeBin(t)+string(os.PathListSeparator)+toolPath(t), "AUTHED_CLI=")

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

	out := runPruneDead(t, dir,
		fakeForgeBin(t)+string(os.PathListSeparator)+toolPath(t), "AUTHED_CLI=glab")

	if strings.Contains(branches(t, dir), "merged-one") {
		t.Errorf("glab is logged in to this host, so its merged branch must go:\n%s", out)
	}
	if !strings.Contains(out, "git.example.internal") {
		t.Errorf("the host asked must be named in the output, got:\n%s", out)
	}
}

// A merged PR proves its head was merged, not the local tip. A branch that
// gained commits after the merge — a follow-up never pushed, or one pushed to
// a remote branch deleted on merge — holds the only copy of them, so it must
// survive, and say why in words distinct from "never merged".
func TestGitPruneDeadKeepsCommitsAfterTheMergedHead(t *testing.T) {
	dir := pruneDeadRepo(t, "https://github.com/example/repo.git", "merged-then-more")
	forgeHead(t, dir, "merged-then-more", gitIn(t, dir, "rev-parse", "merged-then-more"))
	tree := gitIn(t, dir, "rev-parse", "merged-then-more^{tree}")
	after := gitIn(t, dir, "commit-tree", "-p", "merged-then-more", "-m", "after the merge", tree)
	gitIn(t, dir, "update-ref", "refs/heads/merged-then-more", after)

	out := runPruneDead(t, dir,
		fakeForgeBin(t)+string(os.PathListSeparator)+toolPath(t), "AUTHED_CLI=gh")

	if !strings.Contains(branches(t, dir), "merged-then-more") {
		t.Fatalf("a branch with commits after its merged head must survive:\n%s", out)
	}
	if !strings.Contains(out, "kept merged-then-more (no merged PR on gh contains its tip") {
		t.Errorf("the kept branch must be named with its own reason, got:\n%s", out)
	}
}

// The other direction is safe: when more was pushed to the PR from elsewhere
// before it merged, the local tip is an ancestor of the merged head, so every
// local commit is part of what the forge merged and the branch can go.
func TestGitPruneDeadDeletesATipBehindTheMergedHead(t *testing.T) {
	dir := pruneDeadRepo(t, "https://github.com/example/repo.git", "merged-behind")
	tree := gitIn(t, dir, "rev-parse", "merged-behind^{tree}")
	head := gitIn(t, dir, "commit-tree", "-p", "merged-behind", "-m", "pushed from elsewhere", tree)
	forgeHead(t, dir, "merged-behind", head)

	out := runPruneDead(t, dir,
		fakeForgeBin(t)+string(os.PathListSeparator)+toolPath(t), "AUTHED_CLI=gh")

	if strings.Contains(branches(t, dir), "merged-behind") {
		t.Errorf("a tip contained in the merged head must go, still present:\n%s", out)
	}
}

// A merged head the repository does not have cannot vouch for the local tip,
// and it is never fetched to find out: absent and different from the tip
// means the branch stays.
func TestGitPruneDeadKeepsABranchWhoseMergedHeadIsMissingLocally(t *testing.T) {
	dir := pruneDeadRepo(t, "https://github.com/example/repo.git", "merged-elsewhere")
	forgeHead(t, dir, "merged-elsewhere", strings.Repeat("0123456789", 4))

	out := runPruneDead(t, dir,
		fakeForgeBin(t)+string(os.PathListSeparator)+toolPath(t), "AUTHED_CLI=gh")

	if !strings.Contains(branches(t, dir), "merged-elsewhere") {
		t.Fatalf("a merged head absent locally must not count as proof:\n%s", out)
	}
	if !strings.Contains(out, "kept merged-elsewhere (no merged PR on gh contains its tip") {
		t.Errorf("the kept branch must be named with its reason, got:\n%s", out)
	}
}

// A branch name reused after an older PR with that name merged: the forge
// answers with the old PR's head, a commit the repository still has but that
// shares no history with the new work.
func TestGitPruneDeadKeepsABranchWhoseNameWasReused(t *testing.T) {
	dir := pruneDeadRepo(t, "https://github.com/example/repo.git", "merged-reused")
	tree := gitIn(t, dir, "rev-parse", "merged-reused^{tree}")
	old := gitIn(t, dir, "commit-tree", "-m", "the older PR with this name", tree)
	forgeHead(t, dir, "merged-reused", old)

	out := runPruneDead(t, dir,
		fakeForgeBin(t)+string(os.PathListSeparator)+toolPath(t), "AUTHED_CLI=gh")

	if !strings.Contains(branches(t, dir), "merged-reused") {
		t.Fatalf("an unrelated merged head must not count as proof:\n%s", out)
	}
	if !strings.Contains(out, "kept merged-reused (no merged PR on gh contains its tip") {
		t.Errorf("the kept branch must be named with its reason, got:\n%s", out)
	}
}
