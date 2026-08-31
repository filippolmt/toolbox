package cmd

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	moby "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
	"github.com/spf13/cobra"

	"github.com/filippolmt/toolbox/internal/container"
	"github.com/filippolmt/toolbox/internal/proximo"
)

func testSiblings() []container.Sibling {
	// Refs are what Siblings() produces; the fields below are the ones the
	// cmd layer renders, so they are set directly rather than driven through
	// a Docker fake that internal/container already covers.
	return []container.Sibling{
		{Ref: "container:pg", Name: "pg", IDs: []string{"pg"}},
		{Ref: "project:api@/a", Name: "api", WorkDir: "/a", IDs: []string{"a1", "a2"}},
		{Ref: "project:proximo", Name: "proximo", WorkDir: "/h/.proximo/stack", IDs: []string{"px"}, Proximo: true},
	}
}

// Completion hands the shell the typed ref as the value, so what the user
// ends up with on the command line is unambiguous by construction, and puts
// everything human in the description after the tab.
func TestSiblingCompletionsAreTypedRefsWithDescriptions(t *testing.T) {
	got := siblingCompletions(testSiblings())
	if len(got) != 3 {
		t.Fatalf("siblingCompletions() = %d entries, want 3", len(got))
	}
	for i, want := range []string{"container:pg", "project:api@/a", "project:proximo"} {
		value, _, found := strings.Cut(got[i], "\t")
		if !found {
			t.Errorf("entry %q carries no description", got[i])
		}
		if value != want {
			t.Errorf("entry %d value = %q, want %q", i, value, want)
		}
	}
	if !strings.Contains(got[1], "2") {
		t.Errorf("project entry %q does not say how many containers it covers", got[1])
	}
	// The one target a bulk sweep declines has to say so where it is offered,
	// or the user reads its absence from `prune` as a bug.
	if !strings.Contains(got[2], "prune") {
		t.Errorf("proximo entry %q does not warn that bulk skips it", got[2])
	}
}

func TestRenderSiblingsEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderSiblings(&buf, nil)
	if got := buf.String(); !strings.Contains(got, "No ") {
		t.Errorf("empty render = %q, want a plain statement that there is nothing", got)
	}
}

func TestRenderSiblingsTable(t *testing.T) {
	var buf bytes.Buffer
	renderSiblings(&buf, testSiblings())
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want header + 3 rows, got %d lines: %q", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "TARGET") {
		t.Errorf("header = %q", lines[0])
	}
	col := strings.Index(lines[0], "WORKDIR")
	if !strings.HasPrefix(lines[2][col:], "/a") {
		t.Errorf("row workdir misaligned at col %d: %q", col, lines[2])
	}
}

// `--all` is the blind form and takes no targets: passing both is a request
// with two different answers, so it fails as a usage error (exit 2) rather
// than picking one.
func TestContainerStopRejectsAllWithTargets(t *testing.T) {
	t.Cleanup(func() { containerStopAll = false })
	containerStopAll = true

	err := runContainerStop(containerStopCmd, []string{"pg"})
	if err == nil {
		t.Fatal("stop --all pg succeeded, want a usage error")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error %v is not a *usageError, so it would not exit 2", err)
	}
}

// selectTargets is the single place `stop --all` and `prune` get their set
// from, so the two cannot drift apart: bulk means the whole criterion minus
// the proximo stack, named means exactly what was resolved.
func TestSelectTargetsBulkIsTheCriterionMinusProximo(t *testing.T) {
	sibs := testSiblings()

	bulk, err := selectTargets(sibs, nil, true)
	if err != nil {
		t.Fatalf("selectTargets(bulk) error = %v", err)
	}
	if len(bulk) != 2 {
		t.Fatalf("bulk = %d targets, want the 2 non-proximo ones", len(bulk))
	}
	for _, s := range bulk {
		if s.Proximo {
			t.Errorf("bulk would sweep %s", s.Ref)
		}
	}

	named, err := selectTargets(sibs, []string{"proximo"}, false)
	if err != nil {
		t.Fatalf("selectTargets(named) error = %v", err)
	}
	if len(named) != 1 || named[0].Ref != "project:proximo" {
		t.Errorf("named = %v, want the proximo stack addressed by name", named)
	}
}

// stop with neither a target nor --all has nothing to do, and guessing
// (all? none?) is worse than saying so.
func TestContainerStopRequiresTargetOrAll(t *testing.T) {
	t.Cleanup(func() { containerStopAll = false })
	containerStopAll = false

	err := runContainerStop(containerStopCmd, nil)
	var ue *usageError
	if err == nil || !errors.As(err, &ue) {
		t.Errorf("bare `container stop` error = %v, want a *usageError", err)
	}
}

