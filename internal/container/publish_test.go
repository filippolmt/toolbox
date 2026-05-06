package container

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/go-connections/nat"
)

func TestParsePublishSpecs(t *testing.T) {
	cases := []struct {
		name       string
		specs      []string
		wantPort   string
		wantHost   string
		wantHP     string
		wantErrSub string // non-empty => want error containing this substring
	}{
		{name: "empty", specs: nil},
		{name: "port only defaults to localhost", specs: []string{"7171"}, wantPort: "7171/tcp", wantHost: "127.0.0.1"},
		{name: "host:container defaults to localhost", specs: []string{"7171:7171"}, wantPort: "7171/tcp", wantHost: "127.0.0.1", wantHP: "7171"},
		{name: "explicit host IP preserved", specs: []string{"0.0.0.0:7171:7171"}, wantPort: "7171/tcp", wantHost: "0.0.0.0", wantHP: "7171"},
		{name: "explicit loopback preserved", specs: []string{"127.0.0.1:7171:7171"}, wantPort: "7171/tcp", wantHost: "127.0.0.1", wantHP: "7171"},
		{name: "udp proto", specs: []string{"7171:7171/udp"}, wantPort: "7171/udp", wantHost: "127.0.0.1", wantHP: "7171"},
		{name: "invalid spec", specs: []string{"not-a-port"}, wantErrSub: "--publish"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exposed, bindings, err := parsePublishSpecs(tc.specs)
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error should contain %q, got: %v", tc.wantErrSub, err)
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

func TestFormatPublishMismatch(t *testing.T) {
	wanted := nat.PortMap{
		"7171/tcp": nil,
		"8080/tcp": nil,
	}

	t.Run("empty when nothing missing", func(t *testing.T) {
		inspect := container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{HostConfig: &container.HostConfig{
				PortBindings: nat.PortMap{"7171/tcp": nil, "8080/tcp": nil},
			}},
		}
		if got := formatPublishMismatch(inspect, wanted); got != "" {
			t.Errorf("expected empty string when fully bound, got %q", got)
		}
	})

	t.Run("structured message lists wanted, actual, missing", func(t *testing.T) {
		inspect := container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{HostConfig: &container.HostConfig{
				PortBindings: nat.PortMap{"7171/tcp": nil},
			}},
		}
		got := formatPublishMismatch(inspect, wanted)
		// Order is sorted, so the expected substrings are deterministic.
		for _, sub := range []string{
			"wanted [7171/tcp, 8080/tcp]",
			"container has [7171/tcp]",
			"missing [8080/tcp]",
			"toolbox stop",
		} {
			if !strings.Contains(got, sub) {
				t.Errorf("message missing %q\n  full: %s", sub, got)
			}
		}
	})

	t.Run("empty PortBindings reports actual=none", func(t *testing.T) {
		// HostConfig is non-nil (so missingPublishPorts proceeds) but the
		// container has zero published ports — the user sees "actual=[none]"
		// rather than an empty pair of brackets.
		inspect := container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{HostConfig: &container.HostConfig{
				PortBindings: nat.PortMap{},
			}},
		}
		got := formatPublishMismatch(inspect, wanted)
		if !strings.Contains(got, "container has [none]") {
			t.Errorf("expected actual=none with empty PortBindings, got: %s", got)
		}
	})
}

func TestMissingPublishPorts(t *testing.T) {
	wanted := nat.PortMap{
		"7171/tcp": nil,
		"8080/tcp": nil,
	}

	t.Run("nil HostConfig returns empty", func(t *testing.T) {
		got := missingPublishPorts(container.InspectResponse{}, wanted)
		if len(got) != 0 {
			t.Fatalf("want empty, got %v", got)
		}
	})

	t.Run("all ports already bound returns empty", func(t *testing.T) {
		inspect := container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{HostConfig: &container.HostConfig{
				PortBindings: nat.PortMap{"7171/tcp": nil, "8080/tcp": nil},
			}},
		}
		if got := missingPublishPorts(inspect, wanted); len(got) != 0 {
			t.Fatalf("want empty, got %v", got)
		}
	})

	t.Run("partial overlap reports only missing", func(t *testing.T) {
		inspect := container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{HostConfig: &container.HostConfig{
				PortBindings: nat.PortMap{"7171/tcp": nil},
			}},
		}
		got := missingPublishPorts(inspect, wanted)
		sort.Strings(got)
		if len(got) != 1 || got[0] != "8080/tcp" {
			t.Fatalf("want [8080/tcp], got %v", got)
		}
	})
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
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
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
