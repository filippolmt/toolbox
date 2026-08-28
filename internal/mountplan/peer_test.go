package mountplan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
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
			t.Setenv("HOME", t.TempDir())
			workspace := t.TempDir()

			result, err := Plan(PlanInput{Cfg: &config.Config{}, Workspace: workspace, Peer: tc.peer})
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
	t.Setenv("HOME", tmpHome)

	result, err := Plan(PlanInput{Cfg: &config.Config{}, Workspace: t.TempDir(), Peer: true})
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
	t.Setenv("HOME", tmpHome)

	result, err := Plan(PlanInput{
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