// fakeDocker is the Docker double for the verbs that talk to the daemon.
// Unimplemented methods are never called by this group; the embedded
// interface is nil, so any new call panics rather than passing silently.
type fakeDocker struct {
	client.APIClient

	containers []moby.Summary
	listErr    error
	networks   []network.Summary
	volumes    []volume.Volume

	stopped    []string
	removed    []string
	rmNetworks []string
	rmVolumes  []string
	volListed  bool
}

func (f *fakeDocker) Close() error { return nil }

func (f *fakeDocker) ContainerList(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
	if f.listErr != nil {
		return client.ContainerListResult{}, f.listErr
	}
	return client.ContainerListResult{Items: f.containers}, nil
}

func (f *fakeDocker) ContainerStop(_ context.Context, id string, _ client.ContainerStopOptions) (client.ContainerStopResult, error) {
	f.stopped = append(f.stopped, id)
	return client.ContainerStopResult{}, nil
}

func (f *fakeDocker) ContainerRemove(_ context.Context, id string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	f.removed = append(f.removed, id)
	return client.ContainerRemoveResult{}, nil
}

func (f *fakeDocker) NetworkList(_ context.Context, _ client.NetworkListOptions) (client.NetworkListResult, error) {
	return client.NetworkListResult{Items: f.networks}, nil
}

func (f *fakeDocker) NetworkRemove(_ context.Context, id string, _ client.NetworkRemoveOptions) (client.NetworkRemoveResult, error) {
	f.rmNetworks = append(f.rmNetworks, id)
	return client.NetworkRemoveResult{}, nil
}

func (f *fakeDocker) VolumeList(_ context.Context, _ client.VolumeListOptions) (client.VolumeListResult, error) {
	f.volListed = true
	return client.VolumeListResult{Items: f.volumes}, nil
}

func (f *fakeDocker) VolumeRemove(_ context.Context, name string, _ client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
	f.rmVolumes = append(f.rmVolumes, name)
	return client.VolumeRemoveResult{}, nil
}

// hostFake is the inventory the command-level tests run against: one toolbox
// container, one Compose project, one standalone container, and the proximo
// stack.
func hostFake() *fakeDocker {
	return &fakeDocker{
		containers: []moby.Summary{
			{ID: "tb1", Names: []string{"/toolbox-toolbox-97570176"}},
			{ID: "a1", Names: []string{"/api-db-1"}, Labels: map[string]string{
				"com.docker.compose.project": "api", "com.docker.compose.project.working_dir": "/a",
			}},
			{ID: "a2", Names: []string{"/api-web-1"}, Labels: map[string]string{
				"com.docker.compose.project": "api", "com.docker.compose.project.working_dir": "/a",
			}},
			{ID: "pg", Names: []string{"/pg"}},
			{ID: "px", Names: []string{"/proximo-traefik-1"}, Labels: map[string]string{
				"com.docker.compose.project": "proximo", "com.docker.compose.project.working_dir": "/h/.proximo/stack",
				proximo.RoleLabel: "traefik",
			}},
		},
		networks: []network.Summary{{Network: network.Network{ID: "net-a", Name: "api_default"}}},
		volumes:  []volume.Volume{{Name: "api_pgdata"}},
	}
}

// withFakeDocker points the command group at fake for the duration of a test.
func withFakeDocker(t *testing.T, fake *fakeDocker) {
	t.Helper()
	prev := newDockerClient
	newDockerClient = func() (client.APIClient, error) { return fake, nil }
	t.Cleanup(func() { newDockerClient = prev })
}

func TestRunContainerRmRemovesTargetAndItsNetworks(t *testing.T) {
	fake := hostFake()
	withFakeDocker(t, fake)

	if err := runContainerRm(containerRmCmd, []string{"project:api"}); err != nil {
		t.Fatalf("runContainerRm() error = %v", err)
	}

	if len(fake.stopped) != 2 || len(fake.removed) != 2 {
		t.Errorf("stopped = %v, removed = %v, want both containers of the project", fake.stopped, fake.removed)
	}
	if len(fake.rmNetworks) != 1 || fake.rmNetworks[0] != "net-a" {
		t.Errorf("rmNetworks = %v, want the project network", fake.rmNetworks)
	}
	if fake.volListed {
		t.Error("volumes listed without --volumes")
	}
}

// --dry-run has to be inert: it prints the set and touches nothing.
func TestRunContainerPruneDryRunTouchesNothing(t *testing.T) {
	fake := hostFake()
	withFakeDocker(t, fake)
	t.Cleanup(func() { containerPruneOpts.dryRun = false })
	containerPruneOpts.dryRun = true

	var buf bytes.Buffer
	containerPruneCmd.SetOut(&buf)
	t.Cleanup(func() { containerPruneCmd.SetOut(nil) })

	if err := runContainerPrune(containerPruneCmd, nil); err != nil {
		t.Fatalf("runContainerPrune() error = %v", err)
	}

	out := buf.String()
	for _, want := range []string{"TARGET", "project:api", "container:pg"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output %q does not mention %q", out, want)
		}
	}
	if strings.Contains(out, "proximo") {
		t.Errorf("dry-run output lists the proximo stack: %q", out)
	}
	if len(fake.stopped)+len(fake.removed)+len(fake.rmNetworks) != 0 {
		t.Errorf("dry-run acted on the daemon: %v %v %v", fake.stopped, fake.removed, fake.rmNetworks)
	}
}

