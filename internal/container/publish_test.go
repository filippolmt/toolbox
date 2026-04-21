package container

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/go-connections/nat"
)

func TestParsePublishSpecs(t *testing.T) {
	cases := []struct {
		name     string
		specs    []string
		wantPort string
		wantHost string
		wantHP   string
		wantErr  bool
	}{
		{"empty", nil, "", "", "", false},
		{"port only defaults to localhost", []string{"7171"}, "7171/tcp", "127.0.0.1", "", false},
		{"host:container defaults to localhost", []string{"7171:7171"}, "7171/tcp", "127.0.0.1", "7171", false},
		{"explicit host IP preserved", []string{"0.0.0.0:7171:7171"}, "7171/tcp", "0.0.0.0", "7171", false},
		{"explicit loopback preserved", []string{"127.0.0.1:7171:7171"}, "7171/tcp", "127.0.0.1", "7171", false},
		{"udp proto", []string{"7171:7171/udp"}, "7171/udp", "127.0.0.1", "7171", false},
		{"invalid spec", []string{"not-a-port"}, "", "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exposed, bindings, err := parsePublishSpecs(tc.specs)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tc.specs) == 0 {
				if exposed != nil || bindings != nil {
					t.Fatalf("empty input must return nil maps, got exposed=%v bindings=%v", exposed, bindings)
				}
				return
			}
			port := nat.Port(tc.wantPort)
			if _, ok := exposed[port]; !ok {
				t.Fatalf("exposed missing %q, got %v", port, exposed)
			}
			binds := bindings[port]
			if len(binds) != 1 {
				t.Fatalf("want 1 binding for %q, got %d", port, len(binds))
			}
			if binds[0].HostIP != tc.wantHost {
				t.Errorf("HostIP = %q, want %q", binds[0].HostIP, tc.wantHost)
			}
			if binds[0].HostPort != tc.wantHP {
				t.Errorf("HostPort = %q, want %q", binds[0].HostPort, tc.wantHP)
			}
		})
	}
}

func TestParsePublishSpecsInvalidErrorMessage(t *testing.T) {
	_, _, err := parsePublishSpecs([]string{"bad"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--publish") {
		t.Fatalf("error should mention --publish, got: %v", err)
	}
}

// TestShellPublishPopulatesBindings verifies the happy path: --publish values
// end up as both ExposedPorts on the container config and PortBindings on the
// host config when a new container is created.
func TestShellPublishPopulatesBindings(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()

	var capturedCfg *container.Config
	var capturedHost *container.HostConfig

	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &notFoundError{msg: "no such container"}
		},
		imgInspFn: func(_ context.Context, _ string) (image.InspectResponse, error) {
			return image.InspectResponse{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig) (container.CreateResponse, error) {
			capturedCfg = cfg
			capturedHost = hostCfg
			return container.CreateResponse{ID: "new123"}, nil
		},
	}

	err := Shell(context.Background(), mock, testConfig(), testWorkspace(t), []string{"7171"})
	if err != nil {
		t.Fatalf("Shell() error: %v", err)
	}
	if capturedCfg == nil || capturedHost == nil {
		t.Fatal("ContainerCreate was not invoked")
	}
	port := nat.Port("7171/tcp")
	if _, ok := capturedCfg.ExposedPorts[port]; !ok {
		t.Errorf("ExposedPorts missing %q: %v", port, capturedCfg.ExposedPorts)
	}
	binds := capturedHost.PortBindings[port]
	if len(binds) != 1 {
		t.Fatalf("want 1 host binding, got %d", len(binds))
	}
	if binds[0].HostIP != "127.0.0.1" {
		t.Errorf("HostIP = %q, want 127.0.0.1", binds[0].HostIP)
	}
}

// TestShellPublishInvalidSpecFailsFast verifies we reject bad specs BEFORE any
// Docker call — a typo on the flag should not create / start / inspect anything.
func TestShellPublishInvalidSpecFailsFast(t *testing.T) {
	inspectCalls := 0
	mock := &mockClient{
		inspectFn: func(_ context.Context, _ string) (container.InspectResponse, error) {
			inspectCalls++
			return container.InspectResponse{}, nil
		},
	}

	err := Shell(context.Background(), mock, testConfig(), testWorkspace(t), []string{"totally-bogus"})
	if err == nil {
		t.Fatal("expected error for invalid publish spec")
	}
	if inspectCalls != 0 {
		t.Errorf("ContainerInspect should not be called on parse error, got %d calls", inspectCalls)
	}
}
