package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGit scripts the Git seam: Output returns the value keyed by the
// space-joined args (empty string + nil error by default), Run returns the
// keyed error (nil by default). Both record their calls so tests can assert the
// git the orchestration issued and, crucially, its order.
type fakeGit struct {
	outputs map[string]string
	outErrs map[string]error
	runErrs map[string]error
	gets    [][]string
	runs    [][]string
}

func newFakeGit() *fakeGit {
	return &fakeGit{outputs: map[string]string{}, outErrs: map[string]error{}, runErrs: map[string]error{}}
}

func (f *fakeGit) Output(args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.gets = append(f.gets, append([]string(nil), args...))
	if err := f.outErrs[key]; err != nil {
		return "", err
	}
	return f.outputs[key], nil
}

func (f *fakeGit) Run(args ...string) error {
	key := strings.Join(args, " ")
	f.runs = append(f.runs, append([]string(nil), args...))
	return f.runErrs[key]
}

// runIndex returns the index of the first recorded Run whose joined args equal
// key, or -1. Used to assert ordering between mutating git commands.
func (f *fakeGit) runIndex(key string) int {
	for i, r := range f.runs {
		if strings.Join(r, " ") == key {
			return i
		}
	}
	return -1
}

func (f *fakeGit) ranAny(substr string) bool {
	for _, r := range f.runs {
		if strings.Contains(strings.Join(r, " "), substr) {
			return true
		}
	}
	return false
}

const (
	commonDirKey = "rev-parse --path-format=absolute --git-common-dir"
	listKey      = "worktree list --porcelain"
)

// Rm removes the worktree BEFORE stopping the container and BEFORE deleting the
// branch — the ordering guard that keeps a refused removal from half-tearing
// down. A nil cli skips the container stop, so the git orchestration is asserted
// without a daemon.
func TestRmOrdersRemovalBeforeBranchDelete(t *testing.T) {
	f := newFakeGit()
	f.outputs[commonDirKey] = "/repo/.git" // repoRoot → /repo
	f.outputs[listKey] = "worktree /repo\nbranch refs/heads/main\n\n" +
		"worktree /repo/.worktrees/tbx-fix\nbranch refs/heads/fix\n"
	f.outputs["-C /repo/.worktrees/tbx-fix status --porcelain"] = "" // clean

	err := New(f).Rm(context.Background(), nil, RmOpts{Branch: "fix"})
	if err != nil {
		t.Fatalf("Rm: %v", err)
	}

	remove := f.runIndex("worktree remove /repo/.worktrees/tbx-fix")
	del := f.runIndex("-C /repo branch -d fix")
	if remove < 0 {
		t.Fatalf("worktree remove was not run: %v", f.runs)
	}
	if del < 0 {
		t.Fatalf("branch delete was not run: %v", f.runs)
	}
	if remove > del {
		t.Errorf("worktree remove (idx %d) must precede branch delete (idx %d): %v", remove, del, f.runs)
	}
}

// A dirty worktree without --force is refused up front: git worktree remove must
// never run, so nothing is torn down.
func TestRmRefusesDirtyWithoutForce(t *testing.T) {
	f := newFakeGit()
	f.outputs[commonDirKey] = "/repo/.git"
	f.outputs[listKey] = "worktree /repo/.worktrees/tbx-fix\nbranch refs/heads/fix\n"
	f.outputs["-C /repo/.worktrees/tbx-fix status --porcelain"] = " M dirty.go" // dirty

	err := New(f).Rm(context.Background(), nil, RmOpts{Branch: "fix"})
	if err == nil {
		t.Fatal("expected a refusal for a dirty worktree")
	}
	if f.ranAny("worktree remove") {
		t.Errorf("worktree remove must not run for a dirty tree: %v", f.runs)
	}
}

