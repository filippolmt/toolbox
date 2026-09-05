package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/worktree"
)

// stubDaemon is the daemon double startSession's assembly needs: the client is
// constructed and closed by the assembly, and the only call it makes through it
// before the session attaches is the image-digest resolve. Embedding a nil
// client.APIClient keeps every other method a panic naming itself, so a call
// this assembly is not supposed to make cannot pass unnoticed.
//
// This is not the embedding container-runtime.md forbids: that ban is on
// dockertest.Fake, the *shared* double, where embedding would satisfy every
// narrow interface in the tree by accident and undo the narrowing. cmd declares
// no narrow interface — newDockerClient must hand back a real
// client.APIClient — so there is nothing here for an embed to erode, and
// spelling out ~80 methods to reach two would buy nothing.
type stubDaemon struct {
	client.APIClient
	repoDigest string
}

func (stubDaemon) Close() error { return nil }

func (s stubDaemon) ImageInspect(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	return dockertest.ImageInspectResult(ref, s.repoDigest), nil
}

// sessionHarness points the assembly at a temporary home and a stub daemon,
// and returns the plan the attach was handed. Nothing here mutates a flag
// global: the intent *is* the input, which is the whole point of the seam.
func sessionHarness(t *testing.T) **sessionplan.SessionPlan {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if err := os.Unsetenv(reload.FromEnv); err != nil {
		t.Fatalf("setup: %v", err)
	}

	origClient, origShell := newDockerClient, shellFn
	t.Cleanup(func() { newDockerClient, shellFn = origClient, origShell })

	newDockerClient = func() (client.APIClient, error) {
		return stubDaemon{repoDigest: "sha256:cafe"}, nil
	}
	var attached *sessionplan.SessionPlan
	shellFn = func(_ context.Context, _ client.APIClient, p *sessionplan.SessionPlan) (*reload.From, error) {
		attached = p
		return nil, nil
	}
	return &attached
}

// sessionWorkspace creates an existing directory under the temporary home, the
// one fixture the mount stage's filesystem side effects need.
func sessionWorkspace(t *testing.T, name string) string {
	t.Helper()
	ws := filepath.Join(os.Getenv("HOME"), name)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return ws
}

// TestStartSessionPlansWhatTheIntentDescribes is the seam the whole module
// exists for: the assembly is reachable with a value, so what a `shell`
// invocation resolves its flags *into* is asserted here rather than only
// downstream of the flag globals no test can set without mutating them.
func TestStartSessionPlansWhatTheIntentDescribes(t *testing.T) {
	attached := sessionHarness(t)
	ws := sessionWorkspace(t, "ws")

	err := startSession(sessionIntent{Plan: sessionplan.PlanInput{
		Cfg:       &config.Config{Shell: "zsh"},
		Workspace: ws,
		Name:      "web",
		Ports:     []string{"7171:7171"},
	}})
	if err != nil {
		t.Fatalf("startSession: %v", err)
	}

	plan := *attached
	if plan == nil {
		t.Fatal("the session never attached")
	}
	if want := "toolbox-named-web"; plan.ContainerName != want {
		t.Errorf("ContainerName = %q, want %q — the intent's Name never reached the plan", plan.ContainerName, want)
	}
	if bindings := plan.PortBindings[network.MustParsePort("7171/tcp")]; len(bindings) == 0 {
		t.Errorf("PortBindings = %v, want the intent's published port", plan.PortBindings)
	}
	if plan.Image.Ref == "" {
		t.Error("Image.Ref empty — the image was never resolved")
	}
	// The digest is the baseline the update prefetch and the session reload
	// compare the local store against, and the assembly is the only thing that
	// resolves it — dropping it would land the developer back on a stale image
	// with nothing else to notice.
	if got := sessionplan.EnvValue(plan.Env, sessionplan.ImageDigestEnv); got != "sha256:cafe" {
		t.Errorf("%s = %q, want the digest the store answered with", sessionplan.ImageDigestEnv, got)
	}
}

