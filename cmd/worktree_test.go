package cmd

import (
	"io"
	"os"
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

func TestSeedLocalSettings(t *testing.T) {
	const body = `{"permissions":{"allow":["Bash(go test:*)"]}}`

	// seeds when the worktree lacks the file and the main repo has it
	t.Run("seeds into fresh worktree", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), body)

		seedLocalSettings(root, wt)

		got, err := os.ReadFile(filepath.Join(wt, ".claude", "settings.local.json"))
		if err != nil {
			t.Fatalf("expected seeded file: %v", err)
		}
		if string(got) != body {
			t.Errorf("seeded content = %q, want %q", got, body)
		}
	})

	// never clobbers a worktree-local copy the user already edited
	t.Run("does not clobber existing", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(root, ".claude", "settings.local.json"), body)
		dst := filepath.Join(wt, ".claude", "settings.local.json")
		writeFile(t, dst, `{"local":"edit"}`)

		seedLocalSettings(root, wt)

		got, _ := os.ReadFile(dst)
		if string(got) != `{"local":"edit"}` {
			t.Errorf("clobbered worktree-local edit: %q", got)
		}
	})

	// no source in the main repo => no-op, no error, no file created
	t.Run("no source is a no-op", func(t *testing.T) {
		root, wt := t.TempDir(), t.TempDir()

		seedLocalSettings(root, wt)

		if _, err := os.Stat(filepath.Join(wt, ".claude", "settings.local.json")); !os.IsNotExist(err) {
			t.Errorf("expected no seeded file, stat err = %v", err)
		}
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
