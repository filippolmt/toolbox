package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// Service owns the git + filesystem side of the worktree subsystem. All git
// I/O crosses the Git seam it holds, so every method below runs in tests with a
// fake git and no real repository.
type Service struct {
	git Git
}

// New returns a Service backed by git. Production callers pass RealGit{}; tests
// pass a fake.
func New(git Git) Service { return Service{git: git} }

// CreateOpts is the input to Create: the new branch, an optional base to branch
// from (empty = repository default), and whether to skip the remote fetch.
type CreateOpts struct {
	Branch  string
	From    string
	NoFetch bool
}

// RmOpts is the input to Rm: the branch whose worktree to remove, whether to
// force (discard local changes, escalate branch delete to -D), and whether to
// also delete the branch on origin.
type RmOpts struct {
	Branch       string
	Force        bool
	DeleteRemote bool
}

// PruneOpts is the input to Prune: dry-run (list only) and whether to also
// delete each pruned branch on origin.
type PruneOpts struct {
	DryRun       bool
	DeleteRemote bool
}

// SyncOpts is the input to Sync: an optional branch (empty = the worktree the
// command is invoked from), and whether to skip the fetch / the push.
type SyncOpts struct {
	Branch  string
	NoFetch bool
	NoPush  bool
}

// WorktreeStatus is one row of the list view: the branch, its path-scoped
// container state (running/stopped/absent), and the worktree path.
type WorktreeStatus struct {
	Branch string
	Status string
	Path   string
}

// repoRoot returns the absolute MAIN-repository root, erroring clearly when
// run outside a git repository.
//
// It derives the root from `--git-common-dir` (the parent of the shared .git),
// not `--show-toplevel`: the latter returns the *linked-worktree* path when
// invoked from inside an existing toolbox worktree, which would nest new
// worktrees under that worktree and point the .git bind at a gitdir-pointer
// file instead of the real object store. The common dir is also exactly the
// `.git` the worktree's gitdir pointer references, so root/.git resolves
// in-container regardless of the invoking cwd.
func (s Service) repoRoot() (string, error) {
	commonDir, err := s.git.Output("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	return filepath.Dir(commonDir), nil
}

// defaultBranch returns the repository default branch via origin/HEAD,
// stripped of the origin/ prefix.
func (s Service) defaultBranch() (string, error) {
	ref, err := s.git.Output("symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("cannot determine the default branch (origin/HEAD unset); pass --from <base>")
	}
	return strings.TrimPrefix(ref, "origin/"), nil
}

// configureWorktreeBranch persists per-branch facts git does not track on its
// own: the base the worktree was branched from (so `prune` tests merge against
// the right base, not just the repo default), and push.autoSetupRemote so the
// first `git push` from the worktree creates its upstream instead of erroring.
// Both best-effort — a config write failure must not abort an already-created
// worktree. push.autoSetupRemote is repo-wide (git has no per-branch knob) and
// only set when unset, so a user's explicit choice is never overridden.
func (s Service) configureWorktreeBranch(root, branch, base string) {
	if _, err := s.git.Output("-C", root, "config", "branch."+branch+".base", base); err != nil {
		// prune falls back to the default base when this is missing, so warn
		// rather than abort — but don't leave the user guessing why prune later
		// targets the wrong base.
		_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not record base for %s: %v\n", branch, err)
	}
	if _, err := s.git.Output("-C", root, "config", "--get", "push.autoSetupRemote"); err != nil {
		if _, serr := s.git.Output("-C", root, "config", "push.autoSetupRemote", "true"); serr != nil {
			// With --no-track and no autoSetupRemote, the first push needs -u.
			_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not set push.autoSetupRemote (first push may need -u): %v\n", serr)
		}
	}
}

// excludeWorktreesDir records the toolbox worktrees directory in the
// repository-local exclude file (.git/info/exclude) so it never shows up as
// untracked in the main checkout's `git status`. The exclude file is resolved
// via --git-common-dir (the shared .git, correct even when invoked from inside
// a linked worktree); the tracked .gitignore is never touched. Idempotent and
// best-effort: a failure warns but must not abort an already-created worktree.
func (s Service) excludeWorktreesDir(root string) {
	gitDir, err := s.git.Output("-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not locate .git to exclude %s: %v\n", excludeEntry, err)
		return
	}
	excludePath := filepath.Join(gitDir, "info", "exclude")
	body, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not read %s: %v\n", excludePath, err)
		return
	}
	next, changed := ensureExcludeLine(string(body), excludeEntry)
	if !changed {
		return
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not create %s: %v\n", filepath.Dir(excludePath), err)
		return
	}
	if err := os.WriteFile(excludePath, []byte(next), 0o644); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not update %s: %v\n", excludePath, err)
	}
}

