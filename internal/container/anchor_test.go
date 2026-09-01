package container

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// hasPeerSocketBind reports whether a HostConfig.Binds slice still carries the
// shared inbox-socket mount. Matching on the in-container target rather than
// the volume name keeps it in step with a renamed volume, the same way
// mountplan.WithoutPeerSocketBind does.
func hasPeerSocketBind(binds []string) bool {
	for _, b := range binds {
		if strings.Contains(b, ":"+mountplan.PeerSocketDirTarget+":") {
			return true
		}
	}
	return false
}

// peerPlan builds a plan for an opted-in session, so plan.PidMode names the
// anchor.
func peerPlan(t *testing.T, workspace string) *sessionplan.SessionPlan {
	t.Helper()
	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Cfg:       &config.Config{Shell: "zsh"},
		Workspace: workspace,
		Peer:      true,
	})
	if err != nil {
		t.Fatalf("peerPlan: %v", err)
	}
	if plan.PidMode == "" {
		t.Fatalf("peerPlan: expected a PidMode on an opted-in plan")
	}
	return plan
}

// TestShellPeerCreatesAnchor asserts the create path materialises the anchor
// before the session container and hands the daemon the shared PID namespace.
func TestShellPeerCreatesAnchor(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	var created []string
	var sessionPidMode string
	var sessionBinds []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig, name string) (container.CreateResponse, error) {
			created = append(created, name)
			if name != sessionplan.PeerAnchorContainerName {
				sessionPidMode = string(hostCfg.PidMode)
				sessionBinds = hostCfg.Binds
			}
			return container.CreateResponse{ID: name}, nil
		},
	}

	plan := peerPlan(t, testWorkspace(t))
	if err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell: %v", err)
	}

	if len(created) != 2 || created[0] != sessionplan.PeerAnchorContainerName {
		t.Fatalf("created = %v, want the anchor first then the session container", created)
	}
	if sessionPidMode != plan.PidMode {
		t.Errorf("session HostConfig.PidMode = %q, want %q", sessionPidMode, plan.PidMode)
	}
	// The namespace without the socket mount is half the mechanism, which is
	// worse than none: the session would believe it is reachable.
	if !hasPeerSocketBind(sessionBinds) {
		t.Errorf("session Binds = %v, want the %s mount", sessionBinds, mountplan.PeerSocketDirTarget)
	}
}

// TestShellPeerAnchorFailureDegrades asserts an unusable anchor warns and
// starts the shell without peer messaging, rather than blocking it — the same
// posture the repo takes for a missing proximo stack.
func TestShellPeerAnchorFailureDegrades(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	sessionCreated := false
	var sessionPidMode string
	var sessionBinds []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig, name string) (container.CreateResponse, error) {
			if name == sessionplan.PeerAnchorContainerName {
				return container.CreateResponse{}, errors.New("daemon says no")
			}
			sessionCreated = true
			sessionPidMode = string(hostCfg.PidMode)
			sessionBinds = hostCfg.Binds
			return container.CreateResponse{ID: name}, nil
		},
	}

	out := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, peerPlan(t, testWorkspace(t))); err != nil {
			t.Fatalf("Shell: %v", err)
		}
	})

	if !sessionCreated {
		t.Fatal("a failing anchor must not block the shell")
	}
	if sessionPidMode != "" {
		t.Errorf("session HostConfig.PidMode = %q, want empty after the anchor failed", sessionPidMode)
	}
	// Both halves fall together: keeping the shared socket directory while
	// sitting in a private namespace is the silent half-failure.
	if hasPeerSocketBind(sessionBinds) {
		t.Errorf("session Binds = %v, want the %s mount dropped too", sessionBinds, mountplan.PeerSocketDirTarget)
	}
	if !strings.Contains(out, "peer messaging") {
		t.Errorf("expected a peer-messaging warning on stderr, got %q", out)
	}
}

