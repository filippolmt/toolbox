package container

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/filippolmt/toolbox/internal/config"
)

// notFoundError implementa l'interfaccia errdefs per errori "not found".
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string  { return e.msg }
func (e *notFoundError) NotFound()      {}
func (e *notFoundError) Unwrap() error  { return nil }

// mockClient implementa i metodi di client.APIClient necessari per i test.
// I metodi non mockati panicano per evidenziare chiamate inattese.
type mockClient struct {
	client.APIClient

	inspectFn func(ctx context.Context, id string) (container.InspectResponse, error)
	createFn  func(ctx context.Context, cfg *container.Config, hostCfg *container.HostConfig) (container.CreateResponse, error)
	startFn   func(ctx context.Context, id string, opts container.StartOptions) error
	stopFn    func(ctx context.Context, id string, opts container.StopOptions) error
	removeFn  func(ctx context.Context, id string, opts container.RemoveOptions) error
	imgInspFn func(ctx context.Context, id string) (image.InspectResponse, error)
}

func (m *mockClient) ContainerInspect(ctx context.Context, id string) (container.InspectResponse, error) {
	if m.inspectFn != nil {
		return m.inspectFn(ctx, id)
	}
	return container.InspectResponse{}, fmt.Errorf("ContainerInspect not mocked")
}

func (m *mockClient) ContainerCreate(ctx context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ *network.NetworkingConfig, _ *ocispec.Platform, name string) (container.CreateResponse, error) {
	if m.createFn != nil {
		return m.createFn(ctx, cfg, hostCfg)
	}
	return container.CreateResponse{}, fmt.Errorf("ContainerCreate not mocked")
}

func (m *mockClient) ContainerStart(ctx context.Context, id string, opts container.StartOptions) error {
	if m.startFn != nil {
		return m.startFn(ctx, id, opts)
	}
	return nil
}

func (m *mockClient) ContainerStop(ctx context.Context, id string, opts container.StopOptions) error {
	if m.stopFn != nil {
		return m.stopFn(ctx, id, opts)
	}
	return nil
}

func (m *mockClient) ContainerRemove(ctx context.Context, id string, opts container.RemoveOptions) error {
	if m.removeFn != nil {
		return m.removeFn(ctx, id, opts)
	}
	return nil
}

func (m *mockClient) ImageInspect(ctx context.Context, id string, _ ...client.ImageInspectOption) (image.InspectResponse, error) {
	if m.imgInspFn != nil {
		return m.imgInspFn(ctx, id)
	}
	return image.InspectResponse{}, fmt.Errorf("ImageInspect not mocked")
}

func (m *mockClient) Close() error { return nil }

// --- Helpers ---

func testConfig() *config.Config {
	return &config.Config{
		Image: config.ImageConfig{Name: "toolbox", Tag: "test"},
	}
}

// stubExecShell sostituisce execShellFn con un no-op per i test.
// Ritorna una funzione di restore da chiamare in defer.
func stubExecShell() (called *bool, restore func()) {
	c := false
	orig := execShellFn
	execShellFn = func(ctx context.Context, cli client.APIClient, containerID string) error {
		c = true
		return nil
	}
	return &c, func() { execShellFn = orig }
}

// --- Tests ---

func TestContainerNameIsFixed(t *testing.T) {
	if ContainerName != "toolbox" {
		t.Fatalf("ContainerName = %q, want %q", ContainerName, "toolbox")
	}
}

func TestShellExecInRunningContainer(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "abc123",
					State: &container.State{Running: true},
				},
			}, nil
		},
	}

	err := Shell(context.Background(), mock, testConfig())
	if err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !*called {
		t.Fatal("execShellFn was not called")
	}
}

func TestShellStartsStoppedContainer(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	startCalled := false
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{
					ID:    "stopped123",
					State: &container.State{Running: false},
				},
			}, nil
		},
		startFn: func(_ context.Context, id string, _ container.StartOptions) error {
			startCalled = true
			if id != "stopped123" {
				t.Errorf("start called with id=%q, want %q", id, "stopped123")
			}
			return nil
		},
	}

	err := Shell(context.Background(), mock, testConfig())
	if err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !startCalled {
		t.Fatal("ContainerStart was not called")
	}
	if !*called {
		t.Fatal("execShellFn was not called after start")
	}
}

func TestShellCreatesNewContainer(t *testing.T) {
	called, restore := stubExecShell()
	defer restore()

	createCalled := false
	startCalled := false
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, _ *container.HostConfig) (container.CreateResponse, error) {
			createCalled = true
			return container.CreateResponse{ID: "new123"}, nil
		},
		startFn: func(_ context.Context, id string, _ container.StartOptions) error {
			startCalled = true
			if id != "new123" {
				t.Errorf("start called with id=%q, want %q", id, "new123")
			}
			return nil
		},
	}

	err := Shell(context.Background(), mock, testConfig())
	if err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if !createCalled {
		t.Fatal("ContainerCreate was not called")
	}
	if !startCalled {
		t.Fatal("ContainerStart was not called")
	}
	if !*called {
		t.Fatal("execShellFn was not called after create+start")
	}
}

func TestShellErrorOnMissingImage(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, &notFoundError{msg: "no such image"}
		},
	}

	err := Shell(context.Background(), mock, testConfig())
	if err == nil {
		t.Fatal("Shell() should have returned error for missing image")
	}
	if !strings.Contains(err.Error(), "toolbox build") {
		t.Fatalf("error should mention 'toolbox build', got: %v", err)
	}
}

func TestStopAndRemove(t *testing.T) {
	stopCalled := false
	removeCalled := false
	var capturedRemoveOpts container.RemoveOptions

	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			stopCalled = true
			return nil
		},
		removeFn: func(_ context.Context, _ string, opts container.RemoveOptions) error {
			removeCalled = true
			capturedRemoveOpts = opts
			return nil
		},
	}

	err := Stop(context.Background(), mock)
	if err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if !stopCalled {
		t.Fatal("ContainerStop was not called")
	}
	if !removeCalled {
		t.Fatal("ContainerRemove was not called")
	}
	if !capturedRemoveOpts.Force {
		t.Fatal("ContainerRemove should use Force=true")
	}
}

func TestStopContainerNotFound(t *testing.T) {
	mock := &mockClient{
		stopFn: func(_ context.Context, _ string, _ container.StopOptions) error {
			return &notFoundError{msg: "no such container"}
		},
	}

	err := Stop(context.Background(), mock)
	if err != nil {
		t.Fatalf("Stop() should not error on NotFound, got: %v", err)
	}
}

// Verify notFoundError satisfies errdefs.IsNotFound
func TestNotFoundErrorSatisfiesErrdefs(t *testing.T) {
	err := &notFoundError{msg: "test"}
	if !errdefs.IsNotFound(err) {
		t.Fatal("notFoundError should satisfy errdefs.IsNotFound")
	}
	_ = errors.New("suppress unused import")
}