// forgetWorktreeBase drops the base recorded for branch once its worktree is
// gone, so stale metadata cannot outlive the worktree and mislead a later prune
// if the branch name is reused. Best-effort: `git config --unset` exits
// non-zero when the key is already absent.
func (s Service) forgetWorktreeBase(root, branch string) {
	_, _ = s.git.Output("-C", root, "config", "--unset", "branch."+branch+".base")
}

// hasRemoteBranch reports whether branch has an origin counterpart, via the
// local remote-tracking ref (offline, no network round-trip). A create'd
// worktree branch gains refs/remotes/origin/<branch> on its first push
// (push.autoSetupRemote), so a branch never pushed has no such ref.
func (s Service) hasRemoteBranch(root, branch string) bool {
	_, err := s.git.Output("-C", root, "rev-parse", "--verify", "--quiet",
		"refs/remotes/origin/"+branch)
	return err == nil
}

// worktreeDirty reports whether wtPath has uncommitted or untracked changes, so
// prune --dry-run can predict the no-force `git worktree remove` refusal instead
// of overstating a removal (and branch delete) that will not happen. A stat/git
// failure (e.g. the directory was deleted by hand) reads as not-dirty: the real
// run would then remove the stale registration and delete the branch.
func (s Service) worktreeDirty(wtPath string) bool {
	out, err := s.git.Output("-C", wtPath, "status", "--porcelain")
	return err == nil && out != ""
}

// deleteLocalBranch best-effort deletes the local branch orphaned by a removed
// worktree, reporting whether it is now gone. warn-not-fail: the worktree
// removal has already succeeded, so a refused delete (unmerged branch without
// force) must not turn a completed cleanup into an error or abort prune's sweep.
// Git.Run streams git's own stderr (e.g. "not fully merged"), so the warning
// need not restate it.
func (s Service) deleteLocalBranch(root, branch string, force bool) bool {
	if err := s.git.Run(append([]string{"-C", root}, branchDeleteArgs(branch, force)...)...); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not delete branch %s: %v\n", branch, err)
		return false
	}
	return true
}

// remoteDeleteTimeout bounds the origin round-trip so a hung or credential-
// prompting remote cannot freeze `rm` or block prune's remaining branches.
const remoteDeleteTimeout = 60 * time.Second

// deleteRemoteBranches best-effort deletes branches on origin in a single push
// (one round-trip regardless of count, so prune scales to any number of merged
// branches). warn-not-fail. GIT_TERMINAL_PROMPT=0 makes a missing credential
// fail fast rather than block on a prompt; the timeout is the backstop. Callers
// pass only branches that passed shouldDeleteRemote. Shells out directly (not
// through the Git seam) because it needs a bounded context and a scrubbed env
// the read/write seam does not carry.
func (s Service) deleteRemoteBranches(root string, branches []string) {
	if len(branches) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteDeleteTimeout)
	defer cancel()
	args := append([]string{"-C", root, "push", "origin", "--delete"}, branches...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not delete remote branch(es) %s: %v\n",
			strings.Join(branches, ", "), err)
	}
}

