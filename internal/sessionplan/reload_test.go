package sessionplan_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// reloadWorkspace prepares a workspace under a declared host's home.
func reloadWorkspace(t *testing.T) (fsx.Host, string) {
	t.Helper()
	host := fsx.Host{Home: t.TempDir()}
	ws := host.Join("ws")
	if err := mkdirAll(t, ws); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return host, ws
}

// TestPlanInjectsTheReloadMarkerPath pins the capability marker to the mount
// that carries it. The value is a path rather than a boolean because the
// container cannot build the path: it would need the state mount's target, the
// naming convention, and its own container name — and the hostname it can read
// is Docker's short id, not that name. One value computed once means the side
// that writes the marker and the side that reads it cannot diverge.
func TestPlanInjectsTheReloadMarkerPath(t *testing.T) {
	planHost, ws := reloadWorkspace(t)

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: ws})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	got := sessionplan.EnvValue(plan.Env, reload.MarkerEnv)
	want := mountplan.StateMountTarget + "/" + reload.MarkerName(plan.ContainerName)
	if got != want {
		t.Fatalf("%s = %q, want %q", reload.MarkerEnv, got, want)
	}

	// The path must land inside a directory the container actually has, or the
	// write fails and the reload never reaches the host.
	stateBind := ""
	for _, b := range plan.Binds {
		if b.Target == mountplan.StateMountTarget {
			stateBind = b.Source
		}
	}
	if stateBind == "" {
		t.Fatal("no bind at the state mount target to hold the marker")
	}
	if stateBind != plan.StateDir {
		t.Errorf("StateDir = %q, but the state bind sources %q", plan.StateDir, stateBind)
	}
	if !strings.HasPrefix(got, mountplan.StateMountTarget+"/") {
		t.Errorf("marker %q is not under the state mount", got)
	}
}

// TestPlanReloadWorkingDir covers the one piece of in-container process state a
// reload carries. Everything else is re-derived on purpose: an environment
// captured under the old image can hold a PATH or a CA variable the new image
// has moved, and replaying it imports the old image into the new container —
// the half-updated state the reload exists to avoid.
//
// The fallback is silent by design. A reload that lands in a directory the new
// container does not have is worse than one that lands at the top of the
// workspace.
func TestPlanReloadWorkingDir(t *testing.T) {
	planHost, ws := reloadWorkspace(t)

	canonical, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: ws})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	cases := []struct {
		name string
		cwd  string
		want string
	}{
		{name: "no handover keeps the canonical dir", cwd: "", want: canonical.WorkingDir},
		{name: "a subdirectory of the mirror is honoured", cwd: canonical.WorkingDir + "/pkg", want: canonical.WorkingDir + "/pkg"},
		{name: "the mirror root itself is honoured", cwd: canonical.WorkingDir, want: canonical.WorkingDir},
		{
			// Both spellings are the same content, and rejecting the one the
			// developer happened to be standing in would fire the fallback on
			// the common case.
			name: "the canonical /workspace spelling is honoured too",
			cwd:  mountplan.WorkspaceTarget + "/pkg",
			want: mountplan.WorkspaceTarget + "/pkg",
		},
		{name: "outside the workspace falls back", cwd: "/home/toolbox", want: canonical.WorkingDir},
		{name: "a prefix that is not a parent falls back", cwd: canonical.WorkingDir + "-other", want: canonical.WorkingDir},
		{name: "a relative path falls back", cwd: "pkg", want: canonical.WorkingDir},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost,
				Cfg:        testConfig(),
				Workspace:  ws,
				ReloadFrom: &reload.From{Container: "toolbox-old-1234abcd", Cwd: tc.cwd},
			})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if plan.WorkingDir != tc.want {
				t.Errorf("WorkingDir = %q, want %q", plan.WorkingDir, tc.want)
			}
			// PWD is composed from the working dir, so the carried directory
			// has to reach the shell as well as ContainerCreate.
			if got := sessionplan.EnvValue(plan.Env, "PWD"); got != tc.want {
				t.Errorf("PWD = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPlanCarriesTheHandoverToTheContainerEdge pins the split the two variables
// draw: the marker goes into the container, the handover never does. The
// container edge reads it off the typed plan instead.
func TestPlanCarriesTheHandoverToTheContainerEdge(t *testing.T) {
	planHost, ws := reloadWorkspace(t)
	from := &reload.From{Container: "toolbox-old-1234abcd", ImageDigest: "sha256:old"}

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: ws, ReloadFrom: from})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.ReloadFrom != from {
		t.Errorf("ReloadFrom = %+v, want the input handover", plan.ReloadFrom)
	}
	for _, e := range plan.Env {
		if strings.HasPrefix(e, reload.FromEnv+"=") {
			t.Errorf("%s reached the container env as %q", reload.FromEnv, e)
		}
	}
}

