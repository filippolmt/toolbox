package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// Toolbox-owned worktrees live under <repo-root>/.worktrees/ with a tbx-
// directory prefix; the git branch name stays clean so PRs are unaffected.
// list/prune filter on this convention so worktrees an agent creates on its
// own stay invisible to toolbox.
const (
	worktreeDirPrefix = "tbx-"
	worktreesSubdir   = ".worktrees"
)

var (
	wtAgent   string
	wtFrom    string
	wtNoFetch bool
	wtNoPush  bool
	wtForce   bool
	wtDryRun  bool
)

var agentFlagUsage = "AI agent to launch (" + strings.Join(config.SupportedAgents, "|") +
	"); overrides the agent config key"

var worktreeCmd = &cobra.Command{
	Use:     "worktree",
	Aliases: []string{"wt"},
	Short:   "Manage per-branch git worktrees, each backed by a toolbox container",
	Long: `Map one branch to one git worktree to one path-scoped toolbox container
running the configured AI agent.

Each worktree lives under <repo-root>/.worktrees/tbx-<branch>; toolbox derives
a deterministic container per absolute path, so several worktrees run as
several isolated dev environments concurrently. The agent is resolved with
precedence --agent flag > the 'agent' config key > the default "claude".`,
	Args: usageArgs(cobra.NoArgs),
}

var worktreeCreateCmd = &cobra.Command{
	Use:   "create <branch> [-- <task>...]",
	Short: "Create a worktree for a new branch and launch its agent session",
	Long: `Fetch the remote-aligned base branch, add a git worktree branched from
origin/<base>, launch a path-scoped container, and auto-start the agent.

The base is --from when given, else the repository default branch
(origin/HEAD). --no-fetch branches from the local base ref for offline work.

Anything after a '--' separator is passed to the agent as its initial task
prompt, so the worktree spins up already working (e.g.
'create feat-auth -- add an auth module'). With no '--', the agent launches
bare, exactly as before.`,
	Args: usageArgs(worktreeCreateArgs),
	RunE: runWorktreeCreate,
}

// worktreeCreateArgs requires exactly one positional argument — the branch —
// before any `--` separator. Tokens after `--` are the optional task prompt and
// are not counted. Without this, MinimumNArgs(1) would silently accept stray
// tokens typed without `--` (e.g. `create feat-auth typo`) as an agent prompt,
// contradicting the documented `-- <task>` contract and firing unintended work.
func worktreeCreateArgs(cmd *cobra.Command, args []string) error {
	n := len(args)
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		n = dash // count only args before the `--`
	}
	if n != 1 {
		return fmt.Errorf("accepts one branch argument (put the optional task prompt after '--'), received %d", n)
	}
	return nil
}

var worktreeOpenCmd = &cobra.Command{
	Use:     "open <branch>",
	Aliases: []string{"attach"},
	Short:   "Re-attach to an existing toolbox worktree (relaunches the agent)",
	Long: `Recreate the path-scoped container for an existing toolbox worktree and
relaunch the agent. Session containers are auto-removed on exit, so 'open'
brings a worktree back up after its container has gone.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runWorktreeOpen,
}

var worktreeListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List toolbox worktrees with their container status",
	Args:    usageArgs(cobra.NoArgs),
	RunE:    runWorktreeList,
}

var worktreeRmCmd = &cobra.Command{
	Use:   "rm <branch>",
	Short: "Stop a worktree's container and remove the worktree",
	Args:  usageArgs(cobra.ExactArgs(1)),
	RunE:  runWorktreeRm,
}

var worktreePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove toolbox worktrees whose branch is merged into the base",
	Long: `Remove every toolbox worktree whose branch is already merged into the
base it was branched from — the --from base recorded at create, or the
repository default (origin/HEAD) otherwise — using git alone (no GitHub/GitLab
API). Each distinct base is fetched once. --dry-run lists candidates without
removing them.

Detection is local merge ancestry, so squash-merged branches (no merge
commit) are not detected — use 'toolbox worktree rm <branch>' for those.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runWorktreePrune,
}