// TestShellPeerSocketVolumeFailureDegrades is the mirror of the anchor case:
// the volume half failing must drop the PID namespace as well, or the session
// joins the shared process table believing it is reachable through a socket
// directory only it can see.
func TestShellPeerSocketVolumeFailureDegrades(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	sessionCreated := false
	var sessionPidMode string
	var sessionBinds []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		volInspectFn: func(_ context.Context, name string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, &dockertest.NotFoundError{Msg: "no such volume: " + name}
		},
		volCreateFn: func(context.Context, client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
			return client.VolumeCreateResult{}, errors.New("no space left on device")
		},
		createFn: func(_ context.Context, _ *container.Config, hostCfg *container.HostConfig, name string) (container.CreateResponse, error) {
			if name != sessionplan.PeerAnchorContainerName {
				sessionCreated = true
				sessionPidMode = string(hostCfg.PidMode)
				sessionBinds = hostCfg.Binds
			}
			return container.CreateResponse{ID: name}, nil
		},
	}

	out := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, peerPlan(t, testWorkspace(t))); err != nil {
			t.Fatalf("Shell: %v", err)
		}
	})

	if !sessionCreated {
		t.Fatal("a failing socket volume must not block the shell")
	}
	if sessionPidMode != "" {
		t.Errorf("session HostConfig.PidMode = %q, want empty after the volume failed", sessionPidMode)
	}
	if hasPeerSocketBind(sessionBinds) {
		t.Errorf("session Binds = %v, want the %s mount dropped", sessionBinds, mountplan.PeerSocketDirTarget)
	}
	if !strings.Contains(out, "peer messaging") {
		t.Errorf("expected a peer-messaging warning on stderr, got %q", out)
	}
}

// TestShellPeerReusesRunningAnchor asserts a live anchor carrying the current
// entrypoint is left alone — no remove, no re-create, and no holder scan.
func TestShellPeerReusesRunningAnchor(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	var created, removed []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			if id == sessionplan.PeerAnchorContainerName {
				return container.InspectResponse{
					ID:     id,
					State:  &container.State{Running: true},
					Config: &container.Config{Entrypoint: anchorEntrypoint, Cmd: []string{"infinity"}},
				}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		removeFn: func(_ context.Context, id string, _ client.ContainerRemoveOptions) error {
			removed = append(removed, id)
			return nil
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, name string) (container.CreateResponse, error) {
			created = append(created, name)
			return container.CreateResponse{ID: name}, nil
		},
	}

	out := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, peerPlan(t, testWorkspace(t))); err != nil {
			t.Fatalf("Shell: %v", err)
		}
	})
	for _, name := range created {
		if name == sessionplan.PeerAnchorContainerName {
			t.Error("a running anchor must not be re-created")
		}
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want a current anchor left alone", removed)
	}
	// The staleness branch is silent about a healthy anchor. A peer-messaging
	// warning here means the entrypoint comparison stopped recognising its own
	// spec, which the two assertions above cannot see: a failed holder scan
	// warns and keeps the anchor, which looks exactly like reuse.
	if strings.Contains(out, peerWarnPrefix) {
		t.Errorf("expected no peer-messaging warning for a current anchor, got %q", out)
	}
}

// TestShellPeerReattachMismatchWarns covers the silent-failure case the
// container-name fold cannot reach: a container created while the anchor was
// unavailable carries no PidMode, and reconnecting to it would otherwise look
// healthy while seeing no peers.
func TestShellPeerReattachMismatchWarns(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	plan := peerPlan(t, testWorkspace(t))
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			if id == plan.ContainerName {
				return container.InspectResponse{
					ID:         id,
					State:      &container.State{Running: true},
					HostConfig: &container.HostConfig{},
				}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
	}

	out := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, plan); err != nil {
			t.Fatalf("Shell: %v", err)
		}
	})
	if !strings.Contains(out, "peer messaging") {
		t.Errorf("expected a peer-messaging mismatch warning, got %q", out)
	}
}

// TestListExcludesPeerAnchor asserts the anchor never shows up as a shell.
// It carries the toolbox- prefix so `toolbox stop --all` sweeps it up, which
// is wanted; appearing in `toolbox list` is not.
func TestListExcludesPeerAnchor(t *testing.T) {
	m := &mockClient{
		listFn: func(context.Context, client.ContainerListOptions) ([]container.Summary, error) {
			return []container.Summary{
				{Names: []string{"/" + sessionplan.PeerAnchorContainerName}, Status: "Up 2 hours"},
				{Names: []string{"/toolbox-named-infra"}, Status: "Up 1 minute"},
			}, nil
		},
	}
	items, err := List(context.Background(), m)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].Name != "toolbox-named-infra" {
		t.Errorf("List = %+v, want only the named shell", items)
	}
}

