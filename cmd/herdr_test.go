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
	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/herdrcore"
	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// fakeHerdrCore is the host's herdr-core: machines is what it lists, addErr
// what an add answers. Every call lands in log ("ls" for the listing); noHC
// makes every call ErrNoHC, as on a host without hc.
type fakeHerdrCore struct {
	machines []herdrcore.Machine
	addErr   error
	noHC     bool
	log      *[]string
}

func (h fakeHerdrCore) Machines() ([]herdrcore.Machine, error) {
	*h.log = append(*h.log, "ls")
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
	if p == nil || p.ContainerName != "toolbox-herdr" || !p.Detached || p.DataVolume.Name != "toolbox-herdr-data" {
		t.Fatalf("attached plan = %+v, want the detached toolbox-herdr with its state volume", p)
	}
	if got := sessionplan.EnvValue(p.Env, sessionplan.HostWorkspaceEnv); got != ws {
		t.Errorf("workspace = %q, want the dir argument %q", got, ws)
	}
	if want := []string{"ls", "add toolbox via docker exec -i toolbox-herdr sh -c"}; !slices.Equal(*calls, want) {
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
	if want := []string{"ls", "add toolbox-work via docker exec -i toolbox-herdr-work sh -c"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want %q", *calls, want)
	}
}

// A newly created Herdr Core Shell herdr-core could not add is removed, and
// herdr-core's reason is the error: no unusable detached container is left.
func TestHerdrRemovesANewShellWhoseMachineAddFailed(t *testing.T) {
	herdrHarness(t, fakeHerdrCore{addErr: errors.New("hc machine add: toolbox has an hc too old to talk to")})
	var stopped []string
	newDockerClient = func() (client.APIClient, error) {
		return stoppingDaemon{APIClient: orcaDaemon{}, stopped: &stopped}, nil
	}

	err := runHerdr(herdrCmd, []string{sessionWorkspace(t, "src")})
	if err == nil || !strings.Contains(err.Error(), "too old to talk to") {
		t.Fatalf("runHerdr = %v, want herdr-core's reason", err)
	}
	if got := strings.Join(stopped, ", "); got != "stop toolbox-herdr, remove toolbox-herdr" {
		t.Errorf("daemon calls %q, want toolbox-herdr stopped and removed", got)
	}
}

func TestHerdrStopStopsTheHerdrCoreShell(t *testing.T) {
	herdrHarness(t, fakeHerdrCore{})
	var stopped []string
	newDockerClient = func() (client.APIClient, error) {
		return stoppingDaemon{APIClient: runningOrcaDaemon{}, stopped: &stopped}, nil
	}
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

// hcProbeDaemon is a running Herdr Core Shell that answers the probe for
// herdr-core's binary: present unless missing. Each probed command lands in
// probes.
type hcProbeDaemon struct {
	runningOrcaDaemon
	missing bool
	probes  *[]string
}

func (d hcProbeDaemon) ExecCreate(_ context.Context, name string, o client.ExecCreateOptions) (client.ExecCreateResult, error) {
	*d.probes = append(*d.probes, name+": "+strings.Join(o.Cmd, " "))
	return client.ExecCreateResult{ID: "probe"}, nil
}

func (hcProbeDaemon) ExecStart(context.Context, string, client.ExecStartOptions) (client.ExecStartResult, error) {
	return client.ExecStartResult{}, nil
}

func (d hcProbeDaemon) ExecInspect(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
	if d.missing {
		return client.ExecInspectResult{ExitCode: 1}, nil
	}
	return client.ExecInspectResult{}, nil
}

func TestHerdrRerunOnARunningShellWithItsMachineChangesNothing(t *testing.T) {
	_, calls := herdrHarness(t, fakeHerdrCore{machines: toolboxMachine})
	ws := sessionWorkspace(t, "src")
	var probes []string
	newDockerClient = func() (client.APIClient, error) {
		return hcProbeDaemon{runningOrcaDaemon: runningOrcaDaemon{workspace: ws}, probes: &probes}, nil
	}
	if err := runHerdr(herdrCmd, []string{ws}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	if want := []string{"ls"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want only the listing: no add, update or rm", *calls)
	}
	if len(probes) != 1 || !strings.Contains(probes[0], "toolbox-herdr: ") || !strings.Contains(probes[0], ".local/bin/herdr-core") {
		t.Errorf("probes %q, want one for herdr-core's binary in toolbox-herdr", probes)
	}
}

// A Session Reload or start-up refresh recreates the Herdr Core Shell without
// toolbox herdr: a running Shell whose layer lost herdr-core's binary has its
// known machine updated, which installs it again.
func TestHerdrRunningShellWithoutHerdrCoreBinaryUpdatesItsMachine(t *testing.T) {
	_, calls := herdrHarness(t, fakeHerdrCore{machines: toolboxMachine})
	ws := sessionWorkspace(t, "src")
	var probes []string
	newDockerClient = func() (client.APIClient, error) {
		return hcProbeDaemon{runningOrcaDaemon: runningOrcaDaemon{workspace: ws}, missing: true, probes: &probes}, nil
	}
	if err := runHerdr(herdrCmd, []string{ws}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	if want := []string{"ls", "update toolbox"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want %q", *calls, want)
	}
}

// A container this run created has no hc in its writable layer: the known
// machine is updated, which installs it again.
func TestHerdrNewShellWithItsMachineUpdatesIt(t *testing.T) {
	_, calls := herdrHarness(t, fakeHerdrCore{machines: toolboxMachine})
	if err := runHerdr(herdrCmd, []string{sessionWorkspace(t, "src")}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	if want := []string{"ls", "update toolbox"}; !slices.Equal(*calls, want) {
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
	if want := []string{"ls", "rm toolbox", "add toolbox via docker exec -i toolbox-herdr sh -c"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want %q", *calls, want)
	}
}

// herdr-core lists a --via machine's target as the --via command verbatim: a
// machine called toolbox that reaches anything else is not this Shell's, and is
// removed and added again on the Shell's own connection.
func TestHerdrReplacesASameNameMachineOnAnotherConnection(t *testing.T) {
	other := []herdrcore.Machine{{Name: "toolbox", Target: "ssh devbox", Via: false}}
	_, calls := herdrHarness(t, fakeHerdrCore{machines: other})
	ws := sessionWorkspace(t, "src")
	newDockerClient = func() (client.APIClient, error) { return runningOrcaDaemon{workspace: ws}, nil }
	if err := runHerdr(herdrCmd, []string{ws}); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	if want := []string{"ls", "rm toolbox", "add toolbox via docker exec -i toolbox-herdr sh -c"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want %q", *calls, want)
	}
}

// Without hc the Shell stays up and the command to register it once herdr-core
// is installed is printed, built from the planned names.
func TestHerdrWithoutHCKeepsTheShellAndPrintsTheAddCommand(t *testing.T) {
	attached, calls := herdrHarness(t, fakeHerdrCore{noHC: true})
	var stopped []string
	newDockerClient = func() (client.APIClient, error) {
		return stoppingDaemon{APIClient: orcaDaemon{}, stopped: &stopped}, nil
	}
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
	// The listing is how the adapter reports no hc: nothing else is tried.
	if want := []string{"ls"}; !slices.Equal(*calls, want) {
		t.Errorf("herdr-core calls %q, want only the listing that found no hc: no add, update or rm", *calls)
	}
	if want := "hc machine add --name toolbox-work --install --via 'docker exec -i toolbox-herdr-work sh -c'"; !strings.Contains(out.String(), want) {
		t.Errorf("output %q, want it to carry %q", out.String(), want)
	}
}

// joinHerdr runs `toolbox herdr args` against a running Herdr Core Shell on
// running, through the real attach, and returns what it printed.
func joinHerdr(t *testing.T, running string, args []string) string {
	t.Helper()
	newDockerClient = func() (client.APIClient, error) { return runningOrcaDaemon{workspace: running}, nil }
	shellFn = container.Shell
	var out bytes.Buffer
	herdrCmd.SetOut(&out)
	t.Cleanup(func() { herdrCmd.SetOut(nil) })
	if err := runHerdr(herdrCmd, args); err != nil {
		t.Fatalf("runHerdr: %v", err)
	}
	return out.String()
}

// With no dir and no herdr_workspace, `toolbox herdr` joins the running Herdr
// Core Shell on its own workspace: no create, and the report names it.
func TestHerdrJoinsARunningShellOnItsWorkspace(t *testing.T) {
	herdrHarness(t, fakeHerdrCore{})
	ws := sessionWorkspace(t, "src")
	out := joinHerdr(t, ws, nil)
	if want := "The Herdr Core Shell toolbox-herdr is already running on " + ws; !strings.Contains(out, want) {
		t.Errorf("output %q lacks %q", out, want)
	}
	if strings.Contains(out, "stop first") {
		t.Errorf("output %q reports a mismatch", out)
	}
}

// A join keeps the running Shell's workspace: a dir argument or
// herdr_workspace that differs from it is reported, not silently ignored.
func TestHerdrJoinReportsAWorkspaceMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, arg, key string
		mismatch       bool
	}{
		{name: "same dir argument", arg: "src"},
		{name: "other dir argument", arg: "other", mismatch: true},
		{name: "other herdr_workspace", key: "other", mismatch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			herdrHarness(t, fakeHerdrCore{})
			running, other := sessionWorkspace(t, "src"), sessionWorkspace(t, "other")
			var args []string
			if tc.arg != "" {
				args = []string{sessionWorkspace(t, tc.arg)}
			}
			if tc.key != "" {
				cfg.HerdrWorkspace = sessionWorkspace(t, tc.key)
			}
			out := joinHerdr(t, running, args)
			note := "it keeps " + running + "; to mount " + other + ", toolbox herdr stop first"
			if got := strings.Contains(out, note); got != tc.mismatch {
				t.Errorf("output %q: workspace-mismatch note = %v, want %v", out, got, tc.mismatch)
			}
		})
	}
}

// The terminal is planned as the Herdr Core Shell it enters, on its
// workspace, not a detached start; a Session Reload from it re-enters
// `toolbox herdr shell` with the profile.
func TestHerdrShellReentersTheRunningShell(t *testing.T) {
	attached, calls := herdrHarness(t, fakeHerdrCore{})
	ws := sessionWorkspace(t, "src")
	newDockerClient = func() (client.APIClient, error) { return runningOrcaDaemon{workspace: ws}, nil }
	shellFn = func(_ context.Context, _ client.APIClient, p *sessionplan.SessionPlan) (*reload.From, error) {
		*attached = p
		return &reload.From{Container: p.ContainerName}, nil
	}
	exec := stubExecSelf(t, "/usr/local/bin/toolbox")
	herdrShellCmd.InheritedFlags() // merges the persistent --profile, as cobra's parse does
	if err := herdrShellCmd.Flags().Set("profile", "work"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { herdrShellCmd.Flags().Lookup("profile").Changed = false })

	_ = runHerdrShell(herdrShellCmd, nil) // the exec stub reports the reload as an error

	p := *attached
	if p == nil || p.ContainerName != "toolbox-herdr-work" || p.Detached || p.DataVolume.Name != "toolbox-herdr-data-work" {
		t.Fatalf("attached plan = %+v, want an attached toolbox-herdr-work with its state volume", p)
	}
	if got := sessionplan.EnvValue(p.Env, sessionplan.HostWorkspaceEnv); got != ws {
		t.Errorf("workspace = %q, want the running Shell's %q", got, ws)
	}
	if want := []string{"/usr/local/bin/toolbox", "herdr", "shell", "--profile", "work"}; !slices.Equal(exec.argv, want) {
		t.Errorf("reload argv = %q, want %q", exec.argv, want)
	}
	if len(*calls) != 0 {
		t.Errorf("herdr-core calls %q, want none", *calls)
	}
}

// Without a running Herdr Core Shell, `toolbox herdr shell` points at
// `toolbox herdr` and creates nothing.
func TestHerdrShellWithoutAShellPointsAtToolboxHerdr(t *testing.T) {
	attached, _ := herdrHarness(t, fakeHerdrCore{})
	err := runHerdrShell(herdrShellCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "start it with toolbox herdr") {
		t.Fatalf("runHerdrShell = %v, want an error pointing at toolbox herdr", err)
	}
	if *attached != nil {
		t.Error("`toolbox herdr shell` with no Herdr Core Shell reached the attach")
	}
}