// deleteBranch deletes the local branch orphaned by a removed worktree and,
// gated by shouldDeleteRemote, its origin counterpart. Single-branch path for
// rm; prune batches the remote deletes itself (deleteRemoteBranches).
func (s Service) deleteBranch(root, branch string, force, remote bool) {
	localDeleted := s.deleteLocalBranch(root, branch, force)
	if shouldDeleteRemote(localDeleted, remote, s.hasRemoteBranch(root, branch)) {
		s.deleteRemoteBranches(root, []string{branch})
	}
}

// worktreeBase returns the base branch persisted for branch at create
// (branch.<branch>.base), falling back to fallback for worktrees created before
// bases were tracked. Empty only when neither a persisted base nor a fallback
// is available.
func (s Service) worktreeBase(root, branch, fallback string) string {
	base, err := s.git.Output("-C", root, "config", "--get", "branch."+branch+".base")
	if err != nil || base == "" {
		return fallback
	}
	return base
}

// listWorktrees parses `git worktree list --porcelain` into structured entries.
func (s Service) listWorktrees() ([]worktreeInfo, error) {
	out, err := s.git.Output("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out), nil
}

// resolveToolboxWorktree finds the registered toolbox worktree for branch by
// matching the git branch field exactly, not the sanitized directory path —
// distinct branches (feature/x, feature-x) can collapse to the same tbx-
// directory, so resolving by path alone could target the wrong branch.
func (s Service) resolveToolboxWorktree(root, branch string) (string, error) {
	infos, err := s.listWorktrees()
	if err != nil {
		return "", err
	}
	return findToolboxWorktree(root, branch, infos)
}

// mergedBranches returns the set of local branches merged into origin/<base>.
func (s Service) mergedBranches(base string) (map[string]bool, error) {
	out, err := s.git.Output("branch", "--merged", "origin/"+base)
	if err != nil {
		return nil, err
	}
	merged := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		// Strip the leading current/worktree markers ("* ", "+ ") and spaces.
		name := strings.TrimSpace(strings.TrimLeft(line, " *+"))
		if name == "" || strings.HasPrefix(name, "(") { // skip detached-HEAD lines
			continue
		}
		merged[name] = true
	}
	return merged, nil
}