// TestStartSessionResolvesTheProximoGate pins the other half of the assembly's
// own contribution: the Proximo Availability Gate is derived here, once, from
// the same host the plan is built against — an intent declares none, and a
// session that reached the planner without one would silently run with the
// integration off.
//
// `proximo: true` is the arm that needs no host state, so what this asserts is
// that the gate was resolved at all, on any machine.
func TestStartSessionResolvesTheProximoGate(t *testing.T) {
	attached := sessionHarness(t)
	ws := sessionWorkspace(t, "ws")

	err := startSession(sessionIntent{Plan: sessionplan.PlanInput{
		Cfg:       &config.Config{Shell: "zsh", Proximo: new(true)},
		Workspace: ws,
	}})
	if err != nil {
		t.Fatalf("startSession: %v", err)
	}

	plan := *attached
	if plan == nil {
		t.Fatal("the session never attached")
	}
	if !plan.Proximo {
		t.Error("plan.Proximo = false — the assembly never resolved the gate")
	}
}

// TestStartSessionOpensAWorktreeSession asserts the two things only a worktree
// intent adds, both of which the assembly owns: the plan launches the agent in
// the attached exec, and the gitignored per-repo state is carried into the
// fresh checkout. Before this seam existed the seeding sat in a second
// composition root, where it was reachable only through `worktree create`.
func TestStartSessionOpensAWorktreeSession(t *testing.T) {
	attached := sessionHarness(t)
	root := sessionWorkspace(t, "repo")
	wt := sessionWorkspace(t, "repo/.worktrees/tbx-feat")

	// A real repo: the seeding gate is `git check-ignore`, so an ignored file
	// is the only kind that may be carried.
	gitInitRepo(t, root, ".claude/\n")
	writeFile(t, filepath.Join(root, worktree.LocalSettingsRel), `{"permissions":{}}`)

	err := startSession(sessionIntent{Plan: sessionplan.PlanInput{
		Cfg:       &config.Config{Shell: "zsh"},
		Workspace: wt,
		Worktree:  &sessionplan.WorktreeSession{RepoRoot: root, Agent: "claude"},
	}})
	if err != nil {
		t.Fatalf("startSession: %v", err)
	}

	plan := *attached
	if plan == nil {
		t.Fatal("the session never attached")
	}
	if !plan.LaunchesAgent() {
		t.Errorf("ExecCmd = %v; a worktree intent must plan the agent launch", plan.ExecCmd)
	}
	seeded := filepath.Join(wt, worktree.LocalSettingsRel)
	if _, err := os.Stat(seeded); err != nil {
		t.Errorf("%s was not seeded into the worktree: %v", worktree.LocalSettingsRel, err)
	}
}

// TestStartSessionMigratesLegacyStateForEveryIntent locks the first of the two
// divergences this module closed: the relocation of toolbox-own state into the
// ~/.toolbox/toolbox namespace used to run on the `shell` path only, so a
// worktree session left it to whichever `toolbox shell` came next — and until
// one did, the state mount and the pull cache were resolved against a root the
// migration had not reached yet.
func TestStartSessionMigratesLegacyStateForEveryIntent(t *testing.T) {
	sessionHarness(t)
	root := sessionWorkspace(t, "repo")
	wt := sessionWorkspace(t, "repo/.worktrees/tbx-feat")
	gitInitRepo(t, root, "")

	home := os.Getenv("HOME")
	legacy := filepath.Join(home, ".toolbox", "state", "pull-cache", "marker")
	writeFile(t, legacy, "stamp")

	err := startSession(sessionIntent{Plan: sessionplan.PlanInput{
		Cfg:       &config.Config{Shell: "zsh"},
		Workspace: wt,
		Worktree:  &sessionplan.WorktreeSession{RepoRoot: root, Agent: "claude"},
	}})
	if err != nil {
		t.Fatalf("startSession: %v", err)
	}

	mustExist(t, filepath.Join(home, ".toolbox", "toolbox", "state", "pull-cache", "marker"), "stamp")
	mustAbsent(t, filepath.Join(home, ".toolbox", "state"))
}

