package sessionplan

import "github.com/filippolmt/toolbox/internal/mountplan"

// Herdr Core Shell: the Detached Shell that hosts herdr-core for the Herdr
// desktop app, one per host and Profile. → CONTEXT.md, Herdr Core Shell.

// HerdrCoreStateTarget is herdr-core's Unix state home ($XDG_STATE_HOME
// unset): where the Shell mounts its state volume. Off the Host Share on
// purpose: herdr-core keeps its sockets there when no runtime directory
// exists, and a Host Share bind cannot hold one.
const HerdrCoreStateTarget = "/home/toolbox/.local/state/herdr-core"

// HerdrCoreContainerName is `toolbox-herdr`, or `toolbox-herdr-<profile>`.
func HerdrCoreContainerName(profile *mountplan.Profile) (string, error) {
	n, err := herdrCoreNames(profile)
	return n.container, err
}

func herdrCoreNames(profile *mountplan.Profile) (singletonNameSet, error) {
	return singletonNames("herdr", "a Herdr Core Shell", profile)
}

// herdrCoreWorkspace is the dir argument.
// shortcut: no herdr_workspace key yet; #1531 passes in.Cfg.HerdrWorkspace here.
func herdrCoreWorkspace(in PlanInput) (string, error) {
	return singletonWorkspace(in, "toolbox herdr", "herdr_workspace", "", "the Herdr Core Shell")
}
