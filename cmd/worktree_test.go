package cmd

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
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

func TestShellSingleQuote(t *testing.T) {
	// $(...), backticks and ; must survive as literal text, never expanded or
	// treated as a command separator by the session's `-c` wrapper.
	cases := []struct{ in, want string }{
		{"add auth", `'add auth'`},
		{"$(rm -rf /)", `'$(rm -rf /)'`},
		{"`whoami`", "'`whoami`'"},
		{"a; b", `'a; b'`},
		{"it's a trap", `'it'\''s a trap'`},
		{"'leading", `''\''leading'`},
		{"", `''`},
	}
	for _, c := range cases {
		if got := shellSingleQuote(c.in); got != c.want {
			t.Errorf("shellSingleQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAgentCommand(t *testing.T) {
	cases := []struct {
		agent, prompt, want string
	}{
		{"claude", "add auth", `claude 'add auth'`},
		{"codex", "fix pagination", `codex 'fix pagination'`},
		{"claude", "", "claude"},
		{"codex", "", "codex"},
		{"claude", "$(rm -rf /)", `claude '$(rm -rf /)'`},
	}
	for _, c := range cases {
		if got := agentCommand(c.agent, c.prompt); got != c.want {
			t.Errorf("agentCommand(%q, %q) = %q, want %q", c.agent, c.prompt, got, c.want)
		}
	}
}

func TestWorktreeCreateArgs(t *testing.T) {
	// ArgsLenAtDash is only populated by cobra's own arg parsing, so exercise
	// the validator through a real command Execute rather than calling it bare.
	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"branch only", []string{"feat"}, false},
		{"branch then prompt after dash", []string{"feat", "--", "add", "auth"}, false},
		{"branch then empty dash", []string{"feat", "--"}, false},
		{"stray token without dash rejected", []string{"feat", "typo"}, true},
		{"no args rejected", []string{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := &cobra.Command{
				Use:  "create",
				Args: worktreeCreateArgs,
				RunE: func(*cobra.Command, []string) error { return nil },
			}
			cmd.SetArgs(c.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); (err != nil) != c.wantErr {
				t.Errorf("args %v: err = %v, wantErr = %v", c.args, err, c.wantErr)
			}
		})
	}
}

func TestApplyWorktreeSessionMutatesPlan(t *testing.T) {
	assertGitBind := func(t *testing.T, plan *sessionplan.SessionPlan) {
		t.Helper()
		want := mountplan.Bind{Source: "/repo/.git", Target: "/repo/.git", Mode: "rw"}
		if !slices.Contains(plan.Binds, want) {
			t.Errorf("plan.Binds missing .git bind %+v, got %+v", want, plan.Binds)
		}
	}

	t.Run("bare launch", func(t *testing.T) {
		plan := &sessionplan.SessionPlan{Cmd: []string{"/bin/zsh", "-i"}}
		applyWorktreeSession(plan, "/repo", "zsh", "codex", "")
		assertGitBind(t, plan)

		// The agent launches in the attached exec session (ExecCmd), not the
		// container's main process (Cmd) — otherwise the agent runs twice.
		joined := strings.Join(plan.ExecCmd, " ")
		if !strings.Contains(joined, "codex") {
			t.Errorf("plan.ExecCmd should invoke the agent, got %q", joined)
		}
		if !strings.Contains(joined, "/bin/zsh") {
			t.Errorf("plan.ExecCmd should use the configured shell, got %q", joined)
		}
		if !strings.Contains(joined, "exec /bin/zsh -i") {
			t.Errorf("plan.ExecCmd should keep the interactive-shell fallback, got %q", joined)
		}
		if strings.Join(plan.Cmd, " ") != "/bin/zsh -i" {
			t.Errorf("plan.Cmd (container main process) must stay the idle shell, got %q", plan.Cmd)
		}
	})

	t.Run("with initial prompt", func(t *testing.T) {
		plan := &sessionplan.SessionPlan{Cmd: []string{"/bin/zsh", "-i"}}
		applyWorktreeSession(plan, "/repo", "zsh", "claude", "add auth")
		assertGitBind(t, plan)

		joined := strings.Join(plan.ExecCmd, " ")
		if !strings.Contains(joined, `claude 'add auth'`) {
			t.Errorf("plan.ExecCmd should launch the agent with the quoted prompt, got %q", joined)
		}
		if !strings.Contains(joined, "exec /bin/zsh -i") {
			t.Errorf("plan.ExecCmd should keep the interactive-shell fallback, got %q", joined)
		}
		if strings.Join(plan.Cmd, " ") != "/bin/zsh -i" {
			t.Errorf("plan.Cmd (container main process) must stay the idle shell, got %q", plan.Cmd)
		}
	})
}

// gitInitRepo initialises a git repo at root with the given .gitignore body so
// seedWorktreeFiles' `git check-ignore` gate has real rules to evaluate.
func gitInitRepo(t *testing.T, root, gitignore string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	writeFile(t, filepath.Join(root, ".gitignore"), gitignore)
}

func mustExist(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected seeded file %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("seeded content = %q, want %q", got, want)
	}
}

func mustAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s absent, stat err = %v", path, err)
	}
}