var worktreeSyncCmd = &cobra.Command{
	Use:     "sync [<branch>]",
	Aliases: []string{"rebase"},
	Short:   "Rebase a worktree branch onto its recorded base and push",
	Long: `Fetch a worktree branch's recorded base, rebase the branch onto
origin/<base>, and push with --force-with-lease — the "Rebase Before PR"
discipline as one command.

The base is the branch.<branch>.base recorded at create, falling back to the
repository default branch (origin/HEAD). With no <branch> it operates on the
worktree it is invoked from; given a branch it targets that toolbox worktree.

--no-fetch rebases onto the local base ref for offline use; --no-push rebases
without pushing. On a rebase conflict it stops with the rebase in progress,
never auto-resolves or pushes, and prints how to continue or abort.`,
	Args: usageArgs(cobra.MaximumNArgs(1)),
	RunE: runWorktreeSync,
}

// resolveAgent applies the precedence chain: --agent flag > cfg.Agent >
// config.DefaultAgent, validating the result so an invalid value fails before
// any container is launched.
func resolveAgent(flag string) (string, error) {
	agent := flag
	if agent == "" {
		agent = cfg.Agent
	}
	if agent == "" {
		agent = config.DefaultAgent
	}
	if err := config.ValidateAgent(agent); err != nil {
		return "", err
	}
	return agent, nil
}

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

// shellSingleQuote wraps s in single quotes for safe inclusion in a shell -c
// string: everything inside single quotes is literal, so command substitution,
// backticks and semicolons in a user-supplied prompt cannot expand or inject
// commands into the session wrapper. The only quoting concern is an embedded
// single quote, escaped the standard way by closing the quote, adding an
// escaped quote, then reopening.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// agentCommand composes the shell fragment that launches agent, optionally with
// an initial prompt. An empty prompt launches the agent bare (unchanged
// behaviour); otherwise the prompt is passed as a single positional argument —
// the convention both supported agents (claude, codex) follow. An agent needing
// different ergonomics (e.g. a --task flag) would branch on agent here.
func agentCommand(agent, prompt string) string {
	if prompt == "" {
		return agent
	}
	return agent + " " + shellSingleQuote(prompt)
}

// applyWorktreeSession mutates a planned session into a worktree session: it
// appends the main repo .git bind (so git resolves the linked worktree's
// gitdir pointer in-container) and sets ExecCmd to launch the agent in the
// user's attached session, falling back to an interactive shell when the agent
// exits. When prompt is non-empty the agent starts already working on it. Cmd
// (the container's main process) is left as the idle shell so the agent does
// not also run headless in the container's main PID. Pure so tests assert the
// mutation without Docker.
func applyWorktreeSession(plan *sessionplan.SessionPlan, root, shell, agent, prompt string) {
	gitDir := filepath.Join(root, ".git")
	plan.Binds = append(plan.Binds, mountplan.Bind{Source: gitDir, Target: gitDir, Mode: "rw"})
	plan.ExecCmd = []string{"/bin/" + shell, "-i", "-c", agentCommand(agent, prompt) + "; exec /bin/" + shell + " -i"}
}

// openSession plans, mutates, and launches a worktree container session
// scoped to wtPath, auto-starting agent with an optional initial prompt (empty
// = bare launch). Shared by create (which may pass a prompt) and open (which
// never does — re-attach only).
func openSession(ctx context.Context, cli client.APIClient, root, wtPath, agent, prompt string) error {
	imageDigest := resolveImageDigest(ctx, cli, build.ResolveImage(cfg.Image, cfg.RegistryMirror))
	plan, err := sessionplan.Plan(cfg, wtPath, nil, false, imageDigest)
	if err != nil {
		return err
	}
	seedWorktreeFiles(root, wtPath, cfg.Worktree.Seed)
	applyWorktreeSession(plan, root, cfg.Shell, agent, prompt)
	return container.Shell(ctx, cli, plan)
}