// TestStartSessionOffersTheBridgeTipForEveryIntent locks the second: the
// install hint for the host-side bridge used to reach the `shell` path only,
// so a developer whose every session is a worktree session was never told the
// forwarding they had enabled in config was not installed.
func TestStartSessionOffersTheBridgeTipForEveryIntent(t *testing.T) {
	sessionHarness(t)
	root := sessionWorkspace(t, "repo")
	wt := sessionWorkspace(t, "repo/.worktrees/tbx-feat")
	gitInitRepo(t, root, "")

	enabled := true
	stderr := captureCmdStderr(t, func() {
		err := startSession(sessionIntent{Plan: sessionplan.PlanInput{
			Cfg:       &config.Config{Shell: "zsh", Bridge: &enabled},
			Workspace: wt,
			Worktree:  &sessionplan.WorktreeSession{RepoRoot: root, Agent: "claude"},
		}})
		if err != nil {
			t.Errorf("startSession: %v", err)
		}
	})

	if !strings.Contains(stderr, "toolbox bridge install") {
		t.Errorf("stderr = %q, want the bridge install hint", stderr)
	}
}

// TestStartSessionCarriesTheReentryForm pins the one field the assembly only
// passes through: a session that asked for a reload tail-calls the form its
// caller rendered. Dropping it here would recreate the session as a bare
// `toolbox shell` — a different container from the one the payload destroys —
// and nothing before the exec would say so.
func TestStartSessionCarriesTheReentryForm(t *testing.T) {
	sessionHarness(t)
	ws := sessionWorkspace(t, "ws")

	shellFn = func(context.Context, client.APIClient, *sessionplan.SessionPlan) (*reload.From, error) {
		return &reload.From{Container: "toolbox-named-web"}, nil
	}
	exec := stubExecSelf(t, "/usr/local/bin/toolbox")

	err := startSession(sessionIntent{
		Plan: sessionplan.PlanInput{
			Cfg:       &config.Config{Shell: "zsh"},
			Workspace: ws,
			Name:      "web",
		},
		Reentry: []string{"shell", "web", "--peer=false"},
	})
	if err == nil {
		t.Fatal("startSession returned nil — the stub says the process was not replaced")
	}

	want := []string{"/usr/local/bin/toolbox", "shell", "web", "--peer=false"}
	if !slices.Equal(exec.argv, want) {
		t.Errorf("argv = %q, want %q", exec.argv, want)
	}
}

// TestStartSessionLeavesNoPlanSideEffectsWhenTheClientFails pins the second
// ordering invariant: the plan is built *after* the Docker client, so an
// unusable daemon (a bad DOCKER_HOST, a socket that is not there) costs the
// developer nothing on disk. Planning first would materialise the whole
// ~/.toolbox mount root and the workspace's own dirs before failing on a
// client this session can never get.
func TestStartSessionLeavesNoPlanSideEffectsWhenTheClientFails(t *testing.T) {
	sessionHarness(t)
	ws := sessionWorkspace(t, "ws")

	newDockerClient = func() (client.APIClient, error) {
		return nil, errors.New("cannot connect to the Docker daemon")
	}

	err := startSession(sessionIntent{Plan: sessionplan.PlanInput{
		Cfg:       &config.Config{Shell: "zsh"},
		Workspace: ws,
	}})
	if err == nil {
		t.Fatal("startSession accepted a daemon it could not reach")
	}

	// ~/.toolbox is the mount root the plan materialises. Nothing before the
	// client touches it: the legacy-state migration returns early when there
	// is nothing to relocate, and the profile notice only stats.
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".toolbox")); !os.IsNotExist(err) {
		t.Errorf("the mount root was materialised before the client was constructed (stat err = %v)", err)
	}
}
