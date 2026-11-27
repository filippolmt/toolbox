package sessionplan

import (
	"strings"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// Herdr Core Shell: the Detached Shell that hosts herdr-core for the Herdr
// desktop app, one per host and Profile. → CONTEXT.md, Herdr Core Shell.

// HerdrCoreStateTarget is herdr-core's Unix state home ($XDG_STATE_HOME
// unset): where the Shell mounts its state volume. Off the Host Share on
// purpose: herdr-core keeps its sockets there when no runtime directory
// exists, and a Host Share bind cannot hold one.
const HerdrCoreStateTarget = "/home/toolbox/.local/state/herdr-core"

// HerdrMachineName is the machine a Herdr Core Shell registers as: toolbox,
// or toolbox-<profile>.
func HerdrMachineName(container string) string {
	return "toolbox" + strings.TrimPrefix(container, ContainerNamePrefix+config.HerdrCoreShell.Base)
}

// HerdrCoreContainerName is `toolbox-herdr`, or `toolbox-herdr-<profile>`.
func HerdrCoreContainerName(profile *mountplan.Profile) (string, error) {
	n, err := herdrCoreNames(profile)
	return n.container, err
}

func herdrCoreNames(profile *mountplan.Profile) (singletonNameSet, error) {
	return singletonNames(config.HerdrCoreShell, profile)
}

// herdrCoreWorkspace is the dir argument when given, else herdr_workspace.
func herdrCoreWorkspace(in PlanInput) (string, error) {
	return singletonWorkspace(in, config.HerdrCoreShell, in.Cfg.HerdrWorkspace)
}
