package cmd

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/herdrcore"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// fakeHerdrCore is the host's herdr-core: machines is what it lists, addErr
// what an add answers. Every call lands in log; noHC makes every call
// ErrNoHC, as on a host without hc.
type fakeHerdrCore struct {
	machines []herdrcore.Machine
	addErr   error
	noHC     bool
	log      *[]string
}

func (h fakeHerdrCore) Machines() ([]herdrcore.Machine, error) {
	if h.noHC {
		return nil, herdrcore.ErrNoHC
	}
	return h.machines, nil
}
func (h fakeHerdrCore) Add(name, via string) error {
	*h.log = append(*h.log, "add "+name+" via "+via)
	return h.addErr
}
func (h fakeHerdrCore) Update(name string) error {
	*h.log = append(*h.log, "update "+name)
	return nil
}
func (h fakeHerdrCore) Remove(name string) error {
	*h.log = append(*h.log, "rm "+name)
	return nil
}

// herdrHarness sets the herdr flag globals and cfg, a daemon with no Herdr
// Core Shell, and a host herdr-core recording its calls. It returns the plan
// the attach was handed and the herdr-core calls.
func herdrHarness(t *testing.T, core fakeHerdrCore) (**sessionplan.SessionPlan, *[]string) {
	t.Helper()
	sessionHarness(t)
	origCfg, origProfile, origCore, origReregister := cfg, herdrProfile, herdrHost, herdrReregister
	t.Cleanup(func() { cfg, herdrProfile, herdrHost, herdrReregister = origCfg, origProfile, origCore, origReregister })
	var calls []string
	core.log = &calls
	cfg, herdrProfile, herdrHost = &config.Config{Shell: "zsh"}, "", core

	newDockerClient = func() (client.APIClient, error) { return orcaDaemon{}, nil }
	var attached *sessionplan.SessionPlan
	shellFn = func(_ context.Context, _ client.APIClient, p *sessionplan.SessionPlan) (*reload.From, error) {
		attached = p
		return nil, nil
	}
	return &attached, &calls
}