// TestShellPeerReattachMatchingPidModeIsSilent covers the false positive the
// raw string comparison used to produce: the daemon rewrites the plan's
// `container:<name>` into `container:<id>` at ContainerCreate, so every
// *correct* reattach reported a missing namespace.
func TestShellPeerReattachMatchingPidModeIsSilent(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	const anchorID = "e6b1c0f3anchor"
	plan := peerPlan(t, testWorkspace(t))
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			switch id {
			case plan.ContainerName:
				return container.InspectResponse{
					ID:         id,
					State:      &container.State{Running: true},
					HostConfig: &container.HostConfig{PidMode: container.PidMode("container:" + anchorID)},
					Mounts:     []container.MountPoint{peerSocketMountPoint()},
				}, nil
			case sessionplan.PeerAnchorContainerName:
				return container.InspectResponse{ID: anchorID, State: &container.State{Running: true}}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
	}

	out := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, plan); err != nil {
			t.Fatalf("Shell: %v", err)
		}
	})
	if strings.Contains(out, "peer messaging") {
		t.Errorf("a container already in the anchor's namespace must not warn, got %q", out)
	}
}

// TestShellReattachUnwantedNamespaceWarns is the other direction of the same
// mismatch: the plan asks for no shared namespace and the existing container
// has one, so the session would silently see the process table of every
// opted-in shell.
func TestShellReattachUnwantedNamespaceWarns(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Cfg:       &config.Config{Shell: "zsh"},
		Workspace: testWorkspace(t),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.PidMode != "" {
		t.Fatalf("PidMode = %q, want empty on a plan that did not opt in", plan.PidMode)
	}

	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			if id == plan.ContainerName {
				return container.InspectResponse{
					ID:         id,
					State:      &container.State{Running: true},
					HostConfig: &container.HostConfig{PidMode: "container:e6b1c0f3anchor"},
				}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
	}

	out := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, plan); err != nil {
			t.Fatalf("Shell: %v", err)
		}
	})
	if !strings.Contains(out, "peer messaging") {
		t.Errorf("expected a warning about the unwanted shared namespace, got %q", out)
	}
}

// TestStopAllSweepsPeerAnchor is the sibling of TestListExcludesPeerAnchor:
// the anchor is hidden from `toolbox list` but must still be swept up by
// `toolbox stop --all`, or it outlives every shell with nothing to remove it.
func TestStopAllSweepsPeerAnchor(t *testing.T) {
	removed := map[string]bool{}
	mock := &mockClient{
		listFn: func(context.Context, client.ContainerListOptions) ([]container.Summary, error) {
			return []container.Summary{
				{Names: []string{"/" + sessionplan.PeerAnchorContainerName}},
				{Names: []string{"/toolbox-named-infra"}},
			}, nil
		},
		removeFn: func(_ context.Context, name string, _ client.ContainerRemoveOptions) error {
			removed[name] = true
			return nil
		},
	}

	if err := StopAll(context.Background(), mock); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if !removed[sessionplan.PeerAnchorContainerName] {
		t.Errorf("StopAll must sweep up %s, removed = %v", sessionplan.PeerAnchorContainerName, removed)
	}
}

// TestStopByNameAcceptsContainerName asserts an argument that is already a
// full toolbox container name is stopped verbatim. It is the only way to
// target a container whose name carries a discriminator the CLI cannot
// re-derive from a shell name — a peer opt-in or a profile — and it is what
// the peer-mismatch warning tells the user to run.
func TestStopByNameAcceptsContainerName(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want string
	}{
		{arg: "infra", want: "toolbox-named-infra"},
		{arg: "toolbox-named-infra.peer", want: "toolbox-named-infra.peer"},
		{arg: "toolbox-demo-abcdef12", want: "toolbox-demo-abcdef12"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			var stopped string
			mock := &mockClient{
				inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
					return container.InspectResponse{ID: id, State: &container.State{Running: true}}, nil
				},
				stopFn: func(_ context.Context, name string, _ client.ContainerStopOptions) error {
					stopped = name
					return nil
				},
			}
			if err := StopByName(context.Background(), mock, tc.arg); err != nil {
				t.Fatalf("StopByName: %v", err)
			}
			if stopped != tc.want {
				t.Errorf("stopped %q, want %q", stopped, tc.want)
			}
		})
	}
}

// peerSocketMountPoint is the inspect-side shape of the shared socket volume on
// a healthy participating container.
func peerSocketMountPoint() container.MountPoint {
	return container.MountPoint{
		Name:        mountplan.PeerSocketVolumeName,
		Destination: mountplan.PeerSocketDirTarget,
	}
}