// rebaseInProgress reports whether a rebase is mid-flight in wtPath, by probing
// git's rebase state dirs (rebase-merge for the default merge backend,
// rebase-apply for the am backend). --path-format=absolute so the path is
// stattable regardless of cwd. Used to tell a conflict (rebase left in
// progress) from an immediate rebase bail (dirty tree, bad ref).
func (s Service) rebaseInProgress(wtPath string) bool {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		p, err := s.git.Output("-C", wtPath, "rev-parse", "--path-format=absolute", "--git-path", dir)
		if err != nil {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// containerStatus reports the path-scoped container's state for the list view.
// A nil client (no reachable daemon) reads as absent.
func containerStatus(ctx context.Context, cli client.APIClient, wtPath string) string {
	if cli == nil {
		return "absent"
	}
	res, err := cli.ContainerInspect(ctx, sessionplan.ContainerNameFor(wtPath), client.ContainerInspectOptions{})
	if err != nil {
		return "absent"
	}
	if res.Container.State != nil && res.Container.State.Running {
		return "running"
	}
	return "stopped"
}

// Create fetches the remote-aligned base, adds a --no-track worktree branched
// from it, records the base + push.autoSetupRemote, and excludes .worktrees/
// from the main checkout's status. It performs no Docker work: it returns the
// repo root and the new worktree path so cmd can run the interactive session
// launch (seed + sessionplan + container.Shell) at the Docker edge.
func (s Service) Create(opts CreateOpts) (root, wtPath string, err error) {
	root, err = s.repoRoot()
	if err != nil {
		return "", "", err
	}
	base := opts.From
	if base == "" {
		base, err = s.defaultBranch()
		if err != nil {
			return "", "", err
		}
	}
	startRef := base
	if !opts.NoFetch {
		if err = s.git.Run("fetch", "origin", base); err != nil {
			return "", "", err
		}
		startRef = "origin/" + base
	}
	wtPath = worktreePath(root, opts.Branch)
	// --no-track: branching from origin/<base> would otherwise set the new
	// branch's upstream to the base, so `git push` would target the base and
	// `git status` would read "ahead of origin/<base>". push.autoSetupRemote
	// (set below) then creates the correct per-branch upstream on first push.
	if err = s.git.Run("worktree", "add", "--no-track", "-b", opts.Branch, wtPath, startRef); err != nil {
		return "", "", err
	}
	s.configureWorktreeBranch(root, opts.Branch, base)
	s.excludeWorktreesDir(root)
	return root, wtPath, nil
}

// Open resolves the toolbox worktree for branch and confirms its directory is
// present (git keeps a worktree registered even after its directory is deleted
// by hand; launching against a missing source would have Docker silently create
// an empty dir). It returns the repo root and worktree path for the launch.
func (s Service) Open(branch string) (root, wtPath string, err error) {
	root, err = s.repoRoot()
	if err != nil {
		return "", "", err
	}
	wtPath, err = s.resolveToolboxWorktree(root, branch)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(wtPath); err != nil {
		return "", "", fmt.Errorf("worktree directory %s is missing; run 'toolbox worktree prune' or recreate the worktree", wtPath)
	}
	return root, wtPath, nil
}

// List returns the toolbox worktrees with their path-scoped container status. A
// nil cli (no reachable daemon) yields status "absent" for every entry.
func (s Service) List(ctx context.Context, cli client.APIClient) ([]WorktreeStatus, error) {
	root, err := s.repoRoot()
	if err != nil {
		return nil, err
	}
	infos, err := s.listWorktrees()
	if err != nil {
		return nil, err
	}
	var out []WorktreeStatus
	for _, w := range infos {
		if !isToolboxWorktree(root, w.Path) {
			continue
		}
		out = append(out, WorktreeStatus{
			Branch: w.Branch,
			Status: containerStatus(ctx, cli, w.Path),
			Path:   w.Path,
		})
	}
	return out, nil
}

// Rm stops a worktree's container and removes the worktree, deleting the
// orphaned branch (and its origin counterpart when opts.DeleteRemote). Ordering
// is the guard: the worktree is removed first (git refuses a dirty tree without
// --force), so a refused removal returns with the container still up rather than
// a half-torn-down worktree. cli may be nil (no reachable daemon) — the removal
// must not depend on the container.
func (s Service) Rm(ctx context.Context, cli client.APIClient, opts RmOpts) error {
	root, err := s.repoRoot()
	if err != nil {
		return err
	}
	wtPath, err := s.resolveToolboxWorktree(root, opts.Branch)
	if err != nil {
		return err
	}

	// Friendly upfront refusal for a dirty tree (git's own message is terser).
	// Best-effort: worktreeDirty reads false when git status can't run, so it is
	// not the safety net — the ordering below is. --force skips it (discards
	// changes anyway).
	if !opts.Force && s.worktreeDirty(wtPath) {
		return fmt.Errorf("worktree %s has uncommitted changes; commit them or pass --force to discard", wtPath)
	}

	// Remove before tearing anything else down. `git worktree remove` deregisters
	// a hand-deleted directory as cleanly as a present one (only a dirty present
	// tree needs --force). Ordering is the real guard: if git refuses, we return
	// with the container still up rather than leaving a half-torn-down worktree.
	gitArgs := []string{"worktree", "remove"}
	if opts.Force {
		gitArgs = append(gitArgs, "--force")
	}
	if err := s.git.Run(append(gitArgs, wtPath)...); err != nil {
		return err
	}

	// Removal succeeded: stop the now-orphaned container (path-derived, so it
	// works after the directory is gone) and delete the orphaned branch + base
	// (best-effort). --force escalates the branch delete to -D.
	if cli != nil {
		_ = container.Stop(ctx, cli, wtPath)
	}
	s.deleteBranch(root, opts.Branch, opts.Force, opts.DeleteRemote)
	s.forgetWorktreeBase(root, opts.Branch)
	return nil
}

// Prune removes every toolbox worktree whose branch is merged into its own
// recorded base (each distinct base fetched once). Progress is written to out;
// warnings go to stderr. cli may be nil — always nil for a dry run, and a down
// daemon still lets healthy worktrees be removed. DeleteRemote batches the
// origin deletes into one push at the end.
func (s Service) Prune(ctx context.Context, cli client.APIClient, out io.Writer, opts PruneOpts) error {
	root, err := s.repoRoot()
	if err != nil {
		return err
	}
	infos, err := s.listWorktrees()
	if err != nil {
		return err
	}

	// Resolve each toolbox worktree's base (persisted at create, else the repo
	// default) so a worktree branched with --from is tested against that base,
	// not the default. defaultBranch is only a fallback, so origin/HEAD being
	// unset is fatal only for worktrees that also lack a persisted base.
	defaultBase, defErr := s.defaultBranch()
	baseByBranch := map[string]string{}
	bases := map[string]bool{}
	for _, w := range infos {
		if !isToolboxWorktree(root, w.Path) || w.Branch == "" {
			continue
		}
		base := s.worktreeBase(root, w.Branch, defaultBase)
		if base == "" {
			_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: skipping %s: no base recorded and origin/HEAD is unset (%v)\n", w.Branch, defErr)
			continue
		}
		baseByBranch[w.Branch] = base
		bases[base] = true
	}

	// Fetch each distinct base once, then its merged-branch set. Per-base
	// failures are best-effort: a base deleted/renamed on the remote must not
	// abort the sweep — skip it with a warning and prune the healthy ones. A
	// skipped base leaves mergedByBase[base] nil, so its worktrees read as
	// not-merged and are preserved (the safe default).
	mergedByBase := map[string]map[string]bool{}
	for base := range bases {
		if err := s.git.Run("fetch", "origin", base); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: skipping base %s: %v\n", base, err)
			continue
		}
		m, err := s.mergedBranches(base)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: skipping base %s: %v\n", base, err)
			continue
		}
		mergedByBase[base] = m
	}

	candidates := pruneCandidates(root, infos,
		func(branch string) string { return baseByBranch[branch] },
		func(base, branch string) bool { return mergedByBase[base][branch] },
	)

	var remoteToDelete []string // branches whose origin ref to delete in one push
	for _, w := range candidates {
		if opts.DryRun {
			// A dirty worktree fails the no-force `git worktree remove` below, so
			// its branch is not deleted either — don't overstate the removal.
			if s.worktreeDirty(w.Path) {
				_, _ = fmt.Fprintf(out, "would skip %s (%s): worktree has uncommitted changes\n", w.Branch, w.Path)
				continue
			}
			msg := fmt.Sprintf("would remove %s (%s) and delete local branch %s", w.Branch, w.Path, w.Branch)
			if opts.DeleteRemote && s.hasRemoteBranch(root, w.Branch) {
				msg += " and remote origin/" + w.Branch
			}
			_, _ = fmt.Fprintln(out, msg)
			continue
		}
		_, _ = fmt.Fprintf(out, "removing %s (%s)\n", w.Branch, w.Path)
		if cli != nil {
			_ = container.Stop(ctx, cli, w.Path)
		}
		// No --force: git refuses to remove a worktree with uncommitted or
		// untracked changes, so a freshly-created (trivially "merged") branch
		// the user is still working in is preserved rather than discarded.
		if err := s.git.Run("worktree", "remove", w.Path); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: %v\n", err)
			continue
		}
		// Force -D: prune already proved the branch merged into origin/<base>,
		// the authoritative check. Safe -d would instead test merge into the
		// local HEAD (branches are --no-track, so no upstream), and refuse when
		// the local default branch lags origin — orphaning a branch that is in
		// fact merged upstream.
		localDeleted := s.deleteLocalBranch(root, w.Branch, true)
		if shouldDeleteRemote(localDeleted, opts.DeleteRemote, s.hasRemoteBranch(root, w.Branch)) {
			remoteToDelete = append(remoteToDelete, w.Branch)
		}
		s.forgetWorktreeBase(root, w.Branch)
	}
	s.deleteRemoteBranches(root, remoteToDelete) // one push for the whole sweep
	if len(candidates) == 0 {
		_, _ = fmt.Fprintln(out, "No merged toolbox worktrees to prune.")
	}
	return nil
}