// defaultWorktreeSeeds lists the per-repo (not per-branch) working state a
// fresh `git worktree add` checkout lacks — it materialises tracked files
// only. Each entry is a repo-relative file or directory (directories seeded
// recursively). Only entries git actually ignores are copied (seedWorktreeFiles
// gates every candidate through `git check-ignore`), so a repo that tracks one
// of these paths is unaffected. `.env.*` variants are discovered by glob.
// localSettingsRel is the per-repo Claude permission allowlist: the one seed
// worth a git-independent fallback copy when `git check-ignore` itself fails.
const localSettingsRel = ".claude/settings.local.json"

var defaultWorktreeSeeds = []string{
	localSettingsRel, // per-repo Claude permission allowlist
	".env",           // dotenv secrets (+ .env.* via envSeeds)
	"openspec",       // OpenSpec working tree (specs + changes)
	".planning",      // gsd spec-driven planning artifacts
}

// seedWorktreeFiles copies gitignored per-repo working state from the main
// repo (root) into a freshly created worktree (wtPath) so the agent session
// starts with the local specs, planning artifacts, permission allowlist, and
// dotenv secrets a tracked-files-only checkout would miss. extra is the user's
// worktree.seed config, unioned with defaultWorktreeSeeds.
//
// Only paths git ignores in the main repo are copied: a tracked path already
// arrives with the checkout, and a non-ignored untracked path is deliberately
// left alone (the seeding gate is `git check-ignore`, so the built-in defaults
// self-correct in a repo that tracks one of them). Symlinks are recreated, not
// dereferenced, so a link whose target lives outside the repo is not
// materialised as a real file inside the worktree. Best-effort and
// non-clobbering throughout — a missing source, an already-seeded destination,
// or an I/O error must never block the session (the agent still runs, just
// with less inherited state).
func seedWorktreeFiles(root, wtPath string, extra []string) {
	candidates := dedupeSeeds(defaultWorktreeSeeds, extra, envSeeds(root))

	seed := func(rel string) { seedEntry(filepath.Join(root, rel), filepath.Join(wtPath, rel)) }

	// gated collects file/symlink leaves that need the per-file check-ignore
	// gate; a directory ignored wholesale (openspec/, .planning/) is seeded
	// directly, skipping an O(files) walk + git round-trip.
	var gated []string
	collect := func(dir string) {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || (d.IsDir() && d.Type()&fs.ModeSymlink == 0) {
				return nil // skip unreadable entries; descend real dirs (link dirs are leaves)
			}
			if rel, err := filepath.Rel(root, p); err == nil {
				gated = append(gated, rel)
			}
			return nil
		})
	}

	for _, c := range candidates {
		src := filepath.Join(root, c)
		info, err := os.Lstat(src) // Lstat: a symlinked dir is a leaf, not a tree to walk
		if errors.Is(err, os.ErrNotExist) {
			continue // no such candidate in the main repo — nothing to seed
		}
		if err != nil {
			// Present-but-unstattable (EACCES on a parent, odd ownership): warn
			// so a missing seed in the worktree stays diagnosable, not silent.
			fmt.Fprintf(os.Stderr, "toolbox: warning: cannot stat %s to seed worktree: %v\n", src, err)
			continue
		}
		if info.IsDir() {
			if gitIgnores(root, c) {
				// Whole directory ignored by one rule — seed the tree wholesale.
				_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
					if walkErr != nil || (d.IsDir() && d.Type()&fs.ModeSymlink == 0) {
						return nil
					}
					if rel, err := filepath.Rel(root, p); err == nil {
						seed(rel)
					}
					return nil
				})
				continue
			}
			collect(src) // partially ignored — gate every file individually
			continue
		}
		gated = append(gated, c) // a file or symlink leaf
	}

	if len(gated) == 0 {
		return
	}
	ignored, err := gitIgnoredSubset(root, gated)
	if err != nil {
		// git itself failed (not the benign "nothing ignored" exit 1): we cannot
		// confirm what is ignored, so fall back to the one seed that is virtually
		// always gitignored and always worth carrying — the permission allowlist.
		fmt.Fprintf(os.Stderr, "toolbox: warning: git check-ignore failed (%v); seeding only the permission allowlist\n", err)
		seed(localSettingsRel)
		return
	}
	for _, rel := range ignored {
		seed(rel)
	}
}

