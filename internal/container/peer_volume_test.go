package container

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockeridentity"
	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// testImage is the image reference the volume initialiser is expected to reuse.
var testImage = sessionplan.Image{Ref: "ghcr.io/filippolmt/toolbox:latest"}

// TestEnsurePeerSocketVolumeReusesExisting asserts an existing volume costs
// nothing: no create, and above all no initialiser container, since that would
// be a container start on every shell.
func TestEnsurePeerSocketVolumeReusesExisting(t *testing.T) {
	var created []string
	mock := &mockClient{
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, name string) (container.CreateResponse, error) {
			created = append(created, name)
			return container.CreateResponse{ID: "init"}, nil
		},
		volCreateFn: func(_ context.Context, opts client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
			t.Errorf("VolumeCreate called for the already-present %s", opts.Name)
			return client.VolumeCreateResult{}, nil
		},
	}

	if err := ensurePeerSocketVolume(context.Background(), mock, testImage); err != nil {
		t.Fatalf("ensurePeerSocketVolume: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("created %v, want no initialiser container for an existing volume", created)
	}
}

// TestEnsurePeerSocketVolumeInitialisesOwnership asserts a missing volume is
// created and handed to a root container that chowns it to the host UID/GID
// and tightens it to 0700. Both halves are load-bearing: the session container
// runs unprivileged so it cannot chown the root-owned volume itself, and
// Claude Code silently falls back to a private /tmp/cc-socks-<uid> on anything
// looser than 0700.
func TestEnsurePeerSocketVolumeInitialisesOwnership(t *testing.T) {
	var gotCfg *container.Config
	var gotHostCfg *container.HostConfig
	var createdVolume string
	mock := &mockClient{
		volInspectFn: func(_ context.Context, name string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, &dockertest.NotFoundError{Msg: "no such volume: " + name}
		},
		volCreateFn: func(_ context.Context, opts client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
			createdVolume = opts.Name
			return client.VolumeCreateResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig, _ string) (container.CreateResponse, error) {
			gotCfg, gotHostCfg = cfg, hostCfg
			return container.CreateResponse{ID: "init"}, nil
		},
	}

	if err := ensurePeerSocketVolume(context.Background(), mock, testImage); err != nil {
		t.Fatalf("ensurePeerSocketVolume: %v", err)
	}

	if createdVolume != mountplan.PeerSocketVolumeName {
		t.Errorf("created volume %q, want %q", createdVolume, mountplan.PeerSocketVolumeName)
	}
	if gotCfg == nil || gotHostCfg == nil {
		t.Fatal("no initialiser container was created for the new volume")
	}
	if gotCfg.User != "0:0" {
		t.Errorf("initialiser User = %q, want 0:0 — an unprivileged one cannot chown the volume", gotCfg.User)
	}
	if gotCfg.Image != testImage.Ref {
		t.Errorf("initialiser Image = %q, want the runtime image %q", gotCfg.Image, testImage.Ref)
	}
	script := strings.Join(gotCfg.Cmd, " ")
	if owner := dockeridentity.Resolve(nil).UserSpec; !strings.Contains(script, "chown "+owner+" "+mountplan.PeerSocketDirTarget) {
		t.Errorf("initialiser Cmd = %q, want it to chown %s to %s", script, mountplan.PeerSocketDirTarget, owner)
	}
	if !strings.Contains(script, "chmod 0700 "+mountplan.PeerSocketDirTarget) {
		t.Errorf("initialiser Cmd = %q, want it to chmod %s to 0700", script, mountplan.PeerSocketDirTarget)
	}
	wantBind := mountplan.PeerSocketVolumeName + ":" + mountplan.PeerSocketDirTarget
	if len(gotHostCfg.Binds) != 1 || gotHostCfg.Binds[0] != wantBind {
		t.Errorf("initialiser Binds = %v, want exactly [%s]", gotHostCfg.Binds, wantBind)
	}
}

