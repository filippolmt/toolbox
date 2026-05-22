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

func (m *mockClient) ContainerCreate(_ context.Context, _ *container.Config, _ *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, _ string) (container.CreateResponse, error) {
	return container.CreateResponse{}, errors.New("ContainerCreate must not be called from imageplan.Ensure")
}
func (m *mockClient) ImagePull(_ context.Context, _ string, _ image.PullOptions) (io.ReadCloser, error) {
	return nil, errors.New("ImagePull must not be called from imageplan.Ensure")
}
func (m *mockClient) Close() error { return nil }

func TestEnsureNoOpWhenImagePresent(t *testing.T) {
	mock := &mockClient{
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
		},
	}
	if err := Ensure(context.Background(), mock, sessionplan.Image{Ref: "ghcr.io/example:latest"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureRegistryMissingErrors(t *testing.T) {
	mock := &mockClient{
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, errors.New("no such image")
		},
	}
	err := Ensure(context.Background(), mock, sessionplan.Image{Ref: "ghcr.io/example:latest"})
	if err == nil {
		t.Fatal("expected error for missing image")
	}
	if !strings.Contains(err.Error(), "not available locally") {
		t.Errorf("error should mention not-available-locally, got: %v", err)
	}
	if !strings.Contains(err.Error(), "toolbox build") {
		t.Errorf("error should mention `toolbox build`, got: %v", err)
	}
}
