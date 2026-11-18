package sessionplan

import (
	"path"
	"path/filepath"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// Orca Shell: the Detached Shell that hosts Orca's runtime, one per host and
// Profile. → CONTEXT.md, Orca Shell.
const (
	// OrcaPort is the port Orca's server listens on in the container and the
	// one published on host loopback. init.d/80-orca.sh repeats it as
	// --port; TestOrcaInitInstallsVerifiesAndStarts holds the two equal.
	OrcaPort = "6768"
	// OrcaVersionEnv is the env gate the init script reads: only the Orca
	// Shell carries it, and its value is the release tag to run.
	OrcaVersionEnv = "TOOLBOX_ORCA_VERSION"
	// OrcaDataTarget is where the Orca Shell mounts its data volume and what
	// ORCA_USER_DATA_PATH names. Outside the state mount on purpose: Orca
	// creates its Unix sockets in its data directory, and a Host Share bind
	// cannot hold one (see mountplan.PeerSocketVolumeName).
	OrcaDataTarget = "/home/toolbox/.orca-data"
	// orcaLogEnv hands the init script the container-side serve.log path.
	orcaLogEnv = "TOOLBOX_ORCA_LOG"
	// orcaLogDir is the server log's directory, relative to the state mount
	// on both sides of the bind: the host reads the log there.
	orcaLogDir = "orca"
	// orcaLogName is the server log the init script appends to, in orcaLogDir.
	orcaLogName = "serve.log"
)

// OrcaContainerName is `toolbox-orca`, or `toolbox-orca-<profile>`. A profile
// suffix shaped like the workspace hash is refused: `toolbox-orca-<8hex>` is
// the workspace container of a directory named "orca".
func OrcaContainerName(profile *mountplan.Profile) (string, error) {
	n, err := orcaNames(profile)
	return n.container, err
}

// orcaNames is every name an Orca Shell owns: its container, its data volume
// (one per Profile, keeping its own pairing identity) and that volume's
// initialiser.
func orcaNames(profile *mountplan.Profile) (singletonNameSet, error) {
	return singletonNames(config.OrcaShell, profile)
}

// orcaWorkspace is the dir argument when given, else orca_workspace.
func orcaWorkspace(in PlanInput) (string, error) {
	return singletonWorkspace(in, config.OrcaShell, in.Cfg.OrcaWorkspace)
}

// orcaDetached: the Orca Shell starts detached unless a terminal asked for it.
func orcaDetached(in PlanInput) bool {
	return in.OrcaVersion != "" && !in.OrcaAttach
}

// orcaEnv is the gate, Orca's data directory on its volume and the server
// log on the state mount.
func orcaEnv(version string) []string {
	return []string{
		OrcaVersionEnv + "=" + version,
		"ORCA_USER_DATA_PATH=" + OrcaDataTarget,
		orcaLogEnv + "=" + path.Join(mountplan.StateMountTarget, orcaLogDir, orcaLogName),
	}
}

// orcaDataBind mounts the data volume. A session input appended after the
// Mount Plan, like the peer socket bind: no `mounts:` patch can retarget it.
func orcaDataBind(volume string) mountplan.Bind {
	return mountplan.Bind{Source: volume, Target: OrcaDataTarget, Mode: "rw"}
}

// OrcaLogPath is the host-side path of the Orca server log: the state mount's
// host source plus the same relative path the container writes. Empty when
// the plan carries no state mount.
func (p *SessionPlan) OrcaLogPath() string {
	if p.StateDir == "" {
		return ""
	}
	return filepath.Join(p.StateDir, orcaLogDir, orcaLogName)
}