// TestEnsurePeerSocketVolumeRemovesVolumeOnFailedInit covers the invariant that
// makes the create-time-only init safe: a volume whose init failed must not
// survive. Left behind, it would satisfy the VolumeInspect fast path on every
// later shell, so the init would never run again and each session would fail
// its socket bind on a root-owned directory instead — silently.
func TestEnsurePeerSocketVolumeRemovesVolumeOnFailedInit(t *testing.T) {
	var removed string
	mock := &mockClient{
		volInspectFn: func(_ context.Context, name string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, &dockertest.NotFoundError{Msg: "no such volume: " + name}
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "init"}, nil
		},
		waitFn: func(_ context.Context, _ string, _ client.ContainerWaitOptions) (int64, error) {
			return 1, nil
		},
		volRemoveFn: func(_ context.Context, name string, _ client.VolumeRemoveOptions) error {
			removed = name
			return nil
		},
	}

	err := ensurePeerSocketVolume(context.Background(), mock, testImage)
	if err == nil {
		t.Fatal("ensurePeerSocketVolume returned nil, want the non-zero init exit reported")
	}
	if removed != mountplan.PeerSocketVolumeName {
		t.Errorf("removed volume %q, want the failed %q", removed, mountplan.PeerSocketVolumeName)
	}
}

// TestEnsurePeerSocketVolumePropagatesWaitError asserts a daemon-side wait
// failure is reported rather than mistaken for a clean init.
func TestEnsurePeerSocketVolumePropagatesWaitError(t *testing.T) {
	mock := &mockClient{
		volInspectFn: func(_ context.Context, name string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, &dockertest.NotFoundError{Msg: "no such volume: " + name}
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, _ string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "init"}, nil
		},
		waitFn: func(_ context.Context, _ string, _ client.ContainerWaitOptions) (int64, error) {
			return 0, errors.New("daemon went away")
		},
	}

	err := ensurePeerSocketVolume(context.Background(), mock, testImage)
	if err == nil || !strings.Contains(err.Error(), "daemon went away") {
		t.Errorf("err = %v, want the wait failure surfaced", err)
	}
}

