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
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/worktree"
)

// Per-command flag state. Each subcommand owns its own struct so no flag var is
// bound to two commands — cobra parses one command per invocation, but sharing a
// package var across bindings is a latent footgun (a future persistent binding
// would leak state between subcommands).
var (
	createFlags struct {
		agent   string
		from    string
		noFetch bool
	}
	openFlags struct {
		agent string
	}
	rmFlags struct {
		force        bool
		deleteRemote bool
	}
	pruneFlags struct {
		dryRun       bool
		deleteRemote bool
	}
	syncFlags struct {
		noFetch bool
		noPush  bool
	}
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
		// cfg.Agent or, when unset, config.DefaultAgent — the one seam that
		// owns the agent fallback (shared with config show and the config UI).
		agent, _ = config.EffectiveValue(cfg, "agent")
	}
	if err := config.ValidateAgent(agent); err != nil {
		return "", err
	}
	return agent, nil
}

// openSession plans and launches a worktree container session scoped to
// wtPath, auto-starting agent with an optional initial prompt (empty = bare
// launch). Shared by create (which may pass a prompt) and open (which never
// does — re-attach only). This is the interactive Docker edge that stays in
// cmd (see the Worktree entry in CONTEXT.md): the seed gating, the sessionplan
// call, and the TTY attach in container.Shell, plus resolveImageDigest, shared
// with the `shell` command. What a worktree session *is* — the .git bind and
// the agent launch — lives behind PlanInput.Worktree.
func openSession(ctx context.Context, cli client.APIClient, root, wtPath, branch, agent, prompt string) error {
	reloadFrom, err := takeReloadHandover()
	if err != nil {
		return err
	}
	imageDigest := resolveImageDigest(ctx, cli, build.ResolveImage(cfg.Image, cfg.RegistryMirror))
	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Cfg:         cfg,
		Workspace:   wtPath,
		ImageDigest: imageDigest,
		Peer:        cfg.PeerMessaging,
		Worktree:    &sessionplan.WorktreeSession{RepoRoot: root, Agent: agent, Prompt: prompt},
		ReloadFrom:  reloadFrom,
	})
	if err != nil {
		return err
	}
	seedWorktreeFiles(root, wtPath, cfg.Worktree.Seed)
	// The re-entry form is normalised, not replayed: `create` would fail on a
	// branch that now exists and would re-send a prompt the agent already
	// completed, while `open` is idempotent and promptless by construction.
	return runSession(ctx, cli, plan, []string{"worktree", "open", branch})
}

// seedWorktreeFiles copies gitignored per-repo working state from the main
// repo (root) into a freshly created worktree (wtPath) so the agent session
// starts with the local specs, planning artifacts, permission allowlist, and
// dotenv secrets a tracked-files-only checkout would miss. extra is the user's
// worktree.seed config, unioned with worktree.DefaultSeeds.
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
	candidates := worktree.DedupeSeeds(worktree.DefaultSeeds, extra, envSeeds(root))

	seed := func(rel string) { seedEntry(filepath.Join(root, rel), filepath.Join(wtPath, rel)) }

	gated := classifySeedCandidates(root, candidates, seed)
	if len(gated) == 0 {
		return
	}
	ignored, err := gitIgnoredSubset(root, gated)
	if err != nil {
		// git itself failed (not the benign "nothing ignored" exit 1): we cannot
		// confirm what is ignored, so fall back to the one seed that is virtually
		// always gitignored and always worth carrying — the permission allowlist.
		fmt.Fprintf(os.Stderr, "toolbox: warning: git check-ignore failed (%v); seeding only the permission allowlist\n", err)
		seed(worktree.LocalSettingsRel)
		return
	}
	for _, rel := range ignored {
		seed(rel)
	}
}

