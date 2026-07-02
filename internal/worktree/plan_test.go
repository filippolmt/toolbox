package worktree

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestWorktreePathLandsUnderWorktreesDir(t *testing.T) {
	root := "/repo"
	cases := map[string]string{
		"fix-bug":   "/repo/.worktrees/tbx-fix-bug",
		"feature/x": "/repo/.worktrees/tbx-feature-x", // slash sanitized in the dir, not the branch
		"a/b/c":     "/repo/.worktrees/tbx-a-b-c",
	}
	for branch, want := range cases {
		if got := worktreePath(root, branch); got != want {
			t.Errorf("worktreePath(%q, %q) = %q, want %q", root, branch, got, want)
		}
	}
}

func TestIsToolboxWorktreeFilter(t *testing.T) {
	root := "/repo"
	accept := []string{"/repo/.worktrees/tbx-fix-bug", "/repo/.worktrees/tbx-feature-x"}
	reject := []string{
		"/repo/.worktrees/other",    // missing tbx- prefix
		"/repo/elsewhere/tbx-fix",   // not under .worktrees
		"/repo",                     // the main worktree
		"/other/.worktrees/tbx-fix", // different root
	}
	for _, p := range accept {
		if !isToolboxWorktree(root, p) {
			t.Errorf("isToolboxWorktree(%q, %q) = false, want true", root, p)
		}
	}
	for _, p := range reject {
		if isToolboxWorktree(root, p) {
			t.Errorf("isToolboxWorktree(%q, %q) = true, want false", root, p)
		}
	}
}

func TestBranchDeleteArgs(t *testing.T) {
	// Safe delete (-d refuses an unmerged branch); --force escalates to -D, the
	// same flag that forced the worktree removal.
	if got, want := branchDeleteArgs("fix-bug", false), []string{"branch", "-d", "fix-bug"}; !slices.Equal(got, want) {
		t.Errorf("branchDeleteArgs(safe) = %v, want %v", got, want)
	}
	if got, want := branchDeleteArgs("fix-bug", true), []string{"branch", "-D", "fix-bug"}; !slices.Equal(got, want) {
		t.Errorf("branchDeleteArgs(force) = %v, want %v", got, want)
	}
}

func TestShouldDeleteRemote(t *testing.T) {
	// Remote delete requires all three: the flag, a succeeded local delete, and
	// an existing origin counterpart. The critical safety case is the second row:
	// --delete-remote on a branch whose local delete was refused must NOT touch
	// origin (else `rm --delete-remote` on an unmerged branch destroys remote work).
	cases := []struct {
		localDeleted, remoteFlag, hasRemote, want bool
	}{
		{true, true, true, true},     // all conditions met
		{false, true, true, false},   // local refused → never touch remote
		{true, false, true, false},   // no --delete-remote
		{true, true, false, false},   // never pushed → no origin branch
		{false, false, false, false}, // nothing set
	}
	for _, c := range cases {
		if got := shouldDeleteRemote(c.localDeleted, c.remoteFlag, c.hasRemote); got != c.want {
			t.Errorf("shouldDeleteRemote(%v, %v, %v) = %v, want %v",
				c.localDeleted, c.remoteFlag, c.hasRemote, got, c.want)
		}
	}
}

func TestPruneCandidates(t *testing.T) {
	root := "/repo"
	infos := []worktreeInfo{
		{Path: "/repo", Branch: "main"},                         // main worktree → skip
		{Path: "/repo/.worktrees/tbx-merged", Branch: "merged"}, // merged into its base
		{Path: "/repo/.worktrees/tbx-open", Branch: "open"},     // not merged
		{Path: "/repo/.worktrees/other", Branch: "stray"},       // not tbx- → skip
		{Path: "/repo/.worktrees/tbx-detached", Branch: ""},     // detached → skip
		{Path: "/repo/.worktrees/tbx-nobase", Branch: "nobase"}, // base unresolvable → skip
		{Path: "/repo/.worktrees/tbx-feat", Branch: "feat"},     // merged into a CUSTOM base only
	}

	// 'feat' was branched --from develop and is merged into develop but NOT into
	// the default base — it must still be a candidate (per-base correctness).
	baseOf := map[string]string{
		"merged": "main",
		"open":   "main",
		"feat":   "develop",
		"nobase": "", // unresolvable
	}
	mergedInto := map[string]map[string]bool{
		"main":    {"merged": true},                // 'open' absent → not merged
		"develop": {"feat": true, "merged": false}, // 'feat' merged only here
	}

	got := pruneCandidates(root, infos,
		func(b string) string { return baseOf[b] },
		func(base, branch string) bool { return mergedInto[base][branch] },
	)

	want := map[string]bool{"merged": true, "feat": true}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates %v, want %d %v", len(got), got, len(want), want)
	}
	for _, w := range got {
		if !want[w.Branch] {
			t.Errorf("unexpected prune candidate %q", w.Branch)
		}
	}
}