// envSeeds returns the repo-relative names of .env.* dotenv variants present
// at the repo root (e.g. .env.local, .env.production). The bare .env is a
// static default; requiring the trailing dot keeps .envrc (direnv) out.
func envSeeds(root string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, ".env.*"))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		// Only dotenv files — a `.env.d/`-style directory is not env state to
		// carry, and would otherwise be walked and seeded wholesale.
		if info, err := os.Stat(m); err != nil || info.IsDir() {
			continue
		}
		out = append(out, filepath.Base(m))
	}
	return out
}

// dedupeSeeds unions the given seed lists into one order-preserving slice,
// cleaning each entry so ".env" and "./.env" collapse to a single candidate.
func dedupeSeeds(lists ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, list := range lists {
		for _, p := range list {
			p = filepath.Clean(p)
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// gitIgnores reports whether git ignores a single repo-relative path in root.
// Used to detect a wholly-ignored directory (openspec/, .planning/) so it can
// be seeded without enumerating + gating every file. Exit 0 = ignored; any
// other exit (1 = not ignored, >1 = git error) reports false, so a git failure
// falls through to the per-file gate, which reports the error to the caller.
func gitIgnores(root, rel string) bool {
	return exec.Command("git", "-C", root, "check-ignore", "-q", "--", rel).Run() == nil
}

// gitIgnoredSubset returns the subset of repo-relative paths that git ignores
// in root, via a single `git check-ignore --stdin` call. This is the seeding
// gate: only gitignored state is carried into a worktree. Exit 1 ("nothing
// ignored") returns an empty set with no error; a real git failure returns the
// error so the caller can fall back rather than silently seed nothing.
func gitIgnoredSubset(root string, rels []string) ([]string, error) {
	// -z: NUL-delimited stdin AND stdout. Without it git C-quotes any path with
	// non-ASCII or special bytes (e.g. `.env.località`) as `"...\303..."`, which
	// no longer matches a real file and would silently drop that seed.
	cmd := exec.Command("git", "-C", root, "check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(rels, "\x00") + "\x00")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil // exit 1: none of the paths are ignored
		}
		return nil, err
	}
	var ignored []string
	for line := range strings.SplitSeq(string(out), "\x00") {
		if line != "" {
			ignored = append(ignored, line)
		}
	}
	return ignored, nil
}

// seedEntry seeds one repo path into dst unless dst already exists (preserving
// a worktree-local edit and never overwriting a file already in the checkout).
// A symlink is recreated verbatim — never dereferenced, so a link whose target
// lives outside the repo is not materialised as a real file in the worktree; a
// regular file is copied. Parent dirs are created. Best-effort: every failure
// warns and returns without blocking the caller.
func seedEntry(src, dst string) {
	if _, err := os.Lstat(dst); err == nil {
		return // already present (Lstat: a dangling link still counts) — keep it
	}
	info, err := os.Lstat(src)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "toolbox: warning: cannot stat %s to seed worktree: %v\n", src, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "toolbox: warning: cannot seed %s: %v\n", dst, err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "toolbox: warning: cannot read symlink %s: %v\n", src, err)
			return
		}
		if err := os.Symlink(target, dst); err != nil {
			fmt.Fprintf(os.Stderr, "toolbox: warning: cannot seed symlink %s: %v\n", dst, err)
		}
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "toolbox: warning: cannot read %s to seed worktree: %v\n", src, err)
		return
	}
	// 0o600, not the source mode: seeded files are per-repo dev state, some
	// auth-adjacent (the permission allowlist, .env secrets). Keep the copy
	// owner-only rather than inheriting a world-readable source mode.
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "toolbox: warning: cannot seed %s: %v\n", dst, err)
	}
}