// TestShellPeerReattachWithoutSocketVolumeWarns covers the upgrade path the
// container-name fold cannot see: a container created before the socket
// directory became a Docker volume carries the same name and the same PID
// namespace, so it reattaches looking healthy while its inbox sockets sit in a
// directory no peer shares.
func TestShellPeerReattachWithoutSocketVolumeWarns(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	const anchorID = "e6b1c0f3anchor"
	plan := peerPlan(t, testWorkspace(t))
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			switch id {
			case plan.ContainerName:
				return container.InspectResponse{
					ID:         id,
					State:      &container.State{Running: true},
					HostConfig: &container.HostConfig{PidMode: container.PidMode("container:" + anchorID)},
					// The pre-volume host bind: right destination, no volume.
					Mounts: []container.MountPoint{{
						Source:      "/host/.toolbox/cc-socks",
						Destination: mountplan.PeerSocketDirTarget,
					}},
				}, nil
			case sessionplan.PeerAnchorContainerName:
				return container.InspectResponse{ID: anchorID, State: &container.State{Running: true}}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
	}

	out := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, plan); err != nil {
			t.Fatalf("Shell: %v", err)
		}
	})
	if !strings.Contains(out, mountplan.PeerSocketVolumeName) {
		t.Errorf("expected a warning naming %s, got %q", mountplan.PeerSocketVolumeName, out)
	}
	if !strings.Contains(out, "toolbox stop "+plan.ContainerName) {
		t.Errorf("expected the warning to name the targeted recreate, got %q", out)
	}
}

// TestShellPeerStartEnsuresSocketVolume asserts the reattach path re-creates a
// volume that was removed since the container was created — the cleanup the
// docs prescribe. The daemon would otherwise recreate it root-owned on start,
// and Claude Code answers that by falling back to a private directory without
// saying so.
func TestShellPeerStartEnsuresSocketVolume(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	const anchorID = "e6b1c0f3anchor"
	plan := peerPlan(t, testWorkspace(t))
	var createdVolume string
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			switch id {
			case plan.ContainerName:
				return container.InspectResponse{
					ID:         id,
					State:      &container.State{Running: false},
					HostConfig: &container.HostConfig{PidMode: container.PidMode("container:" + anchorID)},
					Mounts:     []container.MountPoint{peerSocketMountPoint()},
				}, nil
			case sessionplan.PeerAnchorContainerName:
				return container.InspectResponse{ID: anchorID, State: &container.State{Running: true}}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		volInspectFn: func(_ context.Context, name string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, &dockertest.NotFoundError{Msg: "no such volume: " + name}
		},
		volCreateFn: func(_ context.Context, opts client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
			createdVolume = opts.Name
			return client.VolumeCreateResult{}, nil
		},
		createFn: func(context.Context, *container.Config, *container.HostConfig, string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "init"}, nil
		},
	}

	if err := Shell(context.Background(), mock, plan); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if createdVolume != mountplan.PeerSocketVolumeName {
		t.Errorf("created volume %q, want %q re-initialised before the container starts", createdVolume, mountplan.PeerSocketVolumeName)
	}
}

// TestShellPeerAnchorReapsOrphans asserts the anchor runs tini as its PID 1.
//
// The anchor owns the PID namespace every opted-in session joins, so its PID 1
// is PID 1 for all of them — and reaping orphans is PID 1's job. A bare `sleep`
// never calls wait(), and the image's own tini is not PID 1 in a joined
// namespace (nor a subreaper: the ENTRYPOINT carries no -s), so every process
// reparented after its parent exits stays a zombie for the anchor's lifetime,
// one PID slot each, across every shell that ever shared it.
func TestShellPeerAnchorReapsOrphans(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	var anchorCfg *container.Config
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, _ *container.HostConfig, name string) (container.CreateResponse, error) {
			if name == sessionplan.PeerAnchorContainerName {
				anchorCfg = cfg
			}
			return container.CreateResponse{ID: name}, nil
		},
	}

	if err := Shell(context.Background(), mock, peerPlan(t, testWorkspace(t))); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if anchorCfg == nil {
		t.Fatal("anchor was never created")
	}
	if len(anchorCfg.Entrypoint) == 0 || anchorCfg.Entrypoint[0] != tiniPath {
		t.Errorf("anchor Entrypoint = %v, want %s first so PID 1 reaps orphans", anchorCfg.Entrypoint, tiniPath)
	}
}

