package container

import (
	"context"
	"errors"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/mountplan"
)

func TestList(t *testing.T) {
	m := &mockClient{
		listFn: func(_ context.Context, opts client.ContainerListOptions) ([]container.Summary, error) {
			if !opts.All {
				t.Errorf("List must request All containers, got All=false")
			}
			return []container.Summary{
				{
					Names:  []string{"/toolbox-proj-a1b2c3d4"},
					Status: "Up 2 hours",
					Mounts: []container.MountPoint{
						{Destination: "/other", Source: "/somewhere"},
						{Destination: mountplan.WorkspaceTarget, Source: "/home/u/proj"},
					},
				},
				// Named shell with no /workspace bind → Workspace falls back to "-".
				{Names: []string{"/toolbox-named-infra"}, Status: "Exited (0) 1 minute ago"},
				// Non-toolbox container the daemon's name filter can still return.
				{Names: []string{"/redis"}, Status: "Up 5 days"},
				// Nameless container is skipped, not panicked on.
				{Names: nil, Status: "Up 1 minute"},
			}, nil
		},
	}

	items, err := List(context.Background(), m)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := []Item{
		{Name: "toolbox-named-infra", Workspace: "-", Status: "Exited (0) 1 minute ago"},
		{Name: "toolbox-proj-a1b2c3d4", Workspace: "/home/u/proj", Status: "Up 2 hours"},
	}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d: %+v", len(items), len(want), items)
	}
	for i, w := range want {
		if items[i] != w {
			t.Errorf("item %d = %+v, want %+v", i, items[i], w)
		}
	}
}

func TestListError(t *testing.T) {
	sentinel := errors.New("boom")
	m := &mockClient{
		listFn: func(context.Context, client.ContainerListOptions) ([]container.Summary, error) {
			return nil, sentinel
		},
	}
	if _, err := List(context.Background(), m); !errors.Is(err, sentinel) {
		t.Errorf("List error = %v, want wrapped %v", err, sentinel)
	}
}
