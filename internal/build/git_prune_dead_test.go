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
// merged: the oids in <git-dir>/forge-<branch>, one per merged PR, when a test
// writes them (a file, not a ref, so it can name an object the repository
// lacks), else the local tip. Every answer is the JSON array shape the
// script's --jq filter produces.
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
		if [ -f "$f" ]; then
			list=
			while IFS= read -r h; do
				case "$h" in null) h=null ;; *) h="\"$h\"" ;; esac
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
		cli, origin string
		want        []string
	}{
		{"gh", "https://github.com/example/repo.git", []string{
			"--repo example/repo", // not whatever remote gh would have picked
			"select(.isCrossRepository == false)",
			"--json headRefOid,", // the field the projection below reads
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
	// The reason the script prints when no merged head covers the tip.
	const uncovered = "no merged PR on gh contains its tip"
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
		kept: "merge state unverified",
	}, {
		// The shape --jq gives when the field is missing altogether.
		name: "merged PR whose head is null",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, "null")
		},
		kept: "merge state unverified",
	}, {
		// The headless PR might be the one that covers the tip, so an
		// unrelated head beside it cannot settle the question either way.
		name: "headless merged PR beside an unrelated head",
		setup: func(t *testing.T, dir, branch string) {
			forgeHeads(t, dir, branch, "", unrelated(t, dir, branch))
		},
		kept: "merge state unverified",
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
