package container

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/proximo"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// hostInventory is the host container list every sibling test works from: two
// toolbox-managed containers (a shell and the peer anchor), two Compose
// projects that share the basename "api" from different working directories,
// one standalone container, and the proximo stack — which is a Compose project
// like any other and is told apart only by its role label.
func hostInventory() []container.Summary {
	return []container.Summary{
		{ID: "tb1", Names: []string{"/toolbox-toolbox-97570176"}},
		{ID: "tb2", Names: []string{"/" + sessionplan.PeerAnchorContainerName}},
		{ID: "a1", Names: []string{"/api-db-1"}, Labels: map[string]string{
			composeProjectLabel: "api", composeWorkDirLabel: "/a",
		}},
		{ID: "a2", Names: []string{"/api-web-1"}, Labels: map[string]string{
			composeProjectLabel: "api", composeWorkDirLabel: "/a",
		}},
		{ID: "b1", Names: []string{"/api-web-1-other"}, Labels: map[string]string{
			composeProjectLabel: "api", composeWorkDirLabel: "/b",
		}},
		{ID: "pg", Names: []string{"/pg"}},
		{ID: "px", Names: []string{"/proximo-traefik-1"}, Labels: map[string]string{
			composeProjectLabel: "proximo", composeWorkDirLabel: "/Users/x/.proximo/stack",
			proximo.RoleLabel: "traefik",
		}},
	}
}

func inventoryClient() *mockClient {
	return &mockClient{
		listFn: func(_ context.Context, _ client.ContainerListOptions) ([]container.Summary, error) {
			return hostInventory(), nil
		},
	}
}

// Siblings is the single criterion behind completion, `stop --all` and
// `prune`: it groups Compose containers by project *and* working directory,
// keeps a label-less container as a target of its own, and never reports a
// container toolbox created.
func TestSiblingsGroupsProjectsAndExcludesToolbox(t *testing.T) {
	sibs, err := Siblings(context.Background(), inventoryClient())
	if err != nil {
		t.Fatalf("Siblings() error = %v", err)
	}

	var refs []string
	for _, s := range sibs {
		refs = append(refs, s.Ref)
	}
	want := []string{"container:pg", "project:api@/a", "project:api@/b", "project:proximo"}
	if len(refs) != len(want) {
		t.Fatalf("Siblings() refs = %v, want %v", refs, want)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Errorf("Siblings()[%d].Ref = %q, want %q", i, refs[i], want[i])
		}
	}

	byRef := make(map[string]Sibling, len(sibs))
	for _, s := range sibs {
		byRef[s.Ref] = s
	}
	if got := byRef["project:api@/a"].IDs; len(got) != 2 {
		t.Errorf("project:api@/a IDs = %v, want both containers", got)
	}
	if !byRef["project:proximo"].Proximo {
		t.Error("proximo stack not flagged: it would be swept in bulk")
	}
	if byRef["container:pg"].Proximo {
		t.Error("standalone container wrongly flagged as proximo")
	}
}

// ResolveSiblings is what turns a command line into targets. A typed ref is
// exact; a bare name is a convenience that holds only while it is
// unambiguous, and the error has to name the alternatives — the user cannot
// guess a working directory qualifier they have never seen.
func TestResolveSiblingsAcceptsRefsAndUnambiguousNames(t *testing.T) {
	sibs, err := Siblings(context.Background(), inventoryClient())
	if err != nil {
		t.Fatalf("Siblings() error = %v", err)
	}

	got, err := ResolveSiblings(sibs, []string{"project:api@/a", "pg", "proximo"})
	if err != nil {
		t.Fatalf("ResolveSiblings() error = %v", err)
	}
	want := []string{"project:api@/a", "container:pg", "project:proximo"}
	if len(got) != len(want) {
		t.Fatalf("ResolveSiblings() = %d targets, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Ref != want[i] {
			t.Errorf("ResolveSiblings()[%d].Ref = %q, want %q", i, got[i].Ref, want[i])
		}
	}
}

