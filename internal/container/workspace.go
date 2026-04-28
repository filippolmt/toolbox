package container

import (
	"path/filepath"
	"strings"
)

// WorkspaceTarget is the canonical in-container path where the host CWD is
// mounted. When it is safe to do so, the same host directory is also mirrored
// at its own absolute host path (see workspaceMirrorPath) and used as the
// shell WorkingDir, so `$PWD`-based bind mounts from inside the container
// resolve to a path the host daemon knows under DooD.
const WorkspaceTarget = "/workspace"

// reservedMirrorPrefixes lists in-container directories that must not be
// shadowed by the host-path mirror of the workspace. A host path equal to or
// nested under any of these is mounted only at WorkspaceTarget.
var reservedMirrorPrefixes = []string{
	WorkspaceTarget,
	"/home/toolbox",
	"/root",
	"/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32",
	"/boot", "/dev", "/etc", "/proc", "/run", "/sys", "/usr", "/var",
}

// workspaceMirrorPath returns the in-container path at which the workspace
// should be mirrored in addition to WorkspaceTarget, plus true when the
// mirror is safe to create. The mirror is the workspace's own absolute host
// path, which makes `$PWD` inside the shell match what the host daemon sees
// — the key ingredient for DooD bind mounts to resolve without rewriting.
//
// The mirror is skipped when:
//   - the path is empty, relative, or equal to the filesystem root;
//   - the path equals WorkspaceTarget (already mounted there);
//   - the path would shadow a reserved container directory (see
//     reservedMirrorPrefixes) — e.g. /home/toolbox, /usr, /etc.
func workspaceMirrorPath(workspace string) (string, bool) {
	if workspace == "" || !filepath.IsAbs(workspace) {
		return "", false
	}
	abs := filepath.Clean(workspace)
	if abs == "/" || abs == WorkspaceTarget {
		return "", false
	}
	for _, r := range reservedMirrorPrefixes {
		if abs == r || strings.HasPrefix(abs, r+"/") {
			return "", false
		}
	}
	return abs, true
}

// shellEnv returns the env vars injected into every shell spawned by the
// container. TOOLBOX_HOST_WORKSPACE holds the absolute host path mounted at
// /workspace so that Makefiles and compose files can pass a host-resolvable
// path to `docker run -v` under the bind-mounted socket (DooD): a literal
// "/workspace/foo" is meaningless to the host daemon. PWD is set explicitly
// to workingDir so that scripts reading $PWD directly (without a getcwd
// fallback) see the same path bash exposes after starting in WorkingDir.
func shellEnv(workspace, workingDir string) []string {
	return []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + workingDir,
	}
}