// prune sweeps everything but the proximo stack, and --volumes reaches the
// volumes of the projects it removes.
func TestRunContainerPruneSweepsAllButProximo(t *testing.T) {
	fake := hostFake()
	withFakeDocker(t, fake)
	t.Cleanup(func() { containerPruneOpts.volumes = false })
	containerPruneOpts.volumes = true

	if err := runContainerPrune(containerPruneCmd, nil); err != nil {
		t.Fatalf("runContainerPrune() error = %v", err)
	}

	for _, id := range []string{"a1", "a2", "pg"} {
		if !slices.Contains(fake.removed, id) {
			t.Errorf("container %s not removed", id)
		}
	}
	for _, id := range []string{"px", "tb1"} {
		if slices.Contains(fake.removed, id) {
			t.Errorf("prune removed %s, which it must never touch", id)
		}
	}
	if len(fake.rmVolumes) != 1 || fake.rmVolumes[0] != "api_pgdata" {
		t.Errorf("rmVolumes = %v, want the project volume under --volumes", fake.rmVolumes)
	}
}

// A daemon that cannot be listed is an error on a verb — the user asked for
// something and it did not happen.
func TestRunContainerRmPropagatesDaemonError(t *testing.T) {
	fake := hostFake()
	fake.listErr = errors.New("daemon unreachable")
	withFakeDocker(t, fake)

	if err := runContainerRm(containerRmCmd, []string{"project:api"}); err == nil {
		t.Fatal("runContainerRm() succeeded against an unreachable daemon")
	}
}

func TestCompleteSiblingsOffersRefsAndDropsAlreadyNamed(t *testing.T) {
	withFakeDocker(t, hostFake())

	got, directive := completeSiblings(containerStopCmd, []string{"container:pg"}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
	for _, entry := range got {
		if strings.HasPrefix(entry, "container:pg\t") {
			t.Errorf("completion re-offers %q, already on the command line", entry)
		}
	}
	if len(got) != 2 {
		t.Errorf("completion = %v, want the project and the proximo stack", got)
	}
}

// The completion runs inside the user's prompt, so an unreachable daemon must
// yield nothing at all rather than an error string the shell would display.
func TestCompleteSiblingsSilentWhenDaemonUnreachable(t *testing.T) {
	prev := newDockerClient
	newDockerClient = func() (client.APIClient, error) { return nil, errors.New("no daemon") }
	t.Cleanup(func() { newDockerClient = prev })

	got, directive := completeSiblings(containerStopCmd, nil, "")
	if got != nil {
		t.Errorf("completion = %v, want nothing", got)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}

	fake := hostFake()
	fake.listErr = errors.New("daemon unreachable")
	withFakeDocker(t, fake)
	if got, _ := completeSiblings(containerStopCmd, nil, ""); got != nil {
		t.Errorf("completion after a list failure = %v, want nothing", got)
	}
}

// stop --all stops every target but the proximo stack, and stops rather than
// removes: the containers must still be there for a `docker start`.
func TestRunContainerStopAllStopsWithoutRemoving(t *testing.T) {
	fake := hostFake()
	withFakeDocker(t, fake)
	t.Cleanup(func() { containerStopAll = false })
	containerStopAll = true

	if err := runContainerStop(containerStopCmd, nil); err != nil {
		t.Fatalf("runContainerStop() error = %v", err)
	}

	for _, id := range []string{"a1", "a2", "pg"} {
		if !slices.Contains(fake.stopped, id) {
			t.Errorf("container %s not stopped", id)
		}
	}
	for _, id := range []string{"px", "tb1"} {
		if slices.Contains(fake.stopped, id) {
			t.Errorf("--all stopped %s, which it must never touch", id)
		}
	}
	if len(fake.removed)+len(fake.rmNetworks) != 0 {
		t.Errorf("stop removed things: %v %v", fake.removed, fake.rmNetworks)
	}
}

func TestRunContainerStopNamedTargetOnly(t *testing.T) {
	fake := hostFake()
	withFakeDocker(t, fake)

	if err := runContainerStop(containerStopCmd, []string{"pg"}); err != nil {
		t.Fatalf("runContainerStop() error = %v", err)
	}
	if len(fake.stopped) != 1 || fake.stopped[0] != "pg" {
		t.Errorf("stopped = %v, want just the named container", fake.stopped)
	}
}