// TestPlanOmitsTheReloadMarkerWithoutTheStateMount closes the one hole the
// capability marker could otherwise open in itself. Presence of the variable
// promises the host will read what the container writes; a session whose
// `mounts:` dropped the state mount breaks that promise silently, because the
// entrypoint creates ~/.toolbox-state anyway, so the write lands in a
// container-local directory, the shell exits, and nothing reloads. Withholding
// the variable turns that into the refusal at the prompt it should have been.
func TestPlanOmitsTheReloadMarkerWithoutTheStateMount(t *testing.T) {
	tmpHome := t.TempDir()
	planHost := fsx.Host{Home: tmpHome}
	ws := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, ws); err != nil {
		t.Fatalf("setup: %v", err)
	}

	cfg := testConfig()
	cfg.Mounts = []config.Mount{{Name: "state", Disabled: true}}

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: cfg, Workspace: ws})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.StateDir != "" {
		t.Fatalf("StateDir = %q, want empty with the state mount disabled", plan.StateDir)
	}
	if got := sessionplan.EnvValue(plan.Env, reload.MarkerEnv); got != "" {
		t.Errorf("%s = %q with no state mount — the container would write where the host cannot read", reload.MarkerEnv, got)
	}
}

// TestPlanReloadReproducesTheLaunchMode covers what a reload puts back in front
// of the developer. It reproduces how the session was *started*, never what
// they were doing: only a worktree session auto-launches an agent, so a plain
// shell reloads into a plain shell.
func TestPlanReloadReproducesTheLaunchMode(t *testing.T) {
	planHost, ws := reloadWorkspace(t)

	plain, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: ws})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plain.LaunchesAgent() {
		t.Error("a plain shell claims to launch an agent")
	}

	wt, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost,
		Cfg:       testConfig(),
		Workspace: ws,
		Worktree:  &sessionplan.WorktreeSession{RepoRoot: ws, Agent: "claude", Prompt: "do the thing"},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !wt.LaunchesAgent() {
		t.Fatal("a worktree session claims to launch no agent")
	}
}

// TestPlanReloadResumesTheAgent pins both halves of the resume rule: the
// prompt is dropped (it was sent once and completed — re-sending it would
// start the work again), and the resume itself is conditional on the carried
// working directory having survived validation, because `claude --continue` is
// keyed on the directory and the workspace is mounted twice. On the fallback
// the agent launches bare: resuming the wrong lineage in silence is worse.
func TestPlanReloadResumesTheAgent(t *testing.T) {
	planHost, ws := reloadWorkspace(t)
	canonical, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost, Cfg: testConfig(), Workspace: ws})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	cases := []struct {
		name  string
		agent string
		from  *reload.From
		want  string
	}{
		{
			name:  "claude resumes on a carried cwd",
			agent: "claude",
			from:  &reload.From{Container: "c", Cwd: canonical.WorkingDir + "/pkg", Resume: true},
			want:  "claude --continue",
		},
		{
			// Flag versus subcommand: there is no shared shape to factor out.
			name:  "codex resumes through its subcommand",
			agent: "codex",
			from:  &reload.From{Container: "c", Cwd: canonical.WorkingDir + "/pkg", Resume: true},
			want:  "codex resume --last",
		},
		{
			name:  "a rejected cwd launches bare",
			agent: "claude",
			from:  &reload.From{Container: "c", Cwd: "/home/toolbox", Resume: true},
			want:  "claude;",
		},
		{
			name:  "no cwd at all launches bare",
			agent: "claude",
			from:  &reload.From{Container: "c", Resume: true},
			want:  "claude;",
		},
		{
			name:  "a plain shell's reload never resumes",
			agent: "claude",
			from:  &reload.From{Container: "c", Cwd: canonical.WorkingDir, Resume: false},
			want:  "claude;",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := sessionplan.Plan(sessionplan.PlanInput{Host: planHost,
				Cfg:        testConfig(),
				Workspace:  ws,
				Worktree:   &sessionplan.WorktreeSession{RepoRoot: ws, Agent: tc.agent, Prompt: "do the thing"},
				ReloadFrom: tc.from,
			})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			got := strings.Join(plan.ExecCmd, " ")
			if !strings.Contains(got, tc.want) {
				t.Errorf("ExecCmd = %q, want it to contain %q", got, tc.want)
			}
			// The prompt is spent whatever the resume decided: it was answered
			// once, and a reload that re-sends it starts the work over.
			if strings.Contains(got, "do the thing") {
				t.Errorf("the reload replayed the original prompt: %q", got)
			}
		})
	}
}