// TestDropPeerSocketBind asserts the degrade path removes exactly the shared
// socket mount and leaves the rest of the bind set intact. A session that
// mounts the shared directory without joining the anchor's PID namespace looks
// healthy and reaches nobody, so the two have to fall together.
func TestDropPeerSocketBind(t *testing.T) {
	workspace := mountplan.Bind{Source: "/host/repo", Target: "/workspace", Mode: "rw"}
	claude := mountplan.Bind{Source: "/host/.toolbox/claude", Target: "/home/toolbox/.claude", Mode: "rw"}
	socks := mountplan.Bind{
		Source: mountplan.PeerSocketVolumeName,
		Target: mountplan.PeerSocketDirTarget,
		Mode:   "rw",
	}

	got := dropPeerSocketBind([]mountplan.Bind{workspace, socks, claude})

	want := []mountplan.Bind{workspace, claude}
	if len(got) != len(want) {
		t.Fatalf("kept %d binds (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bind %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDropPeerSocketBindWithoutOptIn asserts a non-participating bind set
// passes through untouched — the ordinary case, since the mount is only ever
// appended for an opted-in session.
func TestDropPeerSocketBindWithoutOptIn(t *testing.T) {
	binds := []mountplan.Bind{{Source: "/host/repo", Target: "/workspace", Mode: "rw"}}

	got := dropPeerSocketBind(binds)

	if len(got) != len(binds) || got[0] != binds[0] {
		t.Errorf("dropPeerSocketBind(%v) = %v, want it unchanged", binds, got)
	}
}

// TestEnsurePeerSocketVolumeFailsOnInspectError asserts a VolumeInspect error
// that is not "not found" is reported instead of read as an absent volume.
// Treating every error as absence sends the code into VolumeCreate, which
// returns the *existing* volume, and a failing init would then force-remove a
// volume live sessions are binding sockets in.
func TestEnsurePeerSocketVolumeFailsOnInspectError(t *testing.T) {
	mock := &mockClient{
		volInspectFn: func(context.Context, string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, errors.New("daemon went away")
		},
		volCreateFn: func(_ context.Context, opts client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
			t.Errorf("VolumeCreate called for %s after an inconclusive inspect", opts.Name)
			return client.VolumeCreateResult{}, nil
		},
	}

	err := ensurePeerSocketVolume(context.Background(), mock, testImage)
	if err == nil || !strings.Contains(err.Error(), "daemon went away") {
		t.Errorf("err = %v, want the inspect failure surfaced", err)
	}
}

// TestEnsurePeerSocketVolumeReportsFailedCleanup asserts that when the rollback
// of a failed init also fails, the user hears about it. The leftover volume is
// root-owned and satisfies the inspect fast path forever, so silence here is
// the one outcome that makes peer messaging permanently and quietly dead.
func TestEnsurePeerSocketVolumeReportsFailedCleanup(t *testing.T) {
	mock := &mockClient{
		volInspectFn: func(_ context.Context, name string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, &dockertest.NotFoundError{Msg: "no such volume: " + name}
		},
		createFn: func(context.Context, *container.Config, *container.HostConfig, string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "init"}, nil
		},
		waitFn: func(context.Context, string, client.ContainerWaitOptions) (int64, error) {
			return 1, nil
		},
		volRemoveFn: func(context.Context, string, client.VolumeRemoveOptions) error {
			return errors.New("volume is in use")
		},
	}

	err := ensurePeerSocketVolume(context.Background(), mock, testImage)
	if err == nil {
		t.Fatal("ensurePeerSocketVolume returned nil, want the failed init reported")
	}
	if !strings.Contains(err.Error(), "volume is in use") ||
		!strings.Contains(err.Error(), mountplan.PeerSocketVolumeName) {
		t.Errorf("err = %v, want it to name the volume left behind and why it stayed", err)
	}
}

// TestEnsurePeerSocketVolumeNamesInitContainer asserts the initialiser carries
// the toolbox- prefix. One that outlives its defer — a hard daemon restart —
// keeps the volume in use, and without the prefix neither `toolbox list` nor
// `toolbox stop --all` can reach it.
func TestEnsurePeerSocketVolumeNamesInitContainer(t *testing.T) {
	var name string
	mock := &mockClient{
		volInspectFn: func(_ context.Context, v string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, &dockertest.NotFoundError{Msg: "no such volume: " + v}
		},
		createFn: func(_ context.Context, _ *container.Config, _ *container.HostConfig, n string) (container.CreateResponse, error) {
			name = n
			return container.CreateResponse{ID: "init"}, nil
		},
	}

	if err := ensurePeerSocketVolume(context.Background(), mock, testImage); err != nil {
		t.Fatalf("ensurePeerSocketVolume: %v", err)
	}
	if !sessionplan.IsToolboxContainerName(name) {
		t.Errorf("initialiser container name = %q, want a toolbox-managed one", name)
	}
}

// TestEnsurePeerSocketVolumeRemovesInitContainerAfterCancel asserts the
// throwaway initialiser is reaped even when the shell was interrupted. Passing
// the cancelled context straight through would leave the container alive, and
// with it the volume in use — so the rollback above could not remove it either.
func TestEnsurePeerSocketVolumeRemovesInitContainerAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var removed string
	mock := &mockClient{
		volInspectFn: func(_ context.Context, v string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, &dockertest.NotFoundError{Msg: "no such volume: " + v}
		},
		createFn: func(context.Context, *container.Config, *container.HostConfig, string) (container.CreateResponse, error) {
			return container.CreateResponse{ID: "init"}, nil
		},
		waitFn: func(context.Context, string, client.ContainerWaitOptions) (int64, error) {
			return 0, errors.New("interrupted")
		},
		removeFn: func(rmCtx context.Context, id string, _ client.ContainerRemoveOptions) error {
			if rmCtx.Err() != nil {
				return rmCtx.Err()
			}
			removed = id
			return nil
		},
	}

	if err := ensurePeerSocketVolume(ctx, mock, testImage); err == nil {
		t.Fatal("ensurePeerSocketVolume returned nil, want the interrupted wait reported")
	}
	if removed != "init" {
		t.Errorf("removed %q, want the initialiser reaped despite the cancelled context", removed)
	}
}