func TestParseWorktreesPorcelain(t *testing.T) {
	porcelain := "worktree /repo\nHEAD abc\nbranch refs/heads/main\n\n" +
		"worktree /repo/.worktrees/tbx-fix\nHEAD def\nbranch refs/heads/fix\n\n" +
		"worktree /repo/detached\nHEAD 123\ndetached\n"
	infos := parseWorktrees(porcelain)
	if len(infos) != 3 {
		t.Fatalf("parseWorktrees returned %d entries, want 3: %+v", len(infos), infos)
	}
	if infos[1].Path != "/repo/.worktrees/tbx-fix" || infos[1].Branch != "fix" {
		t.Errorf("second entry = %+v, want path tbx-fix branch fix", infos[1])
	}
	if infos[2].Branch != "" {
		t.Errorf("detached entry should have empty branch, got %q", infos[2].Branch)
	}
}

func TestEnsureExcludeLine(t *testing.T) {
	const entry = ".worktrees/"
	cases := []struct {
		name        string
		body        string
		wantOut     string
		wantChanged bool
	}{
		{"empty body appends", "", ".worktrees/\n", true},
		{"missing trailing newline gets one", "node_modules/", "node_modules/\n.worktrees/\n", true},
		{"already present is a no-op", "node_modules/\n.worktrees/\n", "node_modules/\n.worktrees/\n", false},
		{"present among other lines", ".worktrees/\ndist/\n", ".worktrees/\ndist/\n", false},
		// git does not strip a leading space from an exclude pattern, so a padded
		// line does not actually exclude the dir — it must NOT count as present.
		{"whitespace-padded line does not match", "  .worktrees/  \n", "  .worktrees/  \n.worktrees/\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, changed := ensureExcludeLine(c.body, entry)
			if out != c.wantOut || changed != c.wantChanged {
				t.Errorf("ensureExcludeLine(%q) = (%q, %v), want (%q, %v)", c.body, out, changed, c.wantOut, c.wantChanged)
			}
		})
	}
}

func TestContinuePlan(t *testing.T) {
	if got, want := continuePlan(true), [][]string{{"rebase", "--continue"}, {"push", "--force-with-lease"}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("continuePlan(true) = %v, want %v", got, want)
	}
	if got, want := continuePlan(false), [][]string{{"rebase", "--continue"}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("continuePlan(false) = %v, want %v", got, want)
	}
}

// A resumed rebase (continuePlan) that stops again on a further conflict must
// reuse the same stop-and-guide handling as the initial rebase: no push, and
// the continue/abort guidance. The conflict handling keys on step[0]=="rebase",
// which `rebase --continue` also satisfies.
func TestRunSyncStepsResumeReconflict(t *testing.T) {
	steps := continuePlan(true) // rebase --continue, then push
	var calls [][]string
	run := func(args ...string) error {
		calls = append(calls, args)
		if slices.Contains(args, "rebase") {
			return errors.New("conflict")
		}
		return nil
	}

	err := runSyncSteps("/repo/.worktrees/tbx-fix", steps, run, func() bool { return true })
	if err == nil {
		t.Fatal("expected an error when the resumed rebase re-conflicts")
	}
	if !strings.Contains(err.Error(), "no push was performed") {
		t.Errorf("error %q should say no push happened", err.Error())
	}
	for _, c := range calls {
		if slices.Contains(c, "push") {
			t.Errorf("push ran after a re-conflict: %v", c)
		}
	}
}

func TestFindToolboxWorktree(t *testing.T) {
	root := "/repo"
	infos := []worktreeInfo{
		{Path: "/repo", Branch: "main"},
		{Path: "/repo/.worktrees/tbx-feat", Branch: "feat"}, // live branch
		{Path: "/repo/.worktrees/tbx-rebasing", Branch: ""}, // detached mid-rebase
		{Path: "/repo/.worktrees/other", Branch: ""},        // not tbx- → ignored
	}

	// A live branch matches by its exact name.
	if got, err := findToolboxWorktree(root, "feat", infos); err != nil || got != "/repo/.worktrees/tbx-feat" {
		t.Errorf("feat: got %q, %v; want /repo/.worktrees/tbx-feat", got, err)
	}

	// A worktree mid-rebase reports a detached HEAD (empty branch) — it must
	// still resolve by its tbx- directory so `sync <branch>` can resume it.
	if got, err := findToolboxWorktree(root, "rebasing", infos); err != nil || got != "/repo/.worktrees/tbx-rebasing" {
		t.Errorf("rebasing (detached): got %q, %v; want /repo/.worktrees/tbx-rebasing", got, err)
	}

	// A branch with no matching worktree errors.
	if _, err := findToolboxWorktree(root, "missing", infos); err == nil {
		t.Error("expected an error for a branch with no toolbox worktree")
	}
}

