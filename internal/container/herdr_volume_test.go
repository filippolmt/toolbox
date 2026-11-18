package container

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// The Herdr Core Shell's state volume goes through the same ensure/init as the
// Orca data volume, before the Shell is created: herdr-core binds and chmods
// its sockets in it.
func TestShellHerdrCoreShellInitialisesItsStateVolumeBeforeCreate(t *testing.T) {
	_, restore := stubExecShell()
	defer restore()
	plan := testPlan(t, testWorkspace(t), nil)
	plan.ContainerName = "toolbox-herdr"
	plan.Detached = true
	plan.DataVolume = sessionplan.DataVolume{Name: "toolbox-herdr-data", Init: "toolbox-herdr-data_init", Target: sessionplan.HerdrCoreStateTarget, Label: "herdr-core state volume"}
	plan.Binds = append(plan.Binds, mountplan.Bind{Source: "toolbox-herdr-data", Target: sessionplan.HerdrCoreStateTarget, Mode: "rw"})

	var r orcaVolumeMock
	if _, err := Shell(context.Background(), r.client(false, 0), plan); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if want := []string{"toolbox-herdr-data_init", "toolbox-herdr"}; !slices.Equal(r.created, want) {
		t.Fatalf("created %v, want %v", r.created, want)
	}
	if script := strings.Join(r.initCfg.Cmd, " "); !strings.Contains(script, "chmod 0700 "+sessionplan.HerdrCoreStateTarget) {
		t.Errorf("initialiser Cmd = %q, want %s made private", script, sessionplan.HerdrCoreStateTarget)
	}
}
