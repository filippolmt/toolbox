package worktree

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// fakeGit scripts the Git seam: Output returns the value keyed by the
// space-joined args (empty string + nil error by default), Run returns the
// keyed error (nil by default). Both record their calls so tests can assert the
// git the orchestration issued and, crucially, its order.
// calls is the one ordered log across kinds — Output, Run and (via fakeDocker)
// the container stop — because some orderings the orchestration owes span them:
// the container must be stopped before its worktree directory is removed, and
// the base is forgotten (a read-shaped `config --unset`) only after the
// mutating `branch -D`.
type fakeGit struct {
	outputs map[string]string
	outErrs map[string]error
	runErrs map[string]error
	gets    [][]string
	runs    [][]string
	calls   [][]string
	pushes  [][]string
	pushErr error
}

func newFakeGit() *fakeGit {
	return &fakeGit{outputs: map[string]string{}, outErrs: map[string]error{}, runErrs: map[string]error{}}
}

func (f *fakeGit) Output(args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.gets = append(f.gets, append([]string(nil), args...))
	f.record(args...)
	if err := f.outErrs[key]; err != nil {
		return "", err
	}
	return f.outputs[key], nil
}

func (f *fakeGit) Run(args ...string) error {
	key := strings.Join(args, " ")
	f.runs = append(f.runs, append([]string(nil), args...))
	f.record(args...)
	return f.runErrs[key]
}

// PushDelete records one entry per call as {root, branches...}, so a test can
// assert the repo targeted, the exact batch, and how many pushes it took, then
// returns pushErr. It refuses a cancelled context as the real one does —
// RealGit runs exec.CommandContext, which fails instantly on a dead context —
// so a test can tell which context the orchestration hands the seam.
func (f *fakeGit) PushDelete(ctx context.Context, root string, branches []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.pushes = append(f.pushes, append([]string{root}, branches...))
	return f.pushErr
}

