package container

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockeridentity"
	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

const testOrcaVolume = "toolbox-orca-data-work"

// orcaPlan is an Orca Shell plan as far as the container edge reads it: the
// data volume's name and its bind.
func orcaPlan(t *testing.T) *sessionplan.SessionPlan {
	t.Helper()
	plan := testPlan(t, testWorkspace(t), nil)
	plan.ContainerName = "toolbox-orca-work"
	plan.Detached = true
	plan.DataVolume = sessionplan.DataVolume{Name: testOrcaVolume, Init: testOrcaVolume + "_init", Target: sessionplan.OrcaDataTarget, Label: "Orca data volume"}
	plan.Binds = append(plan.Binds, mountplan.Bind{Source: testOrcaVolume, Target: sessionplan.OrcaDataTarget, Mode: "rw"})
	return plan
}

// orcaVolumeMock answers a missing Orca Shell and records every container
// create by name, with the initialiser's config.
type orcaVolumeMock struct {
	created  []string
	initCfg  *container.Config
	initHost *container.HostConfig
	removed  string
}

func (r *orcaVolumeMock) client(volumeExists bool, initExit int64) *mockClient {
	m := &mockClient{
		inspectFn: func(context.Context, string) (container.InspectResponse, error) {
			return container.InspectResponse{}, &dockertest.NotFoundError{Msg: "no such container"}
		},
		imgInspFn: func(context.Context, string) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		createFn: func(_ context.Context, cfg *container.Config, hostCfg *container.HostConfig, name string) (container.CreateResponse, error) {
			r.created = append(r.created, name)
			if strings.HasSuffix(name, "_init") {
				r.initCfg, r.initHost = cfg, hostCfg
			}
			return container.CreateResponse{ID: name}, nil
		},
		startFn: func(context.Context, string, client.ContainerStartOptions) error { return nil },
		waitFn: func(context.Context, string, client.ContainerWaitOptions) (int64, error) {
			return initExit, nil
		},
		volRemoveFn: func(_ context.Context, name string, _ client.VolumeRemoveOptions) error {
			r.removed = name
			return nil
		},
	}
	if !volumeExists {
		m.volInspectFn = func(_ context.Context, name string) (client.VolumeInspectResult, error) {
			return client.VolumeInspectResult{}, &dockertest.NotFoundError{Msg: "no such volume: " + name}
		}
	}
	return m
}

// The Orca Shell's data volume is created and handed to the runtime user,
// private, before the Orca Shell itself is created: Orca binds and chmods its
// sockets in it.
func TestShellOrcaShellInitialisesItsDataVolumeBeforeCreate(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	var r orcaVolumeMock
	if _, err := Shell(context.Background(), r.client(false, 0), orcaPlan(t)); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	want := []string{"toolbox-orca-data-work_init", "toolbox-orca-work"}
	if !slices.Equal(r.created, want) {
		t.Fatalf("created %v, want %v", r.created, want)
	}
	if !sessionplan.IsToolboxContainerName(r.created[0]) {
		t.Errorf("initialiser %q is not toolbox-managed", r.created[0])
	}
	script := strings.Join(r.initCfg.Cmd, " ")
	owner := dockeridentity.Resolve(nil).UserSpec
	if !strings.Contains(script, "chown "+owner+" "+sessionplan.OrcaDataTarget) || !strings.Contains(script, "chmod 0700 "+sessionplan.OrcaDataTarget) {
		t.Errorf("initialiser Cmd = %q, want %s chowned to %s and 0700", script, sessionplan.OrcaDataTarget, owner)
	}
	if wantBind := testOrcaVolume + ":" + sessionplan.OrcaDataTarget; !slices.Equal(r.initHost.Binds, []string{wantBind}) {
		t.Errorf("initialiser Binds = %v, want [%s]", r.initHost.Binds, wantBind)
	}
}

// An existing data volume carries the pairing identity of earlier starts: it
// is neither re-created nor re-initialised.
func TestShellOrcaShellReusesItsDataVolume(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	var r orcaVolumeMock
	if _, err := Shell(context.Background(), r.client(true, 0), orcaPlan(t)); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if want := []string{"toolbox-orca-work"}; !slices.Equal(r.created, want) {
		t.Errorf("created %v, want only the Orca Shell %v", r.created, want)
	}
}

// A data volume whose init failed is removed, so the next start initialises
// it again, and no Orca Shell is created on a volume Orca cannot write.
func TestShellOrcaShellRemovesADataVolumeWhoseInitFailed(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	var r orcaVolumeMock
	if _, err := Shell(context.Background(), r.client(false, 1), orcaPlan(t)); err == nil {
		t.Fatal("Shell returned nil, want the failed init reported")
	}
	if r.removed != testOrcaVolume {
		t.Errorf("removed volume %q, want %q", r.removed, testOrcaVolume)
	}
	if slices.Contains(r.created, "toolbox-orca-work") {
		t.Error("the Orca Shell was created on a volume whose init failed")
	}
}
