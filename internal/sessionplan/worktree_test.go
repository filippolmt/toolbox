package sessionplan_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// TestPlanWorktreeSession is the replacement for what cmd used to assemble by
// hand after planning: PlanInput.Worktree is what makes a session a worktree
// session, and the whole shape of it is decided here.
func TestPlanWorktreeSession(t *testing.T) {
	cases := []struct {
		name        string
		agent       string
		prompt      string
		wantCommand string
	}{
		{name: "bare launch", agent: "codex", wantCommand: "codex;"},
		{name: "with initial prompt", agent: "claude", prompt: "add auth", wantCommand: `claude 'add auth';`},
		// A prompt is user input reaching a shell -c string: command
		// substitution, backticks and separators must stay literal.
		{name: "injection-shaped prompt", agent: "claude", prompt: "$(rm -rf /); `whoami`", wantCommand: "claude '$(rm -rf /); `whoami`';"},
		{name: "prompt with a single quote", agent: "codex", prompt: "it's a trap", wantCommand: `codex 'it'\''s a trap';`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			planHost, workspace := planWorkspace(t)
			repoRoot := filepath.Dir(workspace)
			if err := mkdirAll(t, filepath.Join(repoRoot, ".git")); err != nil {
				t.Fatalf("setup: %v", err)
			}

			plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost,
				Cfg:       testConfig(),
				Workspace: workspace,
				Worktree: &sessionplan.WorktreeSession{
					RepoRoot: repoRoot, Agent: c.agent, Prompt: c.prompt,
				},
			})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}

			// The main repo's .git is bound at its host path so git resolves the
			// linked worktree's gitdir pointer in-container.
			gitDir := filepath.Join(repoRoot, ".git")
			want := mountplan.Bind{Source: gitDir, Target: gitDir, Mode: "rw"}
			if !slices.Contains(plan.Binds, want) {
				t.Errorf("Binds missing the .git bind %+v, got %+v", want, plan.Binds)
			}

			// The agent runs in the attached exec session, never as the
			// container's main process — otherwise it runs twice.
			if want := []string{"/bin/zsh"}; !slices.Equal(plan.Cmd, want) {
				t.Errorf("Cmd = %v, want the idle shell %v", plan.Cmd, want)
			}
			wantExec := []string{"/bin/zsh", "-i", "-c", c.wantCommand + " exec /bin/zsh -i"}
			if !slices.Equal(plan.ExecCmd, wantExec) {
				t.Errorf("ExecCmd = %q, want %q", plan.ExecCmd, wantExec)
			}
		})
	}
}

// TestPlanWorktreeMissingGitDirIsASoftSkip: routing the .git bind through the
// mount pipeline means an absent source warns instead of producing a bind
// ContainerCreate would reject.
func TestPlanWorktreeMissingGitDirIsASoftSkip(t *testing.T) {
	planHost, workspace := planWorkspace(t)
	repoRoot := filepath.Dir(workspace) // no .git created

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost,
		Cfg:       testConfig(),
		Workspace: workspace,
		Worktree:  &sessionplan.WorktreeSession{RepoRoot: repoRoot, Agent: "claude"},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	gitDir := filepath.Join(repoRoot, ".git")
	for _, b := range plan.Binds {
		if b.Source == gitDir {
			t.Fatalf("missing .git still produced a bind: %+v", b)
		}
	}
	if !slices.ContainsFunc(plan.Warnings, func(w string) bool { return strings.Contains(w, gitDir) }) {
		t.Errorf("Warnings = %v, want one naming the missing %s", plan.Warnings, gitDir)
	}
}

// TestPlanWithoutWorktreeLeavesExecCmdAndGitBindAlone: a plain session must not
// pick up either half of the worktree shape.
func TestPlanWithoutWorktreeLeavesExecCmdAndGitBindAlone(t *testing.T) {
	planHost, workspace := planWorkspace(t)

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: workspace})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.ExecCmd != nil {
		t.Errorf("ExecCmd = %v, want nil so the exec reuses Cmd", plan.ExecCmd)
	}
	for _, b := range plan.Binds {
		if strings.HasSuffix(b.Source, "/.git") {
			t.Errorf("plain session carries a .git bind: %+v", b)
		}
	}
}
