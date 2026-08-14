// Package dockeridentity owns the host-process → container-identity
// translation at the Docker edge: the "<uid>:<gid>" user spec passed to
// ContainerCreate and the supplementary group IDs needed for the
// runtime user to talk to a bind-mounted /var/run/docker.sock.
//
// SessionPlan deliberately does NOT encode this concept (see CONTEXT.md
// → Session Plan): host-process identity and daemon-fs state are read
// fresh at the Docker edge so the plan stays a pure design-time artifact
// composable in tests without OS state. dockeridentity is that edge —
// `Resolve(bindTargets)` is the single seam container.Shell calls before
// ContainerCreate, returning a typed Identity{UserSpec, GroupAdd}.
package dockeridentity

import (
	"fmt"
	"os"
	"slices"
	"syscall"
)

// sockPath is the in-container target the group-add decision keys on. It
// mirrors the Target of mountplan's "docker-sock" default mount; the two
// literals are unconnected by the compiler, so the bijection is pinned by
// TestSockPathMatchesMountplanDefault instead of by an import (which would
// cost this leaf its stdlib-only dependency set).
//
// That pin covers the default mount set only. A user `mounts:` patch that
// retargets docker-sock still yields a bind this never matches — long-
// standing behaviour, out of scope here.
const sockPath = "/var/run/docker.sock"

// Identity carries the Docker-edge inputs derived from the host process
// and the bind set: the "<uid>:<gid>" user spec and the supplementary
// group IDs to grant the runtime user.
type Identity struct {
	// UserSpec is the "<uid>:<gid>" string passed to ContainerConfig.User
	// so the container runs as the host UID/GID. This keeps bind-mounted
	// files (credentials, ssh keys, bash history) readable/writable
	// without uid mismatch.
	UserSpec string

	// GroupAdd lists supplementary GIDs to add to HostConfig.GroupAdd so
	// the runtime user can access a bind-mounted /var/run/docker.sock.
	// Nil when the socket is not in the bind set — never grant extra
	// groups the user didn't ask for.
	GroupAdd []string
}

// Resolve assembles the Identity from the current host process (os.Getuid,
// os.Getgid) and the in-container target paths of the bind set the
// SessionPlan composed. Only the docker.sock target is inspected;
// everything else is SessionPlan's concern.
//
// Targets rather than []mountplan.Bind: the caller already holds typed
// binds and reads b.Target off them, which gives the same compile error
// if the field is ever renamed — without making this stdlib-only leaf
// depend on mountplan (and transitively on config, fsx and proximo). A
// docker-sock mounted read-only still needs GroupAdd, so Bind.Mode would
// never be consulted here either.
func Resolve(bindTargets []string) Identity {
	return Identity{
		UserSpec: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		GroupAdd: dockerSockGroups(bindTargets),
	}
}

// dockerSockGroups returns the supplementary group IDs that grant the
// runtime user access to /var/run/docker.sock when it is bind-mounted.
// Without these, a container running as the host UID cannot talk to the
// Docker API because the socket is group-owned (typically mode 660).
//
// Two GIDs are added to cover both deployment modes:
//   - "0" (root): Docker Desktop on macOS/Windows reprojects the socket
//     as root:root inside the container regardless of host ownership.
//   - host sock GID: on Linux the socket keeps the host group (usually
//     "docker"), so the container must join that GID.
//
// Returns nil when docker.sock is not among bindTargets.
func dockerSockGroups(bindTargets []string) []string {
	if !slices.Contains(bindTargets, sockPath) {
		return nil
	}

	groups := []string{"0"}
	if gid, ok := statSockGID(sockPath); ok && gid != 0 {
		groups = append(groups, fmt.Sprintf("%d", gid))
	}
	return groups
}

// statSockGID returns the GID owning the given path on the host,
// following symlinks. Returns (0, false) on any error — the caller falls
// back to gid 0. Exposed as a package-level var so tests can simulate
// Docker Desktop (gid 0) vs Linux (host docker group GID).
var statSockGID = func(path string) (uint32, bool) {
	// Stat follows symlinks; docker.sock is often a symlink on macOS.
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return sys.Gid, true
}