// TestAnchorInitMatchesImageEntrypoint pins the anchor's init against the
// image's own, across a language boundary Go cannot type-check.
//
// tiniPath and the "-g" flag are a second spelling of the Dockerfile's
// ENTRYPOINT. Nothing links the two, so relocating tini in the image — or
// dropping -g there — would leave this package handing ContainerCreate a path
// that no longer exists: the anchor would fail to start and every opted-in
// session would degrade to no peer messaging, with the Dockerfile change
// looking innocent. Read from the embedded build context, which is the same
// Dockerfile `toolbox build` ships.
func TestAnchorInitMatchesImageEntrypoint(t *testing.T) {
	dockerfile, err := build.Assets.ReadFile("assets/Dockerfile")
	if err != nil {
		t.Fatalf("read embedded Dockerfile: %v", err)
	}

	var entrypoint string
	for _, line := range strings.Split(string(dockerfile), "\n") {
		if strings.HasPrefix(line, "ENTRYPOINT ") {
			entrypoint = line
		}
	}
	if entrypoint == "" {
		t.Fatal("embedded Dockerfile declares no ENTRYPOINT")
	}

	// The anchor overrides the init's payload (sleep, not the shell-start
	// entrypoint) but must keep the init itself, and -g with it: -g is what
	// forwards a signal to the whole process group.
	for _, want := range []string{`"` + tiniPath + `"`, `"-g"`} {
		if !strings.Contains(entrypoint, want) {
			t.Errorf("Dockerfile ENTRYPOINT = %s, want it to carry %s (anchor.go spells it separately)", entrypoint, want)
		}
	}
}

// TestShellPeerReplacesUnusedStaleAnchor asserts an anchor that predates the
// reaping init is replaced when no session holds its namespace.
//
// The connect path used to reuse any running anchor, so a namespace owned by a
// bare `sleep` survived every upgrade and kept accumulating zombies. Nothing
// holds it here, so replacing it breaks no session.
func TestShellPeerReplacesUnusedStaleAnchor(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	const anchorID = "staleanchorid"
	var removed []string
	var anchorCfg *container.Config
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			if id == sessionplan.PeerAnchorContainerName || id == anchorID {
				return container.InspectResponse{
					ID:     anchorID,
					State:  &container.State{Running: true},
					Config: &container.Config{Entrypoint: []string{"sleep"}, Cmd: []string{"infinity"}},
				}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		listFn: func(context.Context, client.ContainerListOptions) ([]container.Summary, error) {
			return []container.Summary{{
				ID:    anchorID,
				Names: []string{"/" + sessionplan.PeerAnchorContainerName},
				State: container.StateRunning,
			}}, nil
		},
		removeFn: func(_ context.Context, id string, _ client.ContainerRemoveOptions) error {
			removed = append(removed, id)
			return nil
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, _ *container.HostConfig, name string) (container.CreateResponse, error) {
			if name == sessionplan.PeerAnchorContainerName {
				anchorCfg = cfg
			}
			return container.CreateResponse{ID: name}, nil
		},
	}

	if err := Shell(context.Background(), mock, peerPlan(t, testWorkspace(t))); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if len(removed) != 1 || removed[0] != anchorID {
		t.Fatalf("removed = %v, want the stale anchor %q", removed, anchorID)
	}
	if anchorCfg == nil {
		t.Fatal("the stale anchor was removed but never replaced")
	}
	if len(anchorCfg.Entrypoint) == 0 || anchorCfg.Entrypoint[0] != tiniPath {
		t.Errorf("replacement anchor Entrypoint = %v, want %s first", anchorCfg.Entrypoint, tiniPath)
	}
}

// TestShellPeerKeepsHeldStaleAnchor asserts a stale anchor someone is standing
// on is warned about, never removed.
//
// Docker refuses none of this: `docker rm -f` on an in-use anchor succeeds and
// leaves every session that held the namespace exited with 137. The holder
// check is the only guard, so self-healing must lose to a sibling shell.
func TestShellPeerKeepsHeldStaleAnchor(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	const anchorID = "staleanchorid"
	const holderID = "siblingshellid"
	var removed []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			switch id {
			case sessionplan.PeerAnchorContainerName, anchorID:
				return container.InspectResponse{
					ID:     anchorID,
					State:  &container.State{Running: true},
					Config: &container.Config{Entrypoint: []string{"sleep"}, Cmd: []string{"infinity"}},
				}, nil
			case holderID:
				return container.InspectResponse{
					ID:         holderID,
					State:      &container.State{Running: true},
					HostConfig: &container.HostConfig{PidMode: container.PidMode("container:" + anchorID)},
				}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		listFn: func(context.Context, client.ContainerListOptions) ([]container.Summary, error) {
			return []container.Summary{
				{ID: anchorID, Names: []string{"/" + sessionplan.PeerAnchorContainerName}, State: container.StateRunning},
				{ID: holderID, Names: []string{"/toolbox-named-infra.peer"}, State: container.StateRunning},
			}, nil
		},
		removeFn: func(_ context.Context, id string, _ client.ContainerRemoveOptions) error {
			removed = append(removed, id)
			return nil
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, name string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: name}, nil
		},
	}

	out := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, peerPlan(t, testWorkspace(t))); err != nil {
			t.Fatalf("Shell: %v", err)
		}
	})
	if len(removed) != 0 {
		t.Errorf("removed = %v, want the held anchor left alone", removed)
	}
	if !strings.Contains(out, "peer messaging") || !strings.Contains(out, "zombies") {
		t.Errorf("expected a warning naming the stale anchor, got %q", out)
	}
}