func TestSeedWorktreeFiles(t *testing.T) {
	const body = `{"permissions":{"allow":["Bash(go test:*)"]}}`

	// gitignored file + recursive dir + .env variants are all seeded
	t.Run("seeds gitignored state", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".claude/settings.local.json\n.env\n.env.local\nopenspec/\n")
		writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), body)
		writeFile(t, filepath.Join(root, ".env"), "SECRET=1")
		writeFile(t, filepath.Join(root, ".env.local"), "LOCAL=2")
		writeFile(t, filepath.Join(root, "openspec", "changes", "x", "spec.md"), "# spec")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, ".claude", "settings.local.json"), body)
		mustExist(t, filepath.Join(wt, ".env"), "SECRET=1")
		mustExist(t, filepath.Join(wt, ".env.local"), "LOCAL=2")
		mustExist(t, filepath.Join(wt, "openspec", "changes", "x", "spec.md"), "# spec")
	})

	// a candidate that git does NOT ignore is never copied — the core constraint
	t.Run("skips non-ignored paths", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, "openspec/\n") // .env deliberately NOT ignored
		writeFile(t, filepath.Join(root, ".env"), "SECRET=1")
		writeFile(t, filepath.Join(root, "openspec", "spec.md"), "# spec")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, "openspec", "spec.md"), "# spec")
		mustAbsent(t, filepath.Join(wt, ".env"))
	})

	// never clobbers a worktree-local copy the user already edited
	t.Run("does not clobber existing", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env\n")
		writeFile(t, filepath.Join(root, ".env"), "SECRET=1")
		writeFile(t, filepath.Join(wt, ".env"), "LOCAL_EDIT")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, ".env"), "LOCAL_EDIT")
	})

	// config extra paths are seeded only when gitignored
	t.Run("config extra gated by gitignore", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, "config/local.yaml\n") // tracked.txt not ignored
		writeFile(t, filepath.Join(root, "config", "local.yaml"), "a: 1")
		writeFile(t, filepath.Join(root, "tracked.txt"), "keep")

		seedWorktreeFiles(root, wt, []string{"config/local.yaml", "tracked.txt"})

		mustExist(t, filepath.Join(wt, "config", "local.yaml"), "a: 1")
		mustAbsent(t, filepath.Join(wt, "tracked.txt"))
	})

	// no candidate present in the repo => no-op, no error
	t.Run("no candidates is a no-op", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env\n")

		seedWorktreeFiles(root, wt, nil)

		mustAbsent(t, filepath.Join(wt, ".env"))
	})

	// non-ASCII names survive the `git check-ignore -z` round-trip (no C-quoting)
	t.Run("seeds non-ascii names", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env.*\n")
		writeFile(t, filepath.Join(root, ".env.località"), "X=1")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, ".env.località"), "X=1")
	})

	// a .env.* directory is not dotenv state — never walked or seeded
	t.Run("skips .env.* directories", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env.d/\n")
		writeFile(t, filepath.Join(root, ".env.d", "inner"), "nope")

		seedWorktreeFiles(root, wt, nil)

		mustAbsent(t, filepath.Join(wt, ".env.d"))
	})

	// a symlinked source is recreated as a symlink, not dereferenced into a file
	t.Run("preserves symlinks", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		gitInitRepo(t, root, ".env\n")
		external := filepath.Join(t.TempDir(), "real.env")
		writeFile(t, external, "SECRET=1")
		if err := os.Symlink(external, filepath.Join(root, ".env")); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		seedWorktreeFiles(root, wt, nil)

		fi, err := os.Lstat(filepath.Join(wt, ".env"))
		if err != nil {
			t.Fatalf("expected seeded symlink: %v", err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("seeded .env is a real file, want symlink (mode %v)", fi.Mode())
		}
		if target, _ := os.Readlink(filepath.Join(wt, ".env")); target != external {
			t.Errorf("symlink target = %q, want %q", target, external)
		}
	})

	// when `git check-ignore` itself fails, fall back to the permission allowlist
	t.Run("git error falls back to allowlist", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir() // NOT a git repo => check-ignore errors
		writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), body)
		writeFile(t, filepath.Join(root, ".env"), "SECRET=1")

		seedWorktreeFiles(root, wt, nil)

		mustExist(t, filepath.Join(wt, ".claude", "settings.local.json"), body)
		mustAbsent(t, filepath.Join(wt, ".env")) // fallback seeds only the allowlist
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
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

func TestResolveAgentPrecedence(t *testing.T) {
	orig := cfg
	t.Cleanup(func() { cfg = orig })

	cfg = &config.Config{Agent: "codex"}
	if got, err := resolveAgent("claude"); err != nil || got != "claude" {
		t.Errorf("flag should win: got %q, %v", got, err)
	}
	if got, err := resolveAgent(""); err != nil || got != "codex" {
		t.Errorf("config should be used when no flag: got %q, %v", got, err)
	}

	cfg = &config.Config{}
	if got, err := resolveAgent(""); err != nil || got != config.DefaultAgent {
		t.Errorf("default should apply when neither set: got %q, %v", got, err)
	}

	if _, err := resolveAgent("gemini"); err == nil {
		t.Error("resolveAgent should reject an unsupported agent")
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
