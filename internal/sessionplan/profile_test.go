package sessionplan_test

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// TestContainerNameForProfileDistinct: a profile shell gets a different
// container name from the default shell for the same workspace, and the
// no-profile name is byte-identical to the pre-profile format.
func TestContainerNameForProfileDistinct(t *testing.T) {
	const ws = "/home/u/proj"

	plain := sessionplan.ContainerNameFor(ws, "")
	profiled := sessionplan.ContainerNameFor(ws, "work")

	if plain == profiled {
		t.Fatalf("profile shares container name with default: %q", plain)
	}
	// Empty profile must reproduce the workspace-hash format exactly.
	if !strings.HasPrefix(plain, sessionplan.ContainerNamePrefix+"proj-") {
		t.Errorf("no-profile name = %q, want prefix %q", plain, sessionplan.ContainerNamePrefix+"proj-")
	}
	// Two profiles for the same workspace also differ.
	if other := sessionplan.ContainerNameFor(ws, "personal"); other == profiled {
		t.Errorf("distinct profiles collide: %q", other)
	}
}

// TestContainerNameForProfileDeterministic: same (workspace, profile) always
// yields the same name so a reopened profile shell reattaches.
func TestContainerNameForProfileDeterministic(t *testing.T) {
	a := sessionplan.ContainerNameFor("/home/u/proj", "work")
	b := sessionplan.ContainerNameFor("/home/u/proj", "work")
	if a != b {
		t.Errorf("non-deterministic: %q != %q", a, b)
	}
}

// TestContainerNameForProfileLength: folding the profile into the hash (not
// the visible basename) keeps the name within the Docker cap regardless of
// profile-name length.
func TestContainerNameForProfileLength(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := sessionplan.ContainerNameFor("/home/u/"+long, long)
	if len(got) > sessionplan.MaxContainerNameLen {
		t.Errorf("name too long: %d > %d (%q)", len(got), sessionplan.MaxContainerNameLen, got)
	}
}