func TestHerdrStartsTheHerdrCoreShellAndAddsItsMachine(t *testing.T) {
	attached, calls := herdrHarness(t, fakeHerdrCore{})
	ws := sessionWorkspace(t, "src")
	if err := runHerdr(herdrCmd, []string{ws}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	p := *attached
	if p == nil || p.ContainerName != "toolbox-herdr" || !p.Detached || p.HerdrDataVolume != "toolbox-herdr-data" {
		t.Fatalf("attached plan = %+v, want the detached toolbox-herdr with its state volume", p)
	}
	if got := sessionplan.EnvValue(p.Env, sessionplan.HostWorkspaceEnv); got != ws {
		t.Errorf("workspace = %q, want the dir argument %q", got, ws)
	}
	if want := []string{"add toolbox via docker exec -i toolbox-herdr sh -c"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want %q", *calls, want)
	}
}

func TestHerdrUnderAProfileAddsTheProfileNamedMachine(t *testing.T) {
	attached, calls := herdrHarness(t, fakeHerdrCore{})
	herdrProfile = "work"
	if err := runHerdr(herdrCmd, []string{sessionWorkspace(t, "src")}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	if (*attached).ContainerName != "toolbox-herdr-work" {
		t.Errorf("container = %q, want toolbox-herdr-work", (*attached).ContainerName)
	}
	if want := []string{"add toolbox-work via docker exec -i toolbox-herdr-work sh -c"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want %q", *calls, want)
	}
}

// A newly created Herdr Core Shell herdr-core could not add is removed, and
// herdr-core's reason is the error: no unusable detached container is left.
func TestHerdrRemovesANewShellWhoseMachineAddFailed(t *testing.T) {
	herdrHarness(t, fakeHerdrCore{addErr: errors.New("hc machine add: toolbox has an hc too old to talk to")})
	var stopped []string
	newDockerClient = func() (client.APIClient, error) { return stoppingOrcaDaemon{stopped: &stopped}, nil }

	err := runHerdr(herdrCmd, []string{sessionWorkspace(t, "src")})
	if err == nil || !strings.Contains(err.Error(), "too old to talk to") {
		t.Fatalf("runHerdr = %v, want herdr-core's reason", err)
	}
	if got := strings.Join(stopped, ", "); got != "stop toolbox-herdr, remove toolbox-herdr" {
		t.Errorf("daemon calls %q, want toolbox-herdr stopped and removed", got)
	}
}

// stoppingRunningDaemon answers a running Detached Shell and records the
// containers stopped and removed.
type stoppingRunningDaemon struct {
	runningOrcaDaemon
	stopped *[]string
}

func (d stoppingRunningDaemon) ContainerStop(_ context.Context, name string, _ client.ContainerStopOptions) (client.ContainerStopResult, error) {
	*d.stopped = append(*d.stopped, "stop "+name)
	return client.ContainerStopResult{}, nil
}

func (d stoppingRunningDaemon) ContainerRemove(_ context.Context, name string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	*d.stopped = append(*d.stopped, "remove "+name)
	return client.ContainerRemoveResult{}, nil
}

func TestHerdrStopStopsTheHerdrCoreShell(t *testing.T) {
	herdrHarness(t, fakeHerdrCore{})
	var stopped []string
	newDockerClient = func() (client.APIClient, error) { return stoppingRunningDaemon{stopped: &stopped}, nil }
	if err := runHerdrStop(herdrStopCmd, nil); err != nil {
		t.Fatalf("runHerdrStop: %v", err)
	}
	if got := strings.Join(stopped, ", "); got != "stop toolbox-herdr, remove toolbox-herdr" {
		t.Errorf("daemon calls %q, want toolbox-herdr stopped and removed", got)
	}
}

func TestHerdrStopWithoutAHerdrCoreShellIsQuiet(t *testing.T) {
	herdrHarness(t, fakeHerdrCore{})
	var err error
	stderr := captureCmdStderr(t, func() { err = runHerdrStop(herdrStopCmd, nil) })
	if err != nil {
		t.Fatalf("runHerdrStop = %v, want nil", err)
	}
	if stderr != "" {
		t.Errorf("stderr %q, want nothing", stderr)
	}
}

// toolboxMachine is the host's herdr-core already knowing the default Herdr
// Core Shell's machine.
var toolboxMachine = []herdrcore.Machine{{Name: "toolbox", Target: "docker exec -i toolbox-herdr sh -c", Via: true}}

func TestHerdrRerunOnARunningShellWithItsMachineChangesNothing(t *testing.T) {
	_, calls := herdrHarness(t, fakeHerdrCore{machines: toolboxMachine})
	ws := sessionWorkspace(t, "src")
	newDockerClient = func() (client.APIClient, error) { return runningOrcaDaemon{workspace: ws}, nil }
	if err := runHerdr(herdrCmd, []string{ws}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("herdr-core calls %q, want none", *calls)
	}
}

// A container this run created has no hc in its writable layer: the known
// machine is updated, which installs it again.
func TestHerdrNewShellWithItsMachineUpdatesIt(t *testing.T) {
	_, calls := herdrHarness(t, fakeHerdrCore{machines: toolboxMachine})
	if err := runHerdr(herdrCmd, []string{sessionWorkspace(t, "src")}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	if want := []string{"update toolbox"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want %q", *calls, want)
	}
}

func TestHerdrReregisterRemovesAndAddsTheMachine(t *testing.T) {
	_, calls := herdrHarness(t, fakeHerdrCore{machines: toolboxMachine})
	herdrReregister = true
	ws := sessionWorkspace(t, "src")
	newDockerClient = func() (client.APIClient, error) { return runningOrcaDaemon{workspace: ws}, nil }
	if err := runHerdr(herdrCmd, []string{ws}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	if want := []string{"rm toolbox", "add toolbox via docker exec -i toolbox-herdr sh -c"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want %q", *calls, want)
	}
}

// Without hc the Shell stays up and the command to register it once herdr-core
// is installed is printed, built from the planned names.
func TestHerdrWithoutHCKeepsTheShellAndPrintsTheAddCommand(t *testing.T) {
	attached, calls := herdrHarness(t, fakeHerdrCore{noHC: true})
	var stopped []string
	newDockerClient = func() (client.APIClient, error) { return stoppingOrcaDaemon{stopped: &stopped}, nil }
	herdrProfile = "work"
	var out bytes.Buffer
	herdrCmd.SetOut(&out)
	t.Cleanup(func() { herdrCmd.SetOut(nil) })

	if err := runHerdr(herdrCmd, []string{sessionWorkspace(t, "src")}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	if *attached == nil || len(stopped) != 0 {
		t.Fatalf("attached %v, daemon calls %q: want the Shell started and kept", *attached != nil, stopped)
	}
	if len(*calls) != 0 {
		t.Errorf("herdr-core calls %q, want none", *calls)
	}
	if want := "hc machine add --name toolbox-work --install --via 'docker exec -i toolbox-herdr-work sh -c'"; !strings.Contains(out.String(), want) {
		t.Errorf("output %q, want it to carry %q", out.String(), want)
	}
}
