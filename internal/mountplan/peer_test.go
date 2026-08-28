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
// inbox-socket directory in the bind set: absent by default, bound rw and
// created 0700 when opted in. 0700 is load-bearing — Claude Code silently
// falls back to /tmp/cc-socks-<uid> on a looser directory, which would leave
// the whole feature dead with no error.
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
			tmpHome := t.TempDir()
			t.Setenv("HOME", tmpHome)
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
			src := filepath.Join(tmpHome, ".toolbox", PeerSocketDirName)
			info, statErr := os.Stat(src)
			if statErr != nil {
				t.Fatalf("socket dir not created at %s: %v", src, statErr)
			}
			if perm := info.Mode().Perm(); perm != 0o700 {
				t.Errorf("socket dir perm = %o, want 700", perm)
			}
		})
	}
}

// TestPlanPeerSocketDirFollowsMountsRoot asserts the socket dir rides the
// config-level mounts_root relocation like every other ~/.toolbox source.
func TestPlanPeerSocketDirFollowsMountsRoot(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	root := filepath.Join(tmpHome, "relocated")

	result, err := Plan(PlanInput{
		Cfg:       &config.Config{MountsRoot: root},
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
	want := filepath.Join(root, PeerSocketDirName)
	if b.Source != want {
		t.Errorf("Source = %q, want %q", b.Source, want)
	}
}

// TestPlanPeerSocketDirTightensExistingMode covers the invariant MkdirAll
// cannot hold: it only sets the mode on a directory it creates, so a
// ~/.toolbox/cc-socks left behind at 0755 (by an older run, an umask, another
// tool) was bound as-is — and Claude Code answers a looser directory by
// falling back to /tmp/cc-socks-<uid> without saying so, which kills the
// feature with no error anywhere.
func TestPlanPeerSocketDirTightensExistingMode(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	src := filepath.Join(tmpHome, ".toolbox", PeerSocketDirName)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Chmod(src, 0o755); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	result, err := Plan(PlanInput{Cfg: &config.Config{}, Workspace: t.TempDir(), Peer: true})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, ok := findBind(result.Binds, PeerSocketDirTarget); !ok {
		t.Fatalf("no bind on %s", PeerSocketDirTarget)
	}
	info, statErr := os.Stat(src)
	if statErr != nil {
		t.Fatalf("stat %s: %v", src, statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("socket dir perm = %o, want 700", perm)
	}
}
