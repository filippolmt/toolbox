package dockeridentity

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/mountplan"
)

// TestResolveReturnsHostUserSpec verifies the UserSpec encodes the
// caller process's UID and GID verbatim — this is the host-identity
// invariant that keeps bind-mounted files readable/writable without
// chown ceremony inside the container.
func TestResolveReturnsHostUserSpec(t *testing.T) {
	got := Resolve(nil)
	want := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	if got.UserSpec != want {
		t.Errorf("UserSpec = %q, want %q", got.UserSpec, want)
	}
}

// TestResolveNilGroupAddWhenNoSockBind: don't grant extra groups when
// the user did not bind-mount /var/run/docker.sock.
func TestResolveNilGroupAddWhenNoSockBind(t *testing.T) {
	got := Resolve([]string{"/workspace"})
	if got.GroupAdd != nil {
		t.Errorf("GroupAdd = %v, want nil", got.GroupAdd)
	}
}

// TestDockerSockGroupsReturnsNilWhenSockNotMounted: don't grant extra
// groups the user didn't ask for. If docker.sock isn't in the binds
// list, the runtime user has no need for root or the docker host GID.
func TestDockerSockGroupsReturnsNilWhenSockNotMounted(t *testing.T) {
	targets := []string{"/workspace", "/home/toolbox/.toolbox-state"}
	if got := dockerSockGroups(targets); got != nil {
		t.Errorf("dockerSockGroups(no sock) = %v, want nil", got)
	}
}

// TestDockerSockGroupsIncludesRootForDesktopCase: Docker Desktop
// reprojects the socket as root:root inside the container, so the
// runtime user must be in gid 0 to talk to /var/run/docker.sock
// regardless of host ownership.
func TestDockerSockGroupsIncludesRootForDesktopCase(t *testing.T) {
	orig := statSockGID
	t.Cleanup(func() { statSockGID = orig })
	// Simulate macOS Docker Desktop: host gid 0.
	statSockGID = func(_ string) (uint32, bool) { return 0, true }

	got := dockerSockGroups([]string{"/var/run/docker.sock"})
	if !slices.Contains(got, "0") {
		t.Errorf("groups must contain %q for Docker Desktop, got %v", "0", got)
	}
	if len(got) != 1 {
		t.Errorf("expected only [\"0\"] when host gid is 0, got %v", got)
	}
}

// TestDockerSockGroupsAppendsHostGIDOnLinux: on Linux the socket keeps
// the host "docker" group (e.g. 999). The runtime user joins both gid 0
// (for bind-mount parent access fallback) and the real host gid.
func TestDockerSockGroupsAppendsHostGIDOnLinux(t *testing.T) {
	orig := statSockGID
	t.Cleanup(func() { statSockGID = orig })
	statSockGID = func(_ string) (uint32, bool) { return 999, true }

	got := dockerSockGroups([]string{"/var/run/docker.sock"})
	want := []string{"0", "999"}
	if !slices.Equal(got, want) {
		t.Errorf("dockerSockGroups = %v, want %v", got, want)
	}
}

// TestDockerSockGroupsFallbackWhenStatFails: if statSockGID fails
// (pathological case), still grant gid 0 so Docker Desktop mode keeps
// working.
func TestDockerSockGroupsFallbackWhenStatFails(t *testing.T) {
	orig := statSockGID
	t.Cleanup(func() { statSockGID = orig })
	statSockGID = func(_ string) (uint32, bool) { return 0, false }

	got := dockerSockGroups([]string{"/var/run/docker.sock"})
	if !slices.Equal(got, []string{"0"}) {
		t.Errorf("expected fallback to [0], got %v", got)
	}
}

// TestDockerSockGroupsRequiresExactTarget: the target must be the socket
// path itself, not merely end with it. A near-miss like a bind at
// /workspace/var/run/docker.sock is some other file the user happens to
// mount, and must NOT earn the runtime user extra groups — this is the
// assert that fails if the match is ever loosened to HasSuffix/Contains.
func TestDockerSockGroupsRequiresExactTarget(t *testing.T) {
	orig := statSockGID
	t.Cleanup(func() { statSockGID = orig })
	statSockGID = func(_ string) (uint32, bool) { return 999, true }

	targets := []string{"/workspace/var/run/docker.sock"}
	if got := dockerSockGroups(targets); got != nil {
		t.Errorf("near-miss target must not grant groups, got %v", got)
	}
}

// TestSockPathMatchesMountplanDefault pins the bijection between the two
// unconnected copies of "/var/run/docker.sock": the one dockeridentity
// matches on, and the Target of mountplan's "docker-sock" default mount.
// If the default mount is ever retargeted, group-add resolution silently
// stops firing — nothing in the compiler links the two literals. Scope is
// the default set: a user `mounts:` patch retargeting docker-sock is not
// covered here (pre-existing behaviour, not introduced by this test). The
// import lives in this test file only: production dockeridentity stays a
// stdlib-only leaf, which is checkable by reading its non-test imports.
func TestSockPathMatchesMountplanDefault(t *testing.T) {
	for _, m := range mountplan.Defaults() {
		if m.Name == "docker-sock" {
			if m.Target != sockPath {
				t.Errorf("docker-sock mount Target = %q, want %q", m.Target, sockPath)
			}
			return
		}
	}
	t.Fatal("no default mount named \"docker-sock\" — group-add can never fire")
}

// TestResolveGroupAddWhenSockBound integrates the two seams: Resolve
// reads statSockGID through dockerSockGroups when a sock bind is
// present, surfacing the resulting GroupAdd on the Identity.
func TestResolveGroupAddWhenSockBound(t *testing.T) {
	orig := statSockGID
	t.Cleanup(func() { statSockGID = orig })
	statSockGID = func(_ string) (uint32, bool) { return 999, true }

	got := Resolve([]string{"/var/run/docker.sock"})
	if !slices.Contains(got.GroupAdd, "0") || !slices.Contains(got.GroupAdd, "999") {
		t.Errorf("Identity.GroupAdd = %v, want [0,999]", got.GroupAdd)
	}
	// Identity.UserSpec must still be the host UID/GID, not muted by GroupAdd.
	if !strings.Contains(got.UserSpec, ":") {
		t.Errorf("UserSpec malformed: %q", got.UserSpec)
	}
}