// classifySeedCandidates splits the candidates in two: a directory git ignores
// wholesale (openspec/, .planning/) is handed to seed immediately, skipping an
// O(files) walk plus a git round-trip per file; everything else is returned as
// the leaf set that still needs the per-file check-ignore gate.
func classifySeedCandidates(root string, candidates []string, seed func(rel string)) []string {
	var gated []string
	for _, c := range candidates {
		src := filepath.Join(root, c)
		info, err := os.Lstat(src) // Lstat: a symlinked dir is a leaf, not a tree to walk
		switch {
		case errors.Is(err, os.ErrNotExist):
			// No such candidate in the main repo — nothing to seed.
		case err != nil:
			// Present-but-unstattable (EACCES on a parent, odd ownership): warn
			// so a missing seed in the worktree stays diagnosable, not silent.
			fmt.Fprintf(os.Stderr, "toolbox: warning: cannot stat %s to seed worktree: %v\n", src, err)
		case !info.IsDir():
			gated = append(gated, c) // a file or symlink leaf
		case gitIgnores(root, c):
			walkLeaves(root, src, seed) // whole dir ignored by one rule
		default:
			// Partially ignored — gate every file individually.
			walkLeaves(root, src, func(rel string) { gated = append(gated, rel) })
		}
	}
	return gated
}

// walkLeaves hands visit the root-relative path of every leaf under dir. Real
// directories are descended; a symlinked directory is a leaf, not a tree to
// walk. Unreadable entries are skipped rather than aborting the walk.
func walkLeaves(root, dir string, visit func(rel string)) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || (d.IsDir() && d.Type()&fs.ModeSymlink == 0) {
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil {
			visit(rel)
		}
		return nil
	})
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

// gitIgnores reports whether git ignores a single repo-relative path in root.
// Used to detect a wholly-ignored directory (openspec/, .planning/) so it can
// be seeded without enumerating + gating every file. Exit 0 = ignored; any
// other exit (1 = not ignored, >1 = git error) reports false, so a git failure
// falls through to the per-file gate, which reports the error to the caller.
// Shells out directly rather than through the worktree.Git seam: seeding is a
// filesystem-shaped git query (check-ignore with piped stdin), not the
// orchestration git the seam abstracts.
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

func runWorktreeCreate(cmd *cobra.Command, args []string) error {
	branch := args[0]
	// Only tokens after the `--` separator are the initial prompt; ArgsLenAtDash
	// is the count of args before `--` (-1 when absent), so args[dash:] is exactly
	// the post-`--` tail. worktreeCreateArgs guarantees a lone branch before it.
	prompt := ""
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		prompt = strings.Join(args[dash:], " ")
	}
	agent, err := resolveAgent(createFlags.agent)
	if err != nil {
		return &usageError{err: err}
	}

	root, wtPath, err := worktree.New(worktree.RealGit{}).Create(worktree.CreateOpts{
		Branch:  branch,
		From:    createFlags.from,
		NoFetch: createFlags.noFetch,
	})
	if err != nil {
		return err
	}

	cli, err := container.NewClient()
	if err != nil {
		return dockerClientErr(err)
	}
	defer cli.Close()
	ctx, stop := signalCtx()
	defer stop()
	// The worktree + branch now exist on disk; if the container launch fails
	// (daemon down, image pull error) the worktree is a valid artifact — point
	// the user at `open` to re-attach rather than re-`create` (which would
	// error on the existing branch/directory).
	if err := openSession(ctx, cli, root, wtPath, branch, agent, prompt); err != nil {
		return fmt.Errorf("%w\nworktree created at %s — re-attach with 'toolbox worktree open %s' once resolved", err, wtPath, branch)
	}
	return nil
}

func runWorktreeOpen(cmd *cobra.Command, args []string) error {
	branch := args[0]
	agent, err := resolveAgent(openFlags.agent)
	if err != nil {
		return &usageError{err: err}
	}

	root, wtPath, err := worktree.New(worktree.RealGit{}).Open(branch)
	if err != nil {
		return err
	}

	cli, err := container.NewClient()
	if err != nil {
		return dockerClientErr(err)
	}
	defer cli.Close()
	ctx, stop := signalCtx()
	defer stop()
	return openSession(ctx, cli, root, wtPath, branch, agent, "") // no prompt on re-attach
}