// A rebase already in progress is resumed (continue + push), never restarted:
// no fetch, no fresh rebase onto the base.
func TestSyncResumesRebaseInProgress(t *testing.T) {
	rebaseDir := t.TempDir() // exists → rebaseInProgress reads true
	f := newFakeGit()
	f.outputs[commonDirKey] = "/repo/.git"
	f.outputs[listKey] = "worktree /repo/.worktrees/tbx-fix\nbranch refs/heads/fix\n"
	f.outputs["-C /repo/.worktrees/tbx-fix rev-parse --path-format=absolute --git-path rebase-merge"] = rebaseDir

	if err := New(f).Sync(SyncOpts{Branch: "fix"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if !f.ranAny("rebase --continue") {
		t.Errorf("resume must run rebase --continue: %v", f.runs)
	}
	if f.ranAny("fetch") || f.ranAny("rebase origin/") {
		t.Errorf("resume must not fetch or start a fresh rebase: %v", f.runs)
	}
	if !f.ranAny("push --force-with-lease") {
		t.Errorf("resume must push after continue: %v", f.runs)
	}
}

// With no rebase in progress, Sync fetches the recorded base, rebases onto
// origin/<base>, and pushes — in that order.
func TestSyncFreshRebaseFetchesRebasesPushes(t *testing.T) {
	f := newFakeGit()
	f.outputs[commonDirKey] = "/repo/.git"
	f.outputs[listKey] = "worktree /repo/.worktrees/tbx-fix\nbranch refs/heads/fix\n"
	// rebaseInProgress false: both probe paths do not exist.
	f.outputs["-C /repo/.worktrees/tbx-fix rev-parse --path-format=absolute --git-path rebase-merge"] = "/nope/rebase-merge"
	f.outputs["-C /repo/.worktrees/tbx-fix rev-parse --path-format=absolute --git-path rebase-apply"] = "/nope/rebase-apply"
	f.outputs["-C /repo config --get branch.fix.base"] = "main" // recorded base

	if err := New(f).Sync(SyncOpts{Branch: "fix"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	fetch := f.runIndex("-C /repo/.worktrees/tbx-fix fetch origin main")
	rebase := f.runIndex("-C /repo/.worktrees/tbx-fix rebase origin/main")
	push := f.runIndex("-C /repo/.worktrees/tbx-fix push --force-with-lease")
	if fetch < 0 || rebase < 0 || push < 0 {
		t.Fatalf("expected fetch, rebase, push; got %v", f.runs)
	}
	if fetch >= rebase || rebase >= push {
		t.Errorf("expected order fetch<rebase<push, got %d<%d<%d: %v", fetch, rebase, push, f.runs)
	}
}

// List filters to toolbox worktrees and, with a nil client (no daemon), reports
// every one as absent.
func TestListFiltersToToolboxWorktreesAbsentWhenNoDaemon(t *testing.T) {
	f := newFakeGit()
	f.outputs[commonDirKey] = "/repo/.git"
	f.outputs[listKey] = "worktree /repo\nbranch refs/heads/main\n\n" +
		"worktree /repo/.worktrees/tbx-fix\nbranch refs/heads/fix\n\n" +
		"worktree /repo/.worktrees/other\nbranch refs/heads/stray\n"

	rows, err := New(f).List(context.Background(), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 toolbox worktree, got %d: %+v", len(rows), rows)
	}
	if rows[0].Branch != "fix" || rows[0].Status != "absent" {
		t.Errorf("row = %+v, want {fix absent /repo/.worktrees/tbx-fix}", rows[0])
	}
}

// Create fetches the base, adds a --no-track worktree branched from
// origin/<base>, and returns the repo root + new worktree path.
func TestCreatePreparesWorktree(t *testing.T) {
	f := newFakeGit()
	f.outputs[commonDirKey] = "/repo/.git"                                            // repoRoot → /repo
	f.outputs["symbolic-ref --short refs/remotes/origin/HEAD"] = "origin/main"        // default base
	f.outputs["-C /repo rev-parse --path-format=absolute --git-common-dir"] = tGit(t) // excludeWorktreesDir writes here

	root, wtPath, err := New(f).Create(CreateOpts{Branch: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if root != "/repo" || wtPath != "/repo/.worktrees/tbx-feat" {
		t.Errorf("Create returned root=%q wtPath=%q, want /repo and /repo/.worktrees/tbx-feat", root, wtPath)
	}
	if f.runIndex("fetch origin main") < 0 {
		t.Errorf("Create must fetch the base: %v", f.runs)
	}
	if f.runIndex("worktree add --no-track -b feat /repo/.worktrees/tbx-feat origin/main") < 0 {
		t.Errorf("Create must add a --no-track worktree from origin/main: %v", f.runs)
	}
}

// tGit returns an isolated <tmp>/.git path for excludeWorktreesDir to write its
// exclude file into, so Create's best-effort exclusion touches only the temp dir.
func tGit(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".git")
}

// gitInitRepo initialises a real git repo at root with the given .gitignore body.
func gitInitRepo(t *testing.T, root, gitignore string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(gitignore), 0o644); err != nil {
		t.Fatal(err)
	}
}

// excludeWorktreesDir runs against a real repo (it resolves --git-common-dir and
// writes .git/info/exclude), so it is exercised with RealGit.
func TestExcludeWorktreesDir(t *testing.T) {
	excludePath := func(root string) string { return filepath.Join(root, ".git", "info", "exclude") }
	s := New(RealGit{})

	t.Run("records the entry once and leaves gitignore untouched", func(t *testing.T) {
		root := t.TempDir()
		gitInitRepo(t, root, "node_modules/\n") // tracked .gitignore body

		s.excludeWorktreesDir(root)
		s.excludeWorktreesDir(root) // second create must not duplicate

		got, err := os.ReadFile(excludePath(root))
		if err != nil {
			t.Fatalf("read exclude: %v", err)
		}
		if n := strings.Count(string(got), ".worktrees/"); n != 1 {
			t.Errorf(".git/info/exclude has %d .worktrees/ entries, want 1:\n%s", n, got)
		}

		// The tracked .gitignore must be untouched — the exclusion is repo-local.
		gi, err := os.ReadFile(filepath.Join(root, ".gitignore"))
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}
		if string(gi) != "node_modules/\n" {
			t.Errorf(".gitignore was modified: %q", gi)
		}
	})

	t.Run("outside a git repo warns without panicking", func(t *testing.T) {
		// Not a git repo: --git-common-dir fails; the call must be a best-effort
		// no-op, never a crash or a stray file.
		root := t.TempDir()
		s.excludeWorktreesDir(root)
		if _, err := os.Stat(excludePath(root)); !os.IsNotExist(err) {
			t.Errorf("expected no exclude file outside a git repo, stat err = %v", err)
		}
	})
}
