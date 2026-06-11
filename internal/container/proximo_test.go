package container

import (
	"context"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// proximoCreatePlan builds a minimal create-path plan with the proximo flag
// and a base ExtraHosts entry (the bridge mapping).
func proximoCreatePlan(t *testing.T, proximoOn bool) *sessionplan.SessionPlan {
	t.Helper()
	ws := testWorkspace(t)
	return &sessionplan.SessionPlan{
		Image:         sessionplan.Image{Ref: "ghcr.io/filippolmt/toolbox:latest"},
		ContainerName: sessionplan.ContainerNameFor(ws),
		Cmd:           []string{"zsh"},
		Proximo:       proximoOn,
		ExtraHosts:    []string{"host.docker.internal:host-gateway"},
	}
}

// TestShellProximoAugmentsExtraHosts asserts that, on the create path, a
// proximo-enabled plan has every proximo.hosts label (from running containers)
// appended to ExtraHosts as <host>:host-gateway, keeping the base entries.
func TestShellProximoAugmentsExtraHosts(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	var capturedHosts []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		listFn: func(_ context.Context, _ client.ContainerListOptions) ([]container.Summary, error) {
			return []container.Summary{
				{Labels: map[string]string{"proximo.hosts": "zeromiglia.test,mailpit.test"}},
				{Labels: map[string]string{"some.other": "x"}},
			}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			capturedHosts = hostCfg.ExtraHosts
			return container.CreateResponse{ID: "px1"}, nil
		},
	}

	if err := Shell(context.Background(), mock, proximoCreatePlan(t, true)); err != nil {
		t.Fatalf("Shell: %v", err)
	}

	for _, want := range []string{
		"host.docker.internal:host-gateway",
		"mailpit.test:host-gateway",
		"zeromiglia.test:host-gateway",
	} {
		if !slices.Contains(capturedHosts, want) {
			t.Errorf("ExtraHosts missing %q, got %v", want, capturedHosts)
		}
	}
}

// TestShellNonProximoSkipsDiscovery asserts a non-proximo plan never lists
// containers (listFn is left nil and would error) and forwards ExtraHosts
// verbatim.
func TestShellNonProximoSkipsDiscovery(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	var capturedHosts []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		// listFn intentionally nil: ContainerList must NOT be called.
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			capturedHosts = hostCfg.ExtraHosts
			return container.CreateResponse{ID: "np1"}, nil
		},
	}

	if err := Shell(context.Background(), mock, proximoCreatePlan(t, false)); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if !slices.Equal(capturedHosts, []string{"host.docker.internal:host-gateway"}) {
		t.Errorf("ExtraHosts = %v, want base unchanged", capturedHosts)
	}
}