// Sync rebases a worktree branch onto its recorded base and pushes with
// --force-with-lease. With opts.Branch empty it operates on the worktree the
// command is invoked from (never the primary checkout). A rebase already in
// progress is resumed (continue + push) rather than restarted. On a rebase
// conflict it stops with the rebase in progress and returns continue/abort
// guidance. No Docker work.
func (s Service) Sync(opts SyncOpts) error {
	root, err := s.repoRoot()
	if err != nil {
		return err
	}

	var branch, wtPath string
	if opts.Branch == "" {
		// No branch: operate on the worktree the command is invoked from, but
		// only a toolbox worktree — never the primary checkout. Without this
		// guard, running `sync` from the main repo on `main` would fetch, rebase
		// and force-push the shared default branch.
		if wtPath, err = s.git.Output("rev-parse", "--show-toplevel"); err != nil {
			return err
		}
		if !isToolboxWorktree(root, wtPath) {
			return fmt.Errorf("%s is not a toolbox worktree; run sync from a 'toolbox worktree' checkout or pass a branch", wtPath)
		}
	} else {
		branch = opts.Branch
		if wtPath, err = s.resolveToolboxWorktree(root, branch); err != nil {
			return err
		}
	}

	// A rebase already in progress means a previous sync stopped on a conflict
	// the user has since resolved: resume it (continue, then push) rather than
	// starting a fresh rebase. Checked before branch/base resolution — a rebase
	// leaves HEAD detached, so the no-arg branch lookup below would otherwise
	// fail with the detached-HEAD error and strand the resume.
	if s.rebaseInProgress(wtPath) {
		// Announce the resume so it is not silent: this path skips fetch/rebase
		// and does not re-resolve the base (the rebase already fixed its target),
		// so --no-fetch has no effect here — only --no-push still applies.
		_, _ = fmt.Fprintf(os.Stderr, "toolbox: resuming rebase in progress in %s\n", wtPath)
		return runSyncSteps(wtPath, continuePlan(!opts.NoPush), s.git.Run,
			func() bool { return s.rebaseInProgress(wtPath) })
	}

	// No rebase in progress: for the no-arg case, resolve the current branch now.
	if opts.Branch == "" {
		current, curErr := s.git.Output("rev-parse", "--abbrev-ref", "HEAD")
		if curErr != nil {
			return curErr
		}
		if branch, err = noArgSyncBranch(wtPath, current); err != nil {
			return err
		}
	}

	// defaultBranch is only a fallback, so origin/HEAD being unset is fatal only
	// when the branch also lacks a persisted base (mirrors prune).
	fallback, _ := s.defaultBranch()
	base := s.worktreeBase(root, branch, fallback)
	if base == "" {
		return fmt.Errorf("cannot determine the base for %q (no recorded base and origin/HEAD is unset)", branch)
	}
	// With --no-fetch the rebase targets the local origin/<base> ref directly, so
	// if that ref is absent the rebase dies with an opaque "invalid upstream";
	// surface an actionable error instead. When fetching (the default) we must NOT
	// pre-check: `git fetch origin <base>` creates the tracking ref for a base not
	// yet mirrored locally and fails clearly if the base is truly gone, so a
	// pre-check would wrongly abort a valid first sync.
	if opts.NoFetch && !s.hasRemoteBranch(root, base) {
		return fmt.Errorf("base %q is not available locally (origin/%s is missing and --no-fetch skips the fetch); drop --no-fetch or re-create the worktree with --from", base, base)
	}

	return runSyncSteps(wtPath, syncPlan(base, !opts.NoFetch, !opts.NoPush), s.git.Run,
		func() bool { return s.rebaseInProgress(wtPath) })
}