// TestShellPeerReplacesStoppedStaleAnchor asserts a stopped stale anchor is
// replaced without consulting the holder list.
//
// Nothing can be standing on it: a container joined to an anchor's namespace
// dies when the anchor does. Skipping the scan is not just an optimisation —
// anchorHeld answers "held" on a failed list, which would otherwise strand a
// stopped anchor no session could possibly be using.
func TestShellPeerReplacesStoppedStaleAnchor(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	const anchorID = "stoppedanchorid"
	var removed []string
	var anchorCfg *container.Config
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			if id == sessionplan.PeerAnchorContainerName || id == anchorID {
				return container.InspectResponse{
					ID:     anchorID,
					State:  &container.State{Running: false},
					Config: &container.Config{Entrypoint: []string{"sleep"}, Cmd: []string{"infinity"}},
				}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		removeFn: func(_ context.Context, id string, _ client.ContainerRemoveOptions) error {
			removed = append(removed, id)
			return nil
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, _ *container.HostConfig, name string) (container.CreateResponse, error) {
			if name == sessionplan.PeerAnchorContainerName {
				anchorCfg = cfg
			}
			return container.CreateResponse{ID: name}, nil
		},
	}

	// listFn is deliberately unset: reaching ContainerList here fails the test
	// with "not mocked" rather than silently taking the warn-and-keep branch.
	if err := Shell(context.Background(), mock, peerPlan(t, testWorkspace(t))); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if len(removed) != 1 || removed[0] != anchorID {
		t.Fatalf("removed = %v, want the stopped stale anchor %q", removed, anchorID)
	}
	if anchorCfg == nil || len(anchorCfg.Entrypoint) == 0 || anchorCfg.Entrypoint[0] != tiniPath {
		t.Errorf("replacement anchor Config = %+v, want an entrypoint starting with %s", anchorCfg, tiniPath)
	}
}

// TestShellPeerKeepsStaleAnchorWhenHoldersUnknown asserts an unreadable
// container list keeps the stale anchor rather than replacing it.
//
// anchorHeld fails closed on purpose: guessing "free" from a daemon that would
// not answer costs a live session, guessing "held" costs one more shell start
// with the old anchor. This is the branch that must never be relaxed into an
// optimistic default.
func TestShellPeerKeepsStaleAnchorWhenHoldersUnknown(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	t.Setenv("HOME", t.TempDir())

	const anchorID = "staleanchorid"
	var removed []string
	mock := &mockClient{
		inspectFn: func(_ context.Context, id string) (container.InspectResponse, error) {
			if id == sessionplan.PeerAnchorContainerName || id == anchorID {
				return container.InspectResponse{
					ID:     anchorID,
					State:  &container.State{Running: true},
					Config: &container.Config{Entrypoint: []string{"sleep"}, Cmd: []string{"infinity"}},
				}, nil
			}
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container: " + id}
		},
		listFn: func(context.Context, client.ContainerListOptions) ([]container.Summary, error) {
			return nil, errors.New("daemon says no")
		},
		removeFn: func(_ context.Context, id string, _ client.ContainerRemoveOptions) error {
			removed = append(removed, id)
			return nil
		},
		imgInspFn: func(_ context.Context, _ string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, name string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: name}, nil
		},
	}

	out := captureStderr(t, func() {
		if err := Shell(context.Background(), mock, peerPlan(t, testWorkspace(t))); err != nil {
			t.Fatalf("Shell: %v", err)
		}
	})
	if len(removed) != 0 {
		t.Errorf("removed = %v, want the anchor kept while its holders are unknown", removed)
	}
	if !strings.Contains(out, peerWarnPrefix) {
		t.Errorf("expected a peer-messaging warning, got %q", out)
	}
}