func runWorktreeList(cmd *cobra.Command, _ []string) error {
	cli, err := container.NewClient()
	if err != nil {
		return dockerClientErr(err)
	}
	defer cli.Close()
	ctx, stop := signalCtx()
	defer stop()

	rows, err := worktree.New(worktree.RealGit{}).List(ctx, cli)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, w := range rows {
		_, _ = fmt.Fprintf(out, "%-24s %-8s %s\n", w.Branch, w.Status, w.Path)
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(out, "No toolbox worktrees.")
	}
	return nil
}

func runWorktreeRm(cmd *cobra.Command, args []string) error {
	// Best-effort client: removing the worktree must not depend on the container
	// (no Docker, container already gone). A nil client skips the container stop.
	var cli client.APIClient
	if c, cerr := container.NewClient(); cerr == nil {
		cli = c
		defer cli.Close()
	}
	ctx, stop := signalCtx()
	defer stop()
	return worktree.New(worktree.RealGit{}).Rm(ctx, cli, worktree.RmOpts{
		Branch:       args[0],
		Force:        rmFlags.force,
		DeleteRemote: rmFlags.deleteRemote,
	})
}

func runWorktreePrune(cmd *cobra.Command, _ []string) error {
	// One client for the whole sweep (best-effort: a down daemon still lets us
	// remove worktrees). nil for a dry run — no container work happens.
	var cli client.APIClient
	if !pruneFlags.dryRun {
		if c, cerr := container.NewClient(); cerr == nil {
			cli = c
			defer cli.Close()
		}
	}
	ctx, stop := signalCtx()
	defer stop()
	return worktree.New(worktree.RealGit{}).Prune(ctx, cli, cmd.OutOrStdout(), worktree.PruneOpts{
		DryRun:       pruneFlags.dryRun,
		DeleteRemote: pruneFlags.deleteRemote,
	})
}

func runWorktreeSync(_ *cobra.Command, args []string) error {
	branch := ""
	if len(args) == 1 {
		branch = args[0]
	}
	return worktree.New(worktree.RealGit{}).Sync(worktree.SyncOpts{
		Branch:  branch,
		NoFetch: syncFlags.noFetch,
		NoPush:  syncFlags.noPush,
	})
}

func init() {
	worktreeCreateCmd.Flags().StringVar(&createFlags.agent, "agent", "", agentFlagUsage)
	worktreeCreateCmd.Flags().StringVar(&createFlags.from, "from", "", "base branch to branch from (default: repository default branch)")
	worktreeCreateCmd.Flags().BoolVar(&createFlags.noFetch, "no-fetch", false, "branch from the local base ref without contacting the remote")
	worktreeOpenCmd.Flags().StringVar(&openFlags.agent, "agent", "", agentFlagUsage)
	worktreeRmCmd.Flags().BoolVar(&rmFlags.force, "force", false, "pass --force to git worktree remove (discards local changes)")
	worktreeRmCmd.Flags().BoolVar(&rmFlags.deleteRemote, "delete-remote", false, "also delete the branch on origin (git push origin --delete)")
	worktreePruneCmd.Flags().BoolVar(&pruneFlags.dryRun, "dry-run", false, "list worktrees that would be removed without removing them")
	worktreePruneCmd.Flags().BoolVar(&pruneFlags.deleteRemote, "delete-remote", false, "also delete each pruned branch on origin (git push origin --delete)")
	worktreeSyncCmd.Flags().BoolVar(&syncFlags.noFetch, "no-fetch", false, "rebase onto the local base ref without contacting the remote")
	worktreeSyncCmd.Flags().BoolVar(&syncFlags.noPush, "no-push", false, "rebase without pushing (skip the force-with-lease push)")

	worktreeCmd.AddCommand(worktreeCreateCmd)
	worktreeCmd.AddCommand(worktreeOpenCmd)
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeRmCmd)
	worktreeCmd.AddCommand(worktreePruneCmd)
	worktreeCmd.AddCommand(worktreeSyncCmd)
	rootCmd.AddCommand(worktreeCmd)
}