func TestSyncPlan(t *testing.T) {
	tests := []struct {
		name        string
		fetch, push bool
		want        [][]string
	}{
		{
			name:  "default fetch and push",
			fetch: true, push: true,
			want: [][]string{{"fetch", "origin", "main"}, {"rebase", "origin/main"}, {"push", "--force-with-lease"}},
		},
		{
			name:  "no-fetch rebases onto the remote-tracking ref without fetching",
			fetch: false, push: true,
			want: [][]string{{"rebase", "origin/main"}, {"push", "--force-with-lease"}},
		},
		{
			name:  "no-push skips the push step",
			fetch: true, push: false,
			want: [][]string{{"fetch", "origin", "main"}, {"rebase", "origin/main"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syncPlan("main", tt.fetch, tt.push)
			if !slices.EqualFunc(got, tt.want, slices.Equal) {
				t.Errorf("syncPlan = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunSyncStepsStopsOnRebaseConflict(t *testing.T) {
	steps := syncPlan("main", true, true) // fetch, rebase, push
	var calls [][]string
	run := func(args ...string) error {
		calls = append(calls, args)
		if slices.Contains(args, "rebase") {
			return errors.New("conflict")
		}
		return nil
	}

	// rebaseInProgress=true → a real conflict left a rebase mid-flight.
	err := runSyncSteps("/repo/.worktrees/tbx-fix", steps, run, func() bool { return true })
	if err == nil {
		t.Fatal("expected an error on rebase conflict")
	}
	for _, want := range []string{"rebase --continue", "rebase --abort", "no push was performed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
	for _, c := range calls {
		if slices.Contains(c, "push") {
			t.Errorf("push ran after a rebase conflict: %v", c)
		}
	}
}

// A rebase that bails before starting (dirty worktree, bad base ref) leaves no
// rebase in progress: the raw git error must surface as-is, not be rewritten
// into misleading "resolve the conflict" guidance.
func TestRunSyncStepsSurfacesNonConflictRebaseError(t *testing.T) {
	steps := syncPlan("main", true, true)
	run := func(args ...string) error {
		if slices.Contains(args, "rebase") {
			return errors.New("cannot rebase: You have unstaged changes")
		}
		return nil
	}

	err := runSyncSteps("/repo/.worktrees/tbx-fix", steps, run, func() bool { return false })
	if err == nil {
		t.Fatal("expected the raw rebase error")
	}
	if !strings.Contains(err.Error(), "unstaged changes") {
		t.Errorf("error %q should surface git's message", err.Error())
	}
	if strings.Contains(err.Error(), "rebase --continue") {
		t.Errorf("error %q must not add conflict guidance when no rebase is in progress", err.Error())
	}
}

func TestNoArgSyncBranch(t *testing.T) {
	wtPath := "/repo/.worktrees/tbx-fix"

	// A real branch passes through unchanged.
	if got, err := noArgSyncBranch(wtPath, "fix-bug"); err != nil || got != "fix-bug" {
		t.Errorf("noArgSyncBranch(_, %q) = (%q, %v), want (%q, nil)", "fix-bug", got, err, "fix-bug")
	}

	// A detached HEAD (literal "HEAD") is refused with actionable guidance rather
	// than rebasing detached commits — the guard against a no-branch sync.
	got, err := noArgSyncBranch(wtPath, "HEAD")
	if err == nil {
		t.Fatal("expected a detached-HEAD error")
	}
	if got != "" {
		t.Errorf("noArgSyncBranch returned branch %q alongside an error", got)
	}
	for _, want := range []string{wtPath, "detached HEAD"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestDedupeSeedsUnionsCleansAndPreservesOrder(t *testing.T) {
	got := DedupeSeeds(
		[]string{".env", "./.env", "openspec"}, // "./.env" collapses onto ".env"
		[]string{".env.local", "openspec"},     // duplicate "openspec" dropped
		nil,                                    // nil list tolerated
	)
	want := []string{".env", "openspec", ".env.local"}
	if !slices.Equal(got, want) {
		t.Errorf("DedupeSeeds = %v, want %v (order-preserving, cleaned, deduped)", got, want)
	}
}
