package sessionplan_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// reloadWorkspace prepares a workspace under a sandboxed HOME.
func reloadWorkspace(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	ws := filepath.Join(tmpHome, "ws")
	if err := mkdirAll(t, ws); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return ws
}

// TestPlanInjectsTheReloadMarkerPath pins the capability marker to the mount
// that carries it. The value is a path rather than a boolean because the
// container cannot build the path: it would need the state mount's target, the
// naming convention, and its own container name — and the hostname it can read
// is Docker's short id, not that name. One value computed once means the side
// that writes the marker and the side that reads it cannot diverge.
func TestPlanInjectsTheReloadMarkerPath(t *testing.T) {
	ws := reloadWorkspace(t)

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: ws})
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
	ws := reloadWorkspace(t)

	canonical, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: ws})
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
			plan, err := sessionplan.Plan(sessionplan.PlanInput{
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
	ws := reloadWorkspace(t)
	from := &reload.From{Container: "toolbox-old-1234abcd", ImageDigest: "sha256:old"}

	plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: testConfig(), Workspace: ws, ReloadFrom: from})
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