func (f *fakeGit) record(args ...string) {
	f.calls = append(f.calls, append([]string(nil), args...))
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

// callIndex is runIndex over the cross-kind log.
func (f *fakeGit) callIndex(key string) int {
	for i, c := range f.calls {
		if strings.Join(c, " ") == key {
			return i
		}
	}
	return -1
}

// runCount reports how many recorded Runs joined-equal key, for "exactly once"
// assertions (one fetch per distinct base, not per branch).
func (f *fakeGit) runCount(key string) int {
	n := 0
	for _, r := range f.runs {
		if strings.Join(r, " ") == key {
			n++
		}
	}
	return n
}

func (f *fakeGit) ranAny(substr string) bool {
	for _, r := range f.runs {
		if strings.Contains(strings.Join(r, " "), substr) {
			return true
		}
	}
	return false
}

// fakeDocker is the slice of client.APIClient container.Stop reaches (stop then
// force-remove), recording the stop into the shared log so prune's ordering
// against the git commands is observable. Unmocked methods fall through to the
// embedded nil interface and would panic, surfacing any unexpected Docker call.
type fakeDocker struct {
	client.APIClient
	log *fakeGit
}

func (d fakeDocker) ContainerStop(_ context.Context, name string, _ client.ContainerStopOptions) (client.ContainerStopResult, error) {
	d.log.record("docker", "stop", name)
	return client.ContainerStopResult{}, nil
}

func (d fakeDocker) ContainerRemove(_ context.Context, name string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	d.log.record("docker", "remove", name)
	return client.ContainerRemoveResult{}, nil
}

const (
	commonDirKey  = "rev-parse --path-format=absolute --git-common-dir"
	listKey       = "worktree list --porcelain"
	originHeadKey = "symbolic-ref --short refs/remotes/origin/HEAD"
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

// Rm --delete-remote deletes the branch on origin through the Git seam. The
// single-branch counterpart of prune's batch: rm has one branch, so exactly one
// push carrying exactly it. (The gating itself is pure and already covered by
// TestShouldDeleteRemote.)
func TestRmDeletesRemoteBranch(t *testing.T) {
	f := newFakeGit()
	f.outputs[commonDirKey] = "/repo/.git"
	f.outputs[listKey] = "worktree /repo/.worktrees/tbx-feat\nbranch refs/heads/feat\n"
	f.outputs["-C /repo/.worktrees/tbx-feat status --porcelain"] = "" // clean

	err := New(f).Rm(context.Background(), nil, RmOpts{Branch: "feat", DeleteRemote: true})
	if err != nil {
		t.Fatalf("Rm: %v", err)
	}

	want := []string{"/repo", "feat"}
	if len(f.pushes) != 1 || !slices.Equal(f.pushes[0], want) {
		t.Errorf("pushes = %v, want exactly one %v", f.pushes, want)
	}
}

// A remote that refuses the delete warns, it does not fail: the worktree and
// the local branch are already gone, so returning an error would report a
// cleanup that did happen as one that did not.
func TestRmSurvivesARefusedRemoteDelete(t *testing.T) {
	f := newFakeGit()
	f.outputs[commonDirKey] = "/repo/.git"
	f.outputs[listKey] = "worktree /repo/.worktrees/tbx-feat\nbranch refs/heads/feat\n"
	f.outputs["-C /repo/.worktrees/tbx-feat status --porcelain"] = ""
	f.pushErr = errors.New("remote: permission denied")

	if err := New(f).Rm(context.Background(), nil, RmOpts{Branch: "feat", DeleteRemote: true}); err != nil {
		t.Errorf("Rm must not fail on a refused remote delete, got: %v", err)
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

// Open resolves the branch's worktree and — the reason it exists over a bare
// path lookup — refuses one whose directory is gone: git keeps a worktree
// registered after its directory is deleted by hand, and launching against a
// missing source would have Docker silently create an empty dir.
func TestOpen(t *testing.T) {
	// fakeGit for a repo whose only toolbox worktree is tbx-fix under root.
	setup := func(root string) *fakeGit {
		f := newFakeGit()
		f.outputs[commonDirKey] = filepath.Join(root, ".git")
		f.outputs[listKey] = "worktree " + root + "\nbranch refs/heads/main\n\n" +
			"worktree " + filepath.Join(root, worktreesSubdir, "tbx-fix") + "\nbranch refs/heads/fix\n"
		return f
	}

	t.Run("present worktree resolves to root and path", func(t *testing.T) {
		root := t.TempDir()
		want := filepath.Join(root, worktreesSubdir, "tbx-fix")
		if err := os.MkdirAll(want, 0o755); err != nil {
			t.Fatal(err)
		}

		gotRoot, gotPath, err := New(setup(root)).Open("fix")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if gotRoot != root || gotPath != want {
			t.Errorf("Open = (%q, %q), want (%q, %q)", gotRoot, gotPath, root, want)
		}
	})

	t.Run("registered worktree with a missing directory is refused", func(t *testing.T) {
		root := t.TempDir() // .worktrees/tbx-fix deliberately not created

		gotRoot, gotPath, err := New(setup(root)).Open("fix")
		if err == nil {
			t.Fatal("Open must refuse a worktree whose directory is missing")
		}
		if gotRoot != "" || gotPath != "" {
			t.Errorf("Open = (%q, %q), want empty paths alongside the error", gotRoot, gotPath)
		}
	})
}

// Prune is the destructive sweep: for every toolbox worktree whose branch is
// merged into its own recorded base it stops the container, removes the
// worktree, deletes the branch and forgets the base — while a base it cannot
// resolve, a worktree git refuses to remove, and a dry run must each leave the
// user's work exactly where it is.
func TestPrune(t *testing.T) {
	t.Run("each candidate is stopped, then removed, then branch-deleted, then forgotten", func(t *testing.T) {
		f := newFakeGit()
		f.outputs[commonDirKey] = "/repo/.git"
		f.outputs[listKey] = "worktree /repo\nbranch refs/heads/main\n\n" +
			"worktree /repo/.worktrees/tbx-fix\nbranch refs/heads/fix\n"
		f.outputs[originHeadKey] = "origin/main"
		f.outputs["branch --merged origin/main"] = "  fix\n* main\n"

		var out bytes.Buffer
		if err := New(f).Prune(context.Background(), fakeDocker{log: f}, &out, PruneOpts{}); err != nil {
			t.Fatalf("Prune: %v", err)
		}

		// The container is path-scoped: the one stopped must be the worktree's own.
		stop := f.callIndex("docker stop " + sessionplan.ContainerNameFor("/repo/.worktrees/tbx-fix", ""))
		remove := f.callIndex("worktree remove /repo/.worktrees/tbx-fix")
		del := f.callIndex("-C /repo branch -D fix")
		forget := f.callIndex("-C /repo config --unset branch.fix.base")
		for name, idx := range map[string]int{"docker stop": stop, "worktree remove": remove, "branch -D": del, "config --unset": forget} {
			if idx < 0 {
				t.Fatalf("%s was never issued: %v", name, f.calls)
			}
		}
		// No --force on the removal: git must still refuse a worktree the user
		// has uncommitted work in, even though prune proved the branch merged.
		if f.ranAny("worktree remove --force") {
			t.Errorf("worktree remove must not force: %v", f.runs)
		}
		if stop >= remove || remove >= del || del >= forget {
			t.Errorf("expected order stop<remove<delete<forget, got %d<%d<%d<%d: %v", stop, remove, del, forget, f.calls)
		}
	})

	t.Run("a base is fetched once however many worktrees share it", func(t *testing.T) {
		f := newFakeGit()
		f.outputs[commonDirKey] = "/repo/.git"
		f.outputs[listKey] = "worktree /repo/.worktrees/tbx-one\nbranch refs/heads/one\n\n" +
			"worktree /repo/.worktrees/tbx-two\nbranch refs/heads/two\n\n" +
			"worktree /repo/.worktrees/tbx-rel\nbranch refs/heads/rel\n"
		f.outputs[originHeadKey] = "origin/main"
		f.outputs["-C /repo config --get branch.rel.base"] = "release" // branched with --from
		f.outputs["branch --merged origin/main"] = "  one\n  two\n"
		f.outputs["branch --merged origin/release"] = "  rel\n"

		var out bytes.Buffer
		if err := New(f).Prune(context.Background(), nil, &out, PruneOpts{}); err != nil {
			t.Fatalf("Prune: %v", err)
		}

		if got := f.runCount("fetch origin main"); got != 1 {
			t.Errorf("fetch origin main ran %d times, want 1 (once per distinct base, not per branch): %v", got, f.runs)
		}
		if got := f.runCount("fetch origin release"); got != 1 {
			t.Errorf("fetch origin release ran %d times, want 1: %v", got, f.runs)
		}
	})

	t.Run("a base whose fetch fails leaves its worktrees alone", func(t *testing.T) {
		f := newFakeGit()
		f.outputs[commonDirKey] = "/repo/.git"
		f.outputs[listKey] = "worktree /repo/.worktrees/tbx-fix\nbranch refs/heads/fix\n"
		f.outputs[originHeadKey] = "origin/main"
		f.outputs["branch --merged origin/main"] = "  fix\n" // merged, so only the skip protects it
		f.runErrs["fetch origin main"] = errors.New("couldn't find remote ref main")

		var out bytes.Buffer
		if err := New(f).Prune(context.Background(), nil, &out, PruneOpts{}); err != nil {
			t.Fatalf("Prune must not abort on an unresolvable base: %v", err)
		}

		// A skipped base reads as not-merged, the safe default.
		if f.ranAny("worktree remove") {
			t.Errorf("a worktree under a skipped base must survive: %v", f.runs)
		}
		if f.ranAny("branch -D") {
			t.Errorf("no branch may be deleted under a skipped base: %v", f.runs)
		}
	})

	t.Run("a refused worktree removal leaves the branch alone and the sweep running", func(t *testing.T) {
		f := newFakeGit()
		f.outputs[commonDirKey] = "/repo/.git"
		f.outputs[listKey] = "worktree /repo/.worktrees/tbx-stuck\nbranch refs/heads/stuck\n\n" +
			"worktree /repo/.worktrees/tbx-next\nbranch refs/heads/next\n"
		f.outputs[originHeadKey] = "origin/main"
		f.outputs["branch --merged origin/main"] = "  stuck\n  next\n"
		f.runErrs["worktree remove /repo/.worktrees/tbx-stuck"] = errors.New("contains modified or untracked files")

		var out bytes.Buffer
		if err := New(f).Prune(context.Background(), nil, &out, PruneOpts{}); err != nil {
			t.Fatalf("Prune: %v", err)
		}

		// The branch is the worktree's only remaining handle — orphaning it
		// would strand the work the failed removal preserved.
		if f.runIndex("-C /repo branch -D stuck") >= 0 {
			t.Errorf("branch of a worktree that survived removal must not be deleted: %v", f.runs)
		}
		if f.runIndex("-C /repo branch -D next") < 0 {
			t.Errorf("a failed removal must not abort the sweep: %v", f.runs)
		}
	})

	t.Run("dry run mutates nothing and predicts the dirty-worktree skip", func(t *testing.T) {
		f := newFakeGit()
		f.outputs[commonDirKey] = "/repo/.git"
		f.outputs[listKey] = "worktree /repo/.worktrees/tbx-clean\nbranch refs/heads/clean\n\n" +
			"worktree /repo/.worktrees/tbx-wip\nbranch refs/heads/wip\n"
		f.outputs[originHeadKey] = "origin/main"
		f.outputs["branch --merged origin/main"] = "  clean\n  wip\n"
		f.outputs["-C /repo/.worktrees/tbx-wip status --porcelain"] = " M main.go"

		var out bytes.Buffer
		if err := New(f).Prune(context.Background(), nil, &out, PruneOpts{DryRun: true}); err != nil {
			t.Fatalf("Prune: %v", err)
		}

		if f.ranAny("worktree remove") || f.ranAny("branch -D") {
			t.Errorf("a dry run must issue no destructive git: %v", f.runs)
		}
		// A dirty worktree fails the no-force removal, so promising to remove it
		// (and delete its branch) would overstate what the real run would do.
		if !strings.Contains(out.String(), "would remove clean (/repo/.worktrees/tbx-clean)") {
			t.Errorf("dry run must announce the removal it would do, got:\n%s", out.String())
		}
		if !strings.Contains(out.String(), "would skip wip") {
			t.Errorf("dry run must announce the dirty-worktree skip, got:\n%s", out.String())
		}
	})

	t.Run("remote deletes go out as one push for the whole sweep", func(t *testing.T) {
		f := newFakeGit()
		f.outputs[commonDirKey] = "/repo/.git"
		f.outputs[listKey] = "worktree /repo/.worktrees/tbx-one\nbranch refs/heads/one\n\n" +
			"worktree /repo/.worktrees/tbx-two\nbranch refs/heads/two\n"
		f.outputs[originHeadKey] = "origin/main"
		f.outputs["branch --merged origin/main"] = "  one\n  two\n"
		// hasRemoteBranch reads a local remote-tracking ref; the fake resolves
		// any rev-parse, so both branches have an origin counterpart.

		var out bytes.Buffer
		if err := New(f).Prune(context.Background(), nil, &out, PruneOpts{DeleteRemote: true}); err != nil {
			t.Fatalf("Prune: %v", err)
		}

		// One round-trip regardless of count: prune must scale to a sweep of
		// any size, so the origin deletes are batched, not one push per branch.
		want := []string{"/repo", "one", "two"}
		if len(f.pushes) != 1 || !slices.Equal(f.pushes[0], want) {
			t.Errorf("pushes = %v, want exactly one %v", f.pushes, want)
		}
	})

	// Ctrl+C cancels the command's context without exiting the process
	// (signal.NotifyContext), and the sweep's loop does not check it — so a
	// cancelled prune still removes worktrees and deletes local branches. The
	// remote delete must therefore be equally uncancellable: it is the step
	// whose omission cannot be retried, because prune enumerates candidates from
	// the very worktrees and branches it just deleted. Skipping it strands the
	// origin refs with no local handle left to find them by.
	t.Run("a cancelled sweep still deletes the origin refs it orphaned locally", func(t *testing.T) {
		f := newFakeGit()
		f.outputs[commonDirKey] = "/repo/.git"
		f.outputs[listKey] = "worktree /repo/.worktrees/tbx-one\nbranch refs/heads/one\n"
		f.outputs[originHeadKey] = "origin/main"
		f.outputs["branch --merged origin/main"] = "  one\n"

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var out bytes.Buffer
		if err := New(f).Prune(ctx, nil, &out, PruneOpts{DeleteRemote: true}); err != nil {
			t.Fatalf("Prune: %v", err)
		}

		want := []string{"/repo", "one"}
		if len(f.pushes) != 1 || !slices.Equal(f.pushes[0], want) {
			t.Errorf("pushes = %v, want exactly one %v even under a cancelled context", f.pushes, want)
		}
	})

	t.Run("nothing merged says so", func(t *testing.T) {
		f := newFakeGit()
		f.outputs[commonDirKey] = "/repo/.git"
		f.outputs[listKey] = "worktree /repo/.worktrees/tbx-fix\nbranch refs/heads/fix\n"
		f.outputs[originHeadKey] = "origin/main"
		f.outputs["branch --merged origin/main"] = "* main\n"

		var out bytes.Buffer
		if err := New(f).Prune(context.Background(), nil, &out, PruneOpts{}); err != nil {
			t.Fatalf("Prune: %v", err)
		}

		if out.String() != "No merged toolbox worktrees to prune.\n" {
			t.Errorf("out = %q, want the no-candidates line", out.String())
		}
	})
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
