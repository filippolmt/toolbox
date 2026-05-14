package imageplan

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// mockClient implements the subset of client.APIClient used by Ensure.
type mockClient struct {
	client.APIClient
	imgInspFn func(ctx context.Context, id string) (image.InspectResponse, error)
}

func (m *mockClient) ImageInspect(ctx context.Context, id string, _ ...client.ImageInspectOption) (image.InspectResponse, error) {
	if m.imgInspFn != nil {
		return m.imgInspFn(ctx, id)
	}
	return image.InspectResponse{}, errors.New("ImageInspect not mocked")
}

// Stubs to satisfy other interface methods if accidentally called.
func (m *mockClient) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
	return container.CreateResponse{}, errors.New("ContainerCreate must not be called from imageplan.Ensure")
}
func (m *mockClient) ImagePull(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
	return nil, errors.New("ImagePull must not be called from imageplan.Ensure")
}
func (m *mockClient) Close() error { return nil }

func TestEnsureNoOpWhenImagePresent(t *testing.T) {
	called := false
	orig := buildImageFn
	t.Cleanup(func() { buildImageFn = orig })
	buildImageFn = func(_ context.Context, _ client.APIClient, _ build.Options) error {
		called = true
		return nil
	}

	mock := &mockClient{
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
		},
	}

	err := Ensure(context.Background(), mock, sessionplan.Image{Ref: "ghcr.io/example:latest"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("buildImageFn must not run when image already present locally")
	}
}

func TestEnsureRegistryMissingErrors(t *testing.T) {
	orig := buildImageFn
	t.Cleanup(func() { buildImageFn = orig })
	buildImageFn = func(_ context.Context, _ client.APIClient, _ build.Options) error {
		t.Fatal("buildImageFn must not run for registry tag")
		return nil
	}

	mock := &mockClient{
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, errors.New("no such image")
		},
	}

	err := Ensure(context.Background(), mock, sessionplan.Image{Ref: "ghcr.io/example:latest", IsLocal: false}, nil)
	if err == nil {
		t.Fatal("expected error for missing registry image")
	}
	if !strings.Contains(err.Error(), "not available locally") {
		t.Errorf("error should mention not-available-locally, got: %v", err)
	}
}

func TestEnsureLocalHashTriggersBuild(t *testing.T) {
	var gotTag string
	var gotArgs map[string]*string
	orig := buildImageFn
	t.Cleanup(func() { buildImageFn = orig })
	buildImageFn = func(_ context.Context, _ client.APIClient, opts build.Options) error {
		gotTag = opts.Tag
		gotArgs = opts.BuildArgs
		return nil
	}

	mock := &mockClient{
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, errors.New("no such image")
		},
	}

	val := "true"
	args := map[string]*string{"INSTALL_FOO": &val}
	err := Ensure(context.Background(), mock, sessionplan.Image{Ref: "toolbox:local-abcd1234", IsLocal: true}, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotTag != "toolbox:local-abcd1234" {
		t.Errorf("build tag = %q, want %q", gotTag, "toolbox:local-abcd1234")
	}
	if gotArgs["INSTALL_FOO"] == nil || *gotArgs["INSTALL_FOO"] != "true" {
		t.Errorf("build args = %v, want INSTALL_FOO=true", gotArgs)
	}
}

func TestRefreshNoOpForLocalImage(t *testing.T) {
	// Refresh on a local image must not touch the daemon. Use a mock that
	// would error on any call; absence of an error proves the early return.
	mock := &mockClient{
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			t.Fatal("ImageInspect must not be called for local image")
			return image.InspectResponse{}, nil
		},
	}
	Refresh(context.Background(), mock, sessionplan.Image{Ref: "toolbox:local-abcd1234", IsLocal: true})
}
