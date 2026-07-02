package worktree

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Toolbox-owned worktrees live under <repo-root>/.worktrees/ with a tbx-
// directory prefix; the git branch name stays clean so PRs are unaffected.
// list/prune filter on this convention so worktrees an agent creates on its
// own stay invisible to toolbox.
const (
	worktreeDirPrefix = "tbx-"
	worktreesSubdir   = ".worktrees"
)

// excludeEntry is the .git/info/exclude line that hides the toolbox worktrees
// directory from the main repository's `git status`. Trailing slash so it only
// matches the directory, mirroring a .gitignore dir rule.
const excludeEntry = worktreesSubdir + "/"

// worktreeDirName sanitizes a branch name into a directory component, keeping
// the raw branch for git. Only the directory is sanitized (slashes in
// feature/x would otherwise nest a subdir under .worktrees/).
func worktreeDirName(branch string) string {
	return worktreeDirPrefix + strings.ReplaceAll(branch, "/", "-")
}

// worktreePath is the absolute path of the toolbox worktree for branch under
// root.
func worktreePath(root, branch string) string {
	return filepath.Join(root, worktreesSubdir, worktreeDirName(branch))
}

// isToolboxWorktree reports whether path is a toolbox-owned worktree: a
// direct child of <root>/.worktrees with the tbx- directory prefix.
func isToolboxWorktree(root, path string) bool {
	return filepath.Dir(path) == filepath.Join(root, worktreesSubdir) &&
		strings.HasPrefix(filepath.Base(path), worktreeDirPrefix)
}

