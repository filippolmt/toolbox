package container

import (
	"slices"
	"testing"
)

// TestDockerSockGroupsReturnsNilWhenSockNotMounted: don't grant extra groups
// the user didn't ask for. If docker.sock isn't in the binds list, the
// runtime user has no need for root or the docker host GID.
func TestDockerSockGroupsReturnsNilWhenSockNotMounted(t *testing.T) {
	binds := []string{
		"/home/alice:/workspace:rw",
		"/tmp/state:/home/toolbox/.toolbox-state:rw",
	}
	if got := dockerSockGroups(binds); got != nil {
		t.Errorf("dockerSockGroups(no sock) = %v, want nil", got)
	}
}

// TestDockerSockGroupsIncludesRootForDesktopCase: Docker Desktop reprojects
// the socket as root:root inside the container, so the runtime user must be
// in gid 0 to talk to /var/run/docker.sock regardless of host ownership.
func TestDockerSockGroupsIncludesRootForDesktopCase(t *testing.T) {
	orig := statSockGID
	t.Cleanup(func() { statSockGID = orig })
	// Simulate macOS Docker Desktop: host gid 0.
	statSockGID = func(_ string) (uint32, bool) { return 0, true }

	got := dockerSockGroups([]string{"/var/run/docker.sock:/var/run/docker.sock:rw"})
	if !slices.Contains(got, "0") {
		t.Errorf("groups must contain %q for Docker Desktop, got %v", "0", got)
	}
	if len(got) != 1 {
		t.Errorf("expected only [\"0\"] when host gid is 0, got %v", got)
	}
}

// TestDockerSockGroupsAppendsHostGIDOnLinux: on Linux the socket keeps the
// host "docker" group (e.g. 999). The runtime user joins both gid 0 (for
// bind-mount parent access fallback) and the real host gid.
func TestDockerSockGroupsAppendsHostGIDOnLinux(t *testing.T) {
	orig := statSockGID
	t.Cleanup(func() { statSockGID = orig })
	statSockGID = func(_ string) (uint32, bool) { return 999, true }

	got := dockerSockGroups([]string{"/var/run/docker.sock:/var/run/docker.sock"})
	want := []string{"0", "999"}
	if !slices.Equal(got, want) {
		t.Errorf("dockerSockGroups = %v, want %v", got, want)
	}
}

// TestDockerSockGroupsFallbackWhenStatFails: if statSockGID fails (pathological
// case), still grant gid 0 so Docker Desktop mode keeps working.
func TestDockerSockGroupsFallbackWhenStatFails(t *testing.T) {
	orig := statSockGID
	t.Cleanup(func() { statSockGID = orig })
	statSockGID = func(_ string) (uint32, bool) { return 0, false }

	got := dockerSockGroups([]string{"/var/run/docker.sock:/var/run/docker.sock:rw"})
	if !slices.Equal(got, []string{"0"}) {
		t.Errorf("expected fallback to [0], got %v", got)
	}
}

// TestDockerSockGroupsMatchesOnTargetNotSource: the match is on the
// in-container target path, not the host source. A bind whose host path
// happens to end in "docker.sock" (e.g. a file of the same name in $HOME)
// must NOT trigger the extra groups.
func TestDockerSockGroupsMatchesOnTargetNotSource(t *testing.T) {
	orig := statSockGID
	t.Cleanup(func() { statSockGID = orig })
	statSockGID = func(_ string) (uint32, bool) { return 999, true }

	// Source looks like a socket but target is a normal file — not the sock.
	binds := []string{"/home/alice/docker.sock:/workspace/fake:rw"}
	if got := dockerSockGroups(binds); got != nil {
		t.Errorf("should match on target only, got %v", got)
	}
}
