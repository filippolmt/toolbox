package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestApplyWorktreeSessionMutatesPlan(t *testing.T) {
	plan := &sessionplan.SessionPlan{Cmd: []string{"/bin/zsh", "-i"}}
	applyWorktreeSession(plan, "/repo", "zsh", "codex")

	want := mountplan.Bind{Source: "/repo/.git", Target: "/repo/.git", Mode: "rw"}
	found := false
	for _, b := range plan.Binds {
		if b == want {
			found = true
		}
	}
	if !found {
		t.Errorf("plan.Binds missing .git bind %+v, got %+v", want, plan.Binds)
	}

	// The agent launches in the attached exec session (ExecCmd), not the
	// container's main process (Cmd) — otherwise the agent runs twice.
	joined := strings.Join(plan.ExecCmd, " ")
	if !strings.Contains(joined, "codex") {
		t.Errorf("plan.ExecCmd should invoke the agent, got %q", joined)
	}
	if !strings.Contains(joined, "/bin/zsh") {
		t.Errorf("plan.ExecCmd should use the configured shell, got %q", joined)
	}
	if strings.Join(plan.Cmd, " ") != "/bin/zsh -i" {
		t.Errorf("plan.Cmd (container main process) must stay the idle shell, got %q", plan.Cmd)
	}
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
