package container

// shellEnv returns the env vars injected into every shell spawned by the
// container. TOOLBOX_HOST_WORKSPACE holds the absolute host path mounted at
// the canonical workspace target so that Makefiles and compose files can
// pass a host-resolvable path to `docker run -v` under the bind-mounted
// socket (DooD): a literal "/workspace/foo" is meaningless to the host
// daemon. PWD is set explicitly to workingDir so that scripts reading $PWD
// directly (without a getcwd fallback) see the same path bash exposes after
// starting in WorkingDir.
//
// The workspace target itself and the host-path mirror logic live in
// internal/mountplan; lifecycle.Shell consults mountplan.Plan to learn
// workingDir and forwards it here.
func shellEnv(workspace, workingDir string) []string {
	return []string{
		"TOOLBOX_HOST_WORKSPACE=" + workspace,
		"PWD=" + workingDir,
	}
}
