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
// script has to ask the forge. origin's URL makes it a GitHub repo (so the
// script reaches for gh) while http.proxy points at a closed port, so the
// script's own `git fetch` fails instantly instead of touching the network.
func pruneDeadRepo(t *testing.T, originURL string, branches ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--initial-branch=main", "--quiet")
	run("config", "remote.origin.url", originURL)
	run("config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	run("config", "http.proxy", "http://127.0.0.1:1")
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
// query itself fail. Every answer is the JSON array shape both real CLIs
// return.
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
	merged-*) echo '[{"number":1}]'; exit 0 ;;
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