// gitOutput runs git and returns its trimmed stdout, wrapping failures with
// the captured stderr for a useful message.
func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", gitError(args, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// runGit runs git with stdout/stderr wired through, for mutating commands
// whose progress output the user should see.
func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitError(args []string, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
	}
	return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
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
func repoRoot() (string, error) {
	commonDir, err := gitOutput("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	return filepath.Dir(commonDir), nil
}

// defaultBranch returns the repository default branch via origin/HEAD,
// stripped of the origin/ prefix.
func defaultBranch() (string, error) {
	ref, err := gitOutput("symbolic-ref", "--short", "refs/remotes/origin/HEAD")
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
func configureWorktreeBranch(root, branch, base string) {
	if _, err := gitOutput("-C", root, "config", "branch."+branch+".base", base); err != nil {
		// prune falls back to the default base when this is missing, so warn
		// rather than abort — but don't leave the user guessing why prune later
		// targets the wrong base.
		_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not record base for %s: %v\n", branch, err)
	}
	if _, err := gitOutput("-C", root, "config", "--get", "push.autoSetupRemote"); err != nil {
		if _, serr := gitOutput("-C", root, "config", "push.autoSetupRemote", "true"); serr != nil {
			// With --no-track and no autoSetupRemote, the first push needs -u.
			_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: could not set push.autoSetupRemote (first push may need -u): %v\n", serr)
		}
	}
}

// forgetWorktreeBase drops the base recorded for branch once its worktree is
// gone, so stale metadata cannot outlive the worktree and mislead a later prune
// if the branch name is reused. Best-effort: `git config --unset` exits
// non-zero when the key is already absent.
func forgetWorktreeBase(root, branch string) {
	_, _ = gitOutput("-C", root, "config", "--unset", "branch."+branch+".base")
}

// worktreeBase returns the base branch persisted for branch at create
// (branch.<branch>.base), falling back to fallback for worktrees created before
// bases were tracked. Empty only when neither a persisted base nor a fallback
// is available.
func worktreeBase(root, branch, fallback string) string {
	base, err := gitOutput("-C", root, "config", "--get", "branch."+branch+".base")
	if err != nil || base == "" {
		return fallback
	}
	return base
}

type worktreeInfo struct {
	Path   string
	Branch string
}

// listWorktrees parses `git worktree list --porcelain` into structured
// entries.
func listWorktrees() ([]worktreeInfo, error) {
	out, err := gitOutput("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out), nil
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

// resolveToolboxWorktree finds the registered toolbox worktree for branch by
// matching the git branch field exactly, not the sanitized directory path —
// distinct branches (feature/x, feature-x) can collapse to the same tbx-
// directory, so resolving by path alone could target the wrong branch.
func resolveToolboxWorktree(root, branch string) (string, error) {
	infos, err := listWorktrees()
	if err != nil {
		return "", err
	}
	for _, w := range infos {
		if w.Branch == branch && isToolboxWorktree(root, w.Path) {
			return w.Path, nil
		}
	}
	return "", fmt.Errorf("no toolbox worktree for branch %q", branch)
}

// stopWorktreeContainer best-effort stops and removes the path-scoped
// container for wtPath. Failures (no Docker, container already gone) are
// swallowed: removing the worktree must not depend on the container.
func stopWorktreeContainer(wtPath string) {
	cli, err := container.NewClient()
	if err != nil {
		return
	}
	defer cli.Close()
	ctx, stop := signalCtx()
	defer stop()
	_ = container.Stop(ctx, cli, wtPath)
}

// containerStatus reports the path-scoped container's state for the list view.
func containerStatus(ctx context.Context, cli client.APIClient, wtPath string) string {
	res, err := cli.ContainerInspect(ctx, sessionplan.ContainerNameFor(wtPath), client.ContainerInspectOptions{})
	if err != nil {
		return "absent"
	}
	if res.Container.State != nil && res.Container.State.Running {
		return "running"
	}
	return "stopped"
}

func runWorktreeCreate(cmd *cobra.Command, args []string) error {
	branch := args[0]
	// Only tokens after the `--` separator are the initial prompt; ArgsLenAtDash
	// is the count of args before `--` (-1 when absent), so args[dash:] is exactly
	// the post-`--` tail. worktreeCreateArgs guarantees a lone branch before it.
	prompt := ""
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		prompt = strings.Join(args[dash:], " ")
	}
	agent, err := resolveAgent(wtAgent)
	if err != nil {
		return &usageError{err: err}
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}

	base := wtFrom
	if base == "" {
		base, err = defaultBranch()
		if err != nil {
			return err
		}
	}

	startRef := base
	if !wtNoFetch {
		if err := runGit("fetch", "origin", base); err != nil {
			return err
		}
		startRef = "origin/" + base
	}

	wtPath := worktreePath(root, branch)
	// --no-track: branching from origin/<base> would otherwise set the new
	// branch's upstream to the base, so `git push` would target the base and
	// `git status` would read "ahead of origin/<base>". push.autoSetupRemote
	// (set below) then creates the correct per-branch upstream on first push.
	if err := runGit("worktree", "add", "--no-track", "-b", branch, wtPath, startRef); err != nil {
		return err
	}
	configureWorktreeBranch(root, branch, base)

	cli, err := container.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()
	ctx, stop := signalCtx()
	defer stop()
	// The worktree + branch now exist on disk; if the container launch fails
	// (daemon down, image pull error) the worktree is a valid artifact — point
	// the user at `open` to re-attach rather than re-`create` (which would
	// error on the existing branch/directory).
	if err := openSession(ctx, cli, root, wtPath, agent, prompt); err != nil {
		return fmt.Errorf("%w\nworktree created at %s — re-attach with 'toolbox worktree open %s' once resolved", err, wtPath, branch)
	}
	return nil
}

func runWorktreeOpen(cmd *cobra.Command, args []string) error {
	branch := args[0]
	agent, err := resolveAgent(wtAgent)
	if err != nil {
		return &usageError{err: err}
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	wtPath, err := resolveToolboxWorktree(root, branch)
	if err != nil {
		return err
	}
	// git keeps a worktree registered even after its directory is deleted by
	// hand; bind-mounting a missing source would have Docker silently create
	// an empty dir and launch the agent in a sourceless workspace. Fail clearly.
	if _, err := os.Stat(wtPath); err != nil {
		return fmt.Errorf("worktree directory %s is missing; run 'toolbox worktree prune' or recreate the worktree", wtPath)
	}

	cli, err := container.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()
	ctx, stop := signalCtx()
	defer stop()
	return openSession(ctx, cli, root, wtPath, agent, "") // no prompt on re-attach
}

func runWorktreeList(cmd *cobra.Command, _ []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	infos, err := listWorktrees()
	if err != nil {
		return err
	}

	cli, err := container.NewClient()
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer cli.Close()
	ctx, stop := signalCtx()
	defer stop()

	out := cmd.OutOrStdout()
	count := 0
	for _, w := range infos {
		if !isToolboxWorktree(root, w.Path) {
			continue
		}
		count++
		_, _ = fmt.Fprintf(out, "%-24s %-8s %s\n",
			w.Branch, containerStatus(ctx, cli, w.Path), w.Path)
	}
	if count == 0 {
		_, _ = fmt.Fprintln(out, "No toolbox worktrees.")
	}
	return nil
}

func runWorktreeRm(cmd *cobra.Command, args []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	wtPath, err := resolveToolboxWorktree(root, args[0])
	if err != nil {
		return err
	}
	stopWorktreeContainer(wtPath)

	gitArgs := []string{"worktree", "remove"}
	if wtForce {
		gitArgs = append(gitArgs, "--force")
	}
	if err := runGit(append(gitArgs, wtPath)...); err != nil {
		return err
	}
	forgetWorktreeBase(root, args[0])
	return nil
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

func runWorktreePrune(cmd *cobra.Command, _ []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	infos, err := listWorktrees()
	if err != nil {
		return err
	}

	// Resolve each toolbox worktree's base (persisted at create, else the repo
	// default) so a worktree branched with --from is tested against that base,
	// not the default. defaultBranch is only a fallback, so origin/HEAD being
	// unset is fatal only for worktrees that also lack a persisted base.
	defaultBase, defErr := defaultBranch()
	baseByBranch := map[string]string{}
	bases := map[string]bool{}
	for _, w := range infos {
		if !isToolboxWorktree(root, w.Path) || w.Branch == "" {
			continue
		}
		base := worktreeBase(root, w.Branch, defaultBase)
		if base == "" {
			_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: skipping %s: no base recorded and origin/HEAD is unset (%v)\n", w.Branch, defErr)
			continue
		}
		baseByBranch[w.Branch] = base
		bases[base] = true
	}

	// Fetch each distinct base once, then its merged-branch set. Per-base
	// failures are best-effort: a base that was deleted/renamed on the remote
	// (a worktree's --from base merged and gone) must not abort the whole
	// sweep — skip that base with a warning and still prune the healthy ones.
	// A skipped base leaves mergedByBase[base] nil, so its worktrees read as
	// not-merged and are preserved (the safe default).
	mergedByBase := map[string]map[string]bool{}
	for base := range bases {
		if err := runGit("fetch", "origin", base); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: skipping base %s: %v\n", base, err)
			continue
		}
		m, err := mergedBranches(base)
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

	// One client for the whole sweep (best-effort: a down daemon still lets us
	// remove worktrees). Reused across candidates instead of reconnecting each.
	var cli client.APIClient
	if wtDryRun {
		cli = nil
	} else if c, cerr := container.NewClient(); cerr == nil {
		cli = c
		defer cli.Close()
	}
	ctx, stop := signalCtx()
	defer stop()

	out := cmd.OutOrStdout()
	for _, w := range candidates {
		if wtDryRun {
			_, _ = fmt.Fprintf(out, "would remove %s (%s)\n", w.Branch, w.Path)
			continue
		}
		_, _ = fmt.Fprintf(out, "removing %s (%s)\n", w.Branch, w.Path)
		if cli != nil {
			_ = container.Stop(ctx, cli, w.Path)
		}
		// No --force: git refuses to remove a worktree with uncommitted or
		// untracked changes, so a freshly-created (trivially "merged") branch
		// the user is still working in is preserved rather than discarded.
		if err := runGit("worktree", "remove", w.Path); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "toolbox: warning: %v\n", err)
			continue
		}
		forgetWorktreeBase(root, w.Branch)
	}
	if len(candidates) == 0 {
		_, _ = fmt.Fprintln(out, "No merged toolbox worktrees to prune.")
	}
	return nil
}

// mergedBranches returns the set of local branches merged into origin/<base>.
func mergedBranches(base string) (map[string]bool, error) {
	out, err := gitOutput("branch", "--merged", "origin/"+base)
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

// runSyncSteps runs each syncPlan step as `run("-C", wtPath, step...)`, stopping
// at the first failure. A rebase that fails AND leaves a rebase in progress
// (rebaseInProgress) is a conflict: emit continue/abort guidance, and the push
// step is unreachable after it. Any other failure — a dirty worktree or a
// missing base ref, where git bails before starting and no rebase is in
// progress — is returned as-is so git's own stderr (already streamed by runGit)
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

// rebaseInProgress reports whether a rebase is mid-flight in wtPath, by probing
// git's rebase state dirs (rebase-merge for the default merge backend,
// rebase-apply for the am backend). --path-format=absolute so the path is
// stattable regardless of cwd. Used to tell a conflict (rebase left in
// progress) from an immediate rebase bail (dirty tree, bad ref).
func rebaseInProgress(wtPath string) bool {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		p, err := gitOutput("-C", wtPath, "rev-parse", "--path-format=absolute", "--git-path", dir)
		if err != nil {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func runWorktreeSync(cmd *cobra.Command, args []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	var branch, wtPath string
	if len(args) == 0 {
		// No branch: operate on the worktree the command is invoked from, but
		// only a toolbox worktree — never the primary checkout. Without this
		// guard, running `sync` from the main repo on `main` would fetch, rebase
		// and force-push the shared default branch. resolveToolboxWorktree
		// enforces the same on the branch-arg path.
		if wtPath, err = gitOutput("rev-parse", "--show-toplevel"); err != nil {
			return err
		}
		if !isToolboxWorktree(root, wtPath) {
			return fmt.Errorf("%s is not a toolbox worktree; run sync from a 'toolbox worktree' checkout or pass a branch", wtPath)
		}
		if branch, err = gitOutput("rev-parse", "--abbrev-ref", "HEAD"); err != nil {
			return err
		}
		// A detached HEAD reports the literal "HEAD"; there is no branch to sync
		// or push, so fail clearly rather than rebasing detached commits.
		if branch == "HEAD" {
			return fmt.Errorf("%s is on a detached HEAD; check out a branch or pass one", wtPath)
		}
	} else {
		branch = args[0]
		if wtPath, err = resolveToolboxWorktree(root, branch); err != nil {
			return err
		}
	}

	// defaultBranch is only a fallback, so origin/HEAD being unset is fatal only
	// when the branch also lacks a persisted base (mirrors prune).
	fallback, _ := defaultBranch()
	base := worktreeBase(root, branch, fallback)
	if base == "" {
		return fmt.Errorf("cannot determine the base for %q (no recorded base and origin/HEAD is unset)", branch)
	}

	return runSyncSteps(wtPath, syncPlan(base, !wtNoFetch, !wtNoPush), runGit,
		func() bool { return rebaseInProgress(wtPath) })
}

func init() {
	worktreeCreateCmd.Flags().StringVar(&wtAgent, "agent", "", agentFlagUsage)
	worktreeCreateCmd.Flags().StringVar(&wtFrom, "from", "", "base branch to branch from (default: repository default branch)")
	worktreeCreateCmd.Flags().BoolVar(&wtNoFetch, "no-fetch", false, "branch from the local base ref without contacting the remote")
	worktreeOpenCmd.Flags().StringVar(&wtAgent, "agent", "", agentFlagUsage)
	worktreeRmCmd.Flags().BoolVar(&wtForce, "force", false, "pass --force to git worktree remove (discards local changes)")
	worktreePruneCmd.Flags().BoolVar(&wtDryRun, "dry-run", false, "list worktrees that would be removed without removing them")
	worktreeSyncCmd.Flags().BoolVar(&wtNoFetch, "no-fetch", false, "rebase onto the local base ref without contacting the remote")
	worktreeSyncCmd.Flags().BoolVar(&wtNoPush, "no-push", false, "rebase without pushing (skip the force-with-lease push)")

	worktreeCmd.AddCommand(worktreeCreateCmd)
	worktreeCmd.AddCommand(worktreeOpenCmd)
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeRmCmd)
	worktreeCmd.AddCommand(worktreePruneCmd)
	worktreeCmd.AddCommand(worktreeSyncCmd)
	rootCmd.AddCommand(worktreeCmd)
}
