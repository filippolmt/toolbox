//go:build dockergate

package container

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/teardown"
)

// reloadSessionPlan builds one ordinary session plan against the image under
// test, the same IMAGE_TAG override the peer gate uses. Not opted into peer
// messaging: this gate is about container identity across the swap, and the
// shared anchor would only add a second container to reason about.
func reloadSessionPlan(t *testing.T, host fsx.Host, from *reload.From) *sessionplan.SessionPlan {
	t.Helper()
	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Host:       host,
		Cfg:        &config.Config{Shell: "zsh", Image: os.Getenv("IMAGE_TAG"), Pull: config.PullNever},
		Workspace:  reloadGateWorkspace,
		ReloadFrom: from,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return plan
}

// reloadGateWorkspace is fixed for the whole test: the container name is
// derived from the workspace path, and the reload's destroy-then-create is
// only observable when both halves resolve to the same name.
var reloadGateWorkspace string

// containerID returns the id of the named container, or "" when it is gone.
func containerID(ctx context.Context, t *testing.T, cli client.APIClient, name string) string {
	t.Helper()
	res, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return ""
	}
	return res.Container.ID
}

// TestReloadReplacesTheContainer is the only place a real reload is observable
// at all. Every unit around it proves the *plan* — which calls we make and in
// what order — and a fake Docker client can say nothing about what the daemon
// does in reply. The two effects that live entirely outside this process are
// exactly the ones a mock cannot establish: that the old container is really
// gone, and that the deterministic name it held is free in time for the create.
//
// The exec seam is stubbed on both legs, as it must be everywhere: the first
// leg's stub writes the marker the way the in-container shell function does,
// and the second stands in for the process the syscall.Exec would have
// produced. The syscall itself is never exercised, here or anywhere.
func TestReloadReplacesTheContainer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cli, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cli.Close()

	// A throwaway HOME keeps the run off the developer's own ~/.toolbox; the
	// workspace under it is what the container name hashes.
	host := sandboxHome(t)
	reloadGateWorkspace = t.TempDir()

	first := reloadSessionPlan(t, host, nil)
	t.Cleanup(func() {
		_ = teardown.StopOne(context.Background(), cli, first.ContainerName, teardown.DefaultStopGrace)
	})

	// Leg one: an ordinary session whose shell asks for a reload on its way
	// out. The marker is written through the same helper the image's function
	// writes, at the same host path the host reads.
	origExec := execShellFn
	execShellFn = func(context.Context, client.APIClient, string, []string) error {
		return reload.WriteMarker(first.ReloadMarkerPath(), "")
	}
	handover, err := Shell(ctx, cli, first)
	execShellFn = origExec
	if err != nil {
		t.Fatalf("Shell (first leg): %v", err)
	}
	if handover == nil {
		t.Fatal("the marker written by the attached shell produced no reload request")
	}

	// The teardown must have been suppressed: the next process owns it, and it
	// owns it only after proving it has an image to move onto.
	oldID := containerID(ctx, t, cli, first.ContainerName)
	if oldID == "" {
		t.Fatal("the container was destroyed before the reload's own verify")
	}
	if handover.Container != first.ContainerName {
		t.Fatalf("handover names %q, want the container to destroy (%q)", handover.Container, first.ContainerName)
	}

	// Leg two: the process the re-exec would have produced.
	execShellFn = func(context.Context, client.APIClient, string, []string) error { return nil }
	defer func() { execShellFn = origExec }()

	second := reloadSessionPlan(t, host, handover)
	if _, err := Shell(ctx, cli, second); err != nil {
		t.Fatalf("Shell (reload leg): %v", err)
	}

	newID := containerID(ctx, t, cli, second.ContainerName)
	switch {
	case newID == "":
		t.Fatal("the reload destroyed the session and created nothing in its place")
	case newID == oldID:
		t.Fatal("the reload reattached to the old container instead of replacing it")
	}
}
