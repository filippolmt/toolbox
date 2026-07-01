package cmd

import (
	"context"
	"errors"
	"fmt"
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
	seedLocalSettings(root, wtPath)
	applyWorktreeSession(plan, root, cfg.Shell, agent, prompt)
	return container.Shell(ctx, cli, plan)
}

// seedLocalSettings copies the main repo's .claude/settings.local.json into the
// worktree so the agent inherits the local permission allowlist. That file is
// per-repo (not per-branch) yet gitignored, so a fresh worktree checkout lacks
// it and every session would otherwise re-prompt for the same permissions. A
// copy (not a symlink/bind) is deliberate: the main repo's working tree is not
// mounted in the worktree container, so a link would dangle; the copy lives in
// the worktree checkout, which is. Best-effort and non-clobbering — a missing
// source, an already-seeded worktree, or a write error must never block the
// session (the agent still runs, just with fewer pre-approved permissions).
func seedLocalSettings(root, wtPath string) {
	src := filepath.Join(root, ".claude", "settings.local.json")
	data, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return // no local settings to seed
	}
	if err != nil {
		// Present-but-unreadable (EACCES, odd ownership, a directory): warn so a
		// missing allowlist in the worktree is diagnosable, not silent.
		fmt.Fprintf(os.Stderr, "toolbox: warning: cannot read %s to seed worktree: %v\n", src, err)
		return
	}
	dst := filepath.Join(wtPath, ".claude", "settings.local.json")
	if info, err := os.Stat(dst); err == nil && !info.IsDir() {
		return // already present — keep any worktree-local edits
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return
	}
	// Plain write, not AtomicWriteFile: the destination is guaranteed absent
	// (the Stat guard above), so there is no prior content for an atomic rename
	// to protect — and a rename would leave a non-gitignored .tmp-* orphan if
	// interrupted. 0o600 to match the repo-wide convention for config files
	// (this is an auth-adjacent permission allowlist).
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

func init() {
	worktreeCreateCmd.Flags().StringVar(&wtAgent, "agent", "", agentFlagUsage)
	worktreeCreateCmd.Flags().StringVar(&wtFrom, "from", "", "base branch to branch from (default: repository default branch)")
	worktreeCreateCmd.Flags().BoolVar(&wtNoFetch, "no-fetch", false, "branch from the local base ref without contacting the remote")
	worktreeOpenCmd.Flags().StringVar(&wtAgent, "agent", "", agentFlagUsage)
	worktreeRmCmd.Flags().BoolVar(&wtForce, "force", false, "pass --force to git worktree remove (discards local changes)")
	worktreePruneCmd.Flags().BoolVar(&wtDryRun, "dry-run", false, "list worktrees that would be removed without removing them")

	worktreeCmd.AddCommand(worktreeCreateCmd)
	worktreeCmd.AddCommand(worktreeOpenCmd)
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeRmCmd)
	worktreeCmd.AddCommand(worktreePruneCmd)
	rootCmd.AddCommand(worktreeCmd)
}
