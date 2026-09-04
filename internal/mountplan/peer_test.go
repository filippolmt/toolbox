package mountplan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
)

// findBind returns the bind whose Target matches, or ok=false.
func findBind(binds []Bind, target string) (Bind, bool) {
	for _, b := range binds {
		if b.Target == target {
			return b, true
		}
	}
	return Bind{}, false
}

// TestPlanPeerSocketDir asserts the peer opt-in is what puts the shared
// inbox-socket mount in the bind set: absent by default, and bound rw from the
// named volume when opted in.
//
// The volume name is load-bearing, not cosmetic: a host path here would be
// served over virtiofs on Docker Desktop, where the chmod Claude Code runs on
// each socket it binds fails with EINVAL and kills the feature silently.
func TestPlanPeerSocketDir(t *testing.T) {
	for _, tc := range []struct {
		name string
		peer bool
		want bool
	}{
		{name: "off_by_default"},
		{name: "opted_in", peer: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()

			result, err := Plan(PlanInput{Host: testHost(t), Cfg: &config.Config{}, Workspace: workspace, Peer: tc.peer})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}

			b, ok := findBind(result.Binds, PeerSocketDirTarget)
			if ok != tc.want {
				t.Fatalf("bind on %s present = %v, want %v", PeerSocketDirTarget, ok, tc.want)
			}
			if !tc.want {
				return
			}
			if b.Mode != "rw" {
				t.Errorf("Mode = %q, want rw", b.Mode)
			}
			if b.Source != PeerSocketVolumeName {
				t.Errorf("Source = %q, want the named volume %q", b.Source, PeerSocketVolumeName)
			}
		})
	}
}

// TestPlanPeerSocketDirTouchesNoHostPath asserts the opt-in creates nothing
// under the mounts root. The socket directory used to be a bind of
// ~/.toolbox/cc-socks, so a leftover CreateIfMissing would keep producing a
// host directory that no longer participates in anything — and, on a host
// where that path still exists from an older release, would invite the bind
// back by hand.
func TestPlanPeerSocketDirTouchesNoHostPath(t *testing.T) {
	tmpHome := t.TempDir()

	result, err := Plan(PlanInput{Host: fsx.Host{Home: tmpHome}, Cfg: &config.Config{}, Workspace: t.TempDir(), Peer: true})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, ok := findBind(result.Binds, PeerSocketDirTarget); !ok {
		t.Fatalf("no bind on %s", PeerSocketDirTarget)
	}

	stale := filepath.Join(tmpHome, ".toolbox", "cc-socks")
	if _, statErr := os.Stat(stale); !os.IsNotExist(statErr) {
		t.Errorf("Plan created %s (stat err = %v), want no host path for the socket dir", stale, statErr)
	}
}

// TestPlanPeerSocketDirIgnoresMountsRoot asserts the socket mount does NOT
// ride the mounts_root relocation. It is a Docker volume rather than a path
// under ~/.toolbox, so there is nothing for mounts_root to relocate — and
// forking it per root would leave two opted-in shells discovering each other
// through the shared PID namespace while failing to deliver.
func TestPlanPeerSocketDirIgnoresMountsRoot(t *testing.T) {
	tmpHome := t.TempDir()

	result, err := Plan(PlanInput{
		Host:      fsx.Host{Home: tmpHome},
		Cfg:       &config.Config{MountsRoot: filepath.Join(tmpHome, "relocated")},
		Workspace: t.TempDir(),
		Peer:      true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	b, ok := findBind(result.Binds, PeerSocketDirTarget)
	if !ok {
		t.Fatalf("no bind on %s", PeerSocketDirTarget)
	}
	if b.Source != PeerSocketVolumeName {
		t.Errorf("Source = %q, want the named volume %q regardless of mounts_root", b.Source, PeerSocketVolumeName)
	}
}

// TestWithoutPeerSocketBind asserts the degrade path removes exactly the shared
// socket mount and leaves the rest of the bind set intact. A session that
// mounts the shared directory without joining the anchor's PID namespace looks
// healthy and reaches nobody, so the two have to fall together.
func TestWithoutPeerSocketBind(t *testing.T) {
	workspace := Bind{Source: "/host/repo", Target: "/workspace", Mode: "rw"}
	claude := Bind{Source: "/host/.toolbox/claude", Target: "/home/toolbox/.claude", Mode: "rw"}
	socks := Bind{
		Source: PeerSocketVolumeName,
		Target: PeerSocketDirTarget,
		Mode:   "rw",
	}

	got := WithoutPeerSocketBind([]Bind{workspace, socks, claude})

	want := []Bind{workspace, claude}
	if len(got) != len(want) {
		t.Fatalf("kept %d binds (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bind %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestWithoutPeerSocketBindNoOptIn asserts a non-participating bind set
// passes through untouched — the ordinary case, since the mount is only ever
// appended for an opted-in session.
func TestWithoutPeerSocketBindNoOptIn(t *testing.T) {
	binds := []Bind{{Source: "/host/repo", Target: "/workspace", Mode: "rw"}}

	got := WithoutPeerSocketBind(binds)

	if len(got) != len(binds) || got[0] != binds[0] {
		t.Errorf("WithoutPeerSocketBind(%v) = %v, want it unchanged", binds, got)
	}
}