func TestResolveSiblingsRejectsAmbiguousAndUnknown(t *testing.T) {
	sibs, err := Siblings(context.Background(), inventoryClient())
	if err != nil {
		t.Fatalf("Siblings() error = %v", err)
	}

	_, err = ResolveSiblings(sibs, []string{"api"})
	if err == nil {
		t.Fatal("ResolveSiblings(\"api\") succeeded, want an ambiguity error")
	}
	for _, ref := range []string{"project:api@/a", "project:api@/b"} {
		if !strings.Contains(err.Error(), ref) {
			t.Errorf("ambiguity error %q does not name %q", err, ref)
		}
	}

	if _, err := ResolveSiblings(sibs, []string{"nope"}); err == nil {
		t.Error("ResolveSiblings(\"nope\") succeeded, want a not-found error")
	}
}

// A container toolbox created is never a target. Naming one has to fail with
// a pointer to the command that owns it, not silently resolve to nothing.
func TestResolveSiblingsRefusesToolboxContainer(t *testing.T) {
	sibs, err := Siblings(context.Background(), inventoryClient())
	if err != nil {
		t.Fatalf("Siblings() error = %v", err)
	}

	_, err = ResolveSiblings(sibs, []string{"toolbox-toolbox-97570176"})
	if err == nil {
		t.Fatal("naming a toolbox container succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), "toolbox stop") {
		t.Errorf("refusal %q does not point at `toolbox stop`", err)
	}
}

// BulkSiblings is what `stop --all` and `prune` act on. It is the same
// criterion as completion minus the proximo stack, and the stack it drops is
// returned rather than discarded: a target the shell offered and the sweep
// skipped has to be announced, not silently missing.
func TestBulkSiblingsExcludesProximoAndReportsIt(t *testing.T) {
	sibs, err := Siblings(context.Background(), inventoryClient())
	if err != nil {
		t.Fatalf("Siblings() error = %v", err)
	}

	targets, skipped := BulkSiblings(sibs)
	if len(targets) != len(sibs)-1 {
		t.Errorf("BulkSiblings() targets = %d, want %d", len(targets), len(sibs)-1)
	}
	for _, s := range targets {
		if s.Proximo {
			t.Errorf("BulkSiblings() would sweep %s", s.Ref)
		}
	}
	if len(skipped) != 1 || skipped[0].Ref != "project:proximo" {
		t.Errorf("BulkSiblings() skipped = %v, want just the proximo stack", skipped)
	}
}

// StopSiblings stops every container of every target it is handed, with a
// grace long enough for a database — a test stack usually has one, and the
// 2s tuned for a toolbox shell is not that.
func TestStopSiblingsStopsEveryContainerWithGrace(t *testing.T) {
	stopped := map[string]int{}
	cli := inventoryClient()
	cli.stopFn = func(_ context.Context, id string, opts client.ContainerStopOptions) error {
		if opts.Timeout == nil {
			t.Errorf("ContainerStop(%s) passed no timeout", id)
			return nil
		}
		stopped[id] = *opts.Timeout
		return nil
	}

	sibs, err := Siblings(context.Background(), cli)
	if err != nil {
		t.Fatalf("Siblings() error = %v", err)
	}
	targets, _ := BulkSiblings(sibs)
	if err := StopSiblings(context.Background(), cli, targets); err != nil {
		t.Fatalf("StopSiblings() error = %v", err)
	}

	for _, id := range []string{"a1", "a2", "b1", "pg"} {
		grace, ok := stopped[id]
		if !ok {
			t.Errorf("container %s not stopped", id)
			continue
		}
		if grace != siblingStopGrace {
			t.Errorf("container %s stopped with grace %d, want %d", id, grace, siblingStopGrace)
		}
	}
	if _, swept := stopped["px"]; swept {
		t.Error("proximo container stopped by a bulk sweep")
	}
	if _, swept := stopped["tb1"]; swept {
		t.Error("toolbox container stopped by this command")
	}
}

// RemoveSiblings removes what a project can rebuild for free — its containers
// and its networks — and nothing else unless asked. The network lookup must be
// scoped by the project label: an unscoped filter would remove every network
// on the host.
func TestRemoveSiblingsRemovesContainersAndProjectNetworks(t *testing.T) {
	var netFilters []client.Filters
	removedNets := map[string]bool{}
	removed := map[string]bool{}
	stopped := map[string]bool{}

	cli := inventoryClient()
	cli.stopFn = func(_ context.Context, id string, _ client.ContainerStopOptions) error {
		stopped[id] = true
		return nil
	}
	cli.removeFn = func(_ context.Context, id string, _ client.ContainerRemoveOptions) error {
		removed[id] = true
		return nil
	}
	cli.netListFn = func(_ context.Context, opts client.NetworkListOptions) ([]network.Summary, error) {
		netFilters = append(netFilters, opts.Filters)
		return []network.Summary{{Network: network.Network{ID: "net-a", Name: "api_default"}}}, nil
	}
	cli.netRemoveFn = func(_ context.Context, id string) error {
		removedNets[id] = true
		return nil
	}
	cli.volListFn = func(_ context.Context, _ client.VolumeListOptions) ([]volume.Volume, error) {
		t.Error("volumes listed without --volumes")
		return nil, nil
	}

	sibs, err := Siblings(context.Background(), cli)
	if err != nil {
		t.Fatalf("Siblings() error = %v", err)
	}
	targets, err := ResolveSiblings(sibs, []string{"project:api@/a", "pg"})
	if err != nil {
		t.Fatalf("ResolveSiblings() error = %v", err)
	}
	if err := RemoveSiblings(context.Background(), cli, targets, false); err != nil {
		t.Fatalf("RemoveSiblings() error = %v", err)
	}

	for _, id := range []string{"a1", "a2", "pg"} {
		if !stopped[id] {
			t.Errorf("container %s removed without being stopped first", id)
		}
		if !removed[id] {
			t.Errorf("container %s not removed", id)
		}
	}
	if !removedNets["net-a"] {
		t.Error("project network not removed")
	}
	// One lookup, for the project only: a standalone container owns no network.
	if len(netFilters) != 1 {
		t.Fatalf("NetworkList called %d times, want once (the project)", len(netFilters))
	}
	if !netFilters[0]["label"][composeProjectLabel+"=api"] {
		t.Errorf("network filter = %v, want it scoped to the project label", netFilters[0])
	}
}

// --volumes is the one escalation that loses data, so it must remove only
// volumes the project owns. A volume declared external carries no project
// label and is therefore never eligible.
func TestRemoveSiblingsVolumesOptInScopedToProject(t *testing.T) {
	var volFilters []client.Filters
	removedVols := map[string]bool{}

	cli := inventoryClient()
	cli.volListFn = func(_ context.Context, opts client.VolumeListOptions) ([]volume.Volume, error) {
		volFilters = append(volFilters, opts.Filters)
		return []volume.Volume{{Name: "api_pgdata"}}, nil
	}
	cli.volRemoveFn = func(_ context.Context, name string, _ client.VolumeRemoveOptions) error {
		removedVols[name] = true
		return nil
	}

	sibs, err := Siblings(context.Background(), cli)
	if err != nil {
		t.Fatalf("Siblings() error = %v", err)
	}
	targets, err := ResolveSiblings(sibs, []string{"project:api@/a"})
	if err != nil {
		t.Fatalf("ResolveSiblings() error = %v", err)
	}
	if err := RemoveSiblings(context.Background(), cli, targets, true); err != nil {
		t.Fatalf("RemoveSiblings() error = %v", err)
	}

	if !removedVols["api_pgdata"] {
		t.Error("project volume not removed under --volumes")
	}
	if len(volFilters) != 1 || !volFilters[0]["label"][composeProjectLabel+"=api"] {
		t.Errorf("volume filter = %v, want it scoped to the project label", volFilters)
	}
}