// ensureExcludeLine returns body with entry present exactly once. When a line
// already equals entry exactly it returns body unchanged and changed=false;
// otherwise it appends entry on its own line (adding a trailing newline to body
// first if missing) and returns changed=true. The match is exact, not
// whitespace-trimmed: git does not strip a leading space from an exclude
// pattern, so a padded " .worktrees/" line does not actually exclude the
// directory — treating it as present would leave .worktrees/ visible in status,
// the very thing this prevents. Pure so idempotency and newline handling are
// unit-tested without a repo.
func ensureExcludeLine(body, entry string) (out string, changed bool) {
	for line := range strings.SplitSeq(body, "\n") {
		if line == entry {
			return body, false
		}
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body + entry + "\n", true
}

// branchDeleteArgs returns the `git branch` argv for deleting branch: safe `-d`
// (git refuses a branch not merged into its upstream/HEAD) by default, `-D` when
// force is set. Pure so the -d/-D choice is unit-tested without a repo; the -C
// <root> prefix and execution live in deleteLocalBranch.
func branchDeleteArgs(branch string, force bool) []string {
	flag := "-d"
	if force {
		flag = "-D"
	}
	return []string{"branch", flag, branch}
}

// shouldDeleteRemote reports whether the origin branch should be deleted. Pure
// so the gating is unit-tested. Three conditions must all hold: --delete-remote
// was given; the local delete succeeded (never touch the remote for a branch
// git refused to delete locally — otherwise `rm --delete-remote` on an unmerged
// branch would destroy origin while keeping the local, the opposite of the
// safe-by-default contract); and the branch actually has an origin counterpart
// (skip a no-op push that would warn for a never-pushed branch).
func shouldDeleteRemote(localDeleted, remoteFlag, hasRemote bool) bool {
	return remoteFlag && localDeleted && hasRemote
}

type worktreeInfo struct {
	Path   string
	Branch string
}

// parseWorktrees parses the porcelain worktree listing. Each entry starts with
// a `worktree <path>` line and optionally carries a `branch refs/heads/<name>`
// line (absent for detached HEADs).
func parseWorktrees(porcelain string) []worktreeInfo {
	var infos []worktreeInfo
	var cur worktreeInfo
	flush := func() {
		if cur.Path != "" {
			infos = append(infos, cur)
		}
		cur = worktreeInfo{}
	}
	for line := range strings.SplitSeq(porcelain, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return infos
}

// findToolboxWorktree returns the path of the toolbox worktree for branch among
// infos, matched by exact branch name — or, for a worktree mid-rebase whose
// detached HEAD leaves the branch field empty, by the deterministic tbx-
// directory. The directory fallback is what lets `sync <branch>` resume a
// rebase in progress: during a rebase `git worktree list` reports the worktree
// as detached (no branch line), so a branch-only match would fail exactly when
// resume is needed. Distinct branches that sanitize to the same directory never
// coexist (git refuses the second `worktree add`), so the fallback is
// unambiguous. Pure so the dual match is unit-tested without a repo (mirrors
// pruneCandidates).
func findToolboxWorktree(root, branch string, infos []worktreeInfo) (string, error) {
	dir := worktreeDirName(branch)
	for _, w := range infos {
		if !isToolboxWorktree(root, w.Path) {
			continue
		}
		if w.Branch == branch || (w.Branch == "" && filepath.Base(w.Path) == dir) {
			return w.Path, nil
		}
	}
	return "", fmt.Errorf("no toolbox worktree for branch %q", branch)
}

// pruneCandidates selects toolbox worktrees that are merged into their own
// recorded base. Pure: git I/O is injected via baseOf (a worktree's base
// branch) and mergedInBase (whether a branch is merged into a given base), so
// the per-base decision is unit-testable without a repo.
func pruneCandidates(root string, infos []worktreeInfo, baseOf func(branch string) string, mergedInBase func(base, branch string) bool) []worktreeInfo {
	var out []worktreeInfo
	for _, w := range infos {
		if !isToolboxWorktree(root, w.Path) || w.Branch == "" {
			continue
		}
		base := baseOf(w.Branch)
		if base == "" {
			continue
		}
		if mergedInBase(base, w.Branch) {
			out = append(out, w)
		}
	}
	return out
}

// noArgSyncBranch returns the branch a no-arg sync should operate on, given the
// checkout's current ref. A detached HEAD reports the literal "HEAD": there is
// no branch to rebase or push, so refuse rather than rebasing detached commits.
// Reached only when no rebase is being resumed (a resume's detached HEAD is
// legitimate). Pure so the refusal is unit-tested without a repo.
func noArgSyncBranch(wtPath, current string) (string, error) {
	if current == "HEAD" {
		return "", fmt.Errorf("%s is on a detached HEAD; check out a branch or pass one", wtPath)
	}
	return current, nil
}

// syncPlan returns the ordered git arg-vectors for a sync: an optional
// `fetch origin <base>`, a rebase onto `origin/<base>`, and an optional
// `push --force-with-lease`. Each vector is the git subcommand only; the runner
// prepends `-C <wtPath>`. Pure so the sequence is unit-testable without a repo
// (mirrors pruneCandidates).
//
// The rebase target is always the remote-tracking ref `origin/<base>`, never a
// bare local `<base>` branch: a worktree made by `create` is branched
// `--no-track` from `origin/<base>` and has no local `<base>` branch, so
// rebasing onto `<base>` would fail with "invalid upstream". With --no-fetch
// the remote-tracking ref is used as last fetched — no network, still valid.
func syncPlan(base string, fetch, push bool) [][]string {
	var steps [][]string
	if fetch {
		steps = append(steps, []string{"fetch", "origin", base})
	}
	steps = append(steps, []string{"rebase", "origin/" + base})
	if push {
		steps = append(steps, []string{"push", "--force-with-lease"})
	}
	return steps
}

// continuePlan returns the git arg-vectors for resuming a rebase already in
// progress: `rebase --continue`, then `push --force-with-lease` unless push is
// false. There is no fetch or base — the rebase is mid-flight, so the target was
// fixed when it started. Routed through runSyncSteps like syncPlan, so a resume
// that stops again on a further conflict gets the same continue/abort guidance.
// Pure so the sequence is unit-tested without a repo (mirrors syncPlan).
func continuePlan(push bool) [][]string {
	steps := [][]string{{"rebase", "--continue"}}
	if push {
		steps = append(steps, []string{"push", "--force-with-lease"})
	}
	return steps
}

// runSyncSteps runs each syncPlan step as `run("-C", wtPath, step...)`, stopping
// at the first failure. A rebase that fails AND leaves a rebase in progress
// (rebaseInProgress) is a conflict: emit continue/abort guidance, and the push
// step is unreachable after it. Any other failure — a dirty worktree or a
// missing base ref, where git bails before starting and no rebase is in
// progress — is returned as-is so git's own stderr (already streamed by Git.Run)
// is the whole story, not a misleading "resolve the conflict" message. git I/O
// and the in-progress check are injected so both branches are testable without a
// repo (mirrors pruneCandidates).
func runSyncSteps(wtPath string, steps [][]string, run func(args ...string) error, rebaseInProgress func() bool) error {
	for _, step := range steps {
		if err := run(append([]string{"-C", wtPath}, step...)...); err != nil {
			if step[0] == "rebase" && rebaseInProgress() {
				return fmt.Errorf("rebase stopped with conflicts in %s; no push was performed\n"+
					"resolve, then 'git -C %s rebase --continue' (or 'git -C %s rebase --abort' to back out)",
					wtPath, wtPath, wtPath)
			}
			return err
		}
	}
	return nil
}
