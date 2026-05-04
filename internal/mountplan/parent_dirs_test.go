package mountplan

import (
	"slices"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

// TestParentDirsExcludesHomeRoot guards the contract that /home/toolbox
// itself is never listed (Dockerfile already creates it). Only sibling
// dirs (e.g. /home/toolbox/.config) need pre-creation.
func TestParentDirsExcludesHomeRoot(t *testing.T) {
	mounts := []config.Mount{
		{Target: "/home/toolbox/.claude"},
		{Target: "/home/toolbox/foo"},
	}
	got := ParentDirs(mounts)
	if slices.Contains(got, "/home/toolbox") {
		t.Errorf("ParentDirs must not include /home/toolbox itself, got %v", got)
	}
}

// TestParentDirsSkipsTargetsOutsideHome guards the prefix filter: only
// targets under /home/toolbox/ produce parent dirs (the Dockerfile fix is
// scoped to the runtime user's HOME).
func TestParentDirsSkipsTargetsOutsideHome(t *testing.T) {
	mounts := []config.Mount{
		{Target: "/var/run/docker.sock"},
		{Target: "/home/toolbox/.config/gh"},
	}
	got := ParentDirs(mounts)
	for _, p := range got {
		if !slices.ContainsFunc([]string{"/home/toolbox/.config"}, func(s string) bool { return p == s }) {
			t.Errorf("unexpected parent dir %q in %v", p, got)
		}
	}
	if !slices.Contains(got, "/home/toolbox/.config") {
		t.Errorf("expected /home/toolbox/.config in %v", got)
	}
}

// TestParentDirsDeduplicatesAndSorts: multiple mounts under the same parent
// produce a single entry; the slice is sorted for stable output.
func TestParentDirsDeduplicatesAndSorts(t *testing.T) {
	mounts := []config.Mount{
		{Target: "/home/toolbox/.config/gh"},
		{Target: "/home/toolbox/.config/glab-cli"},
		{Target: "/home/toolbox/.cache/ms-playwright"},
	}
	got := ParentDirs(mounts)
	want := []string{"/home/toolbox/.cache", "/home/toolbox/.config"}
	if !slices.Equal(got, want) {
		t.Errorf("ParentDirs = %v, want %v", got, want)
	}
}
