package sessionplan

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"

	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/workspace"
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

var hashShapedRe = regexp.MustCompile(fmt.Sprintf(`^[a-f0-9]{%d}$`, WorkspaceHashLen))

// OrcaContainerName is `toolbox-orca`, or `toolbox-orca-<profile>`. A profile
// suffix shaped like the workspace hash is refused: `toolbox-orca-<8hex>` is
// the workspace container of a directory named "orca".
func OrcaContainerName(profile *mountplan.Profile) (string, error) {
	n, err := orcaNames(profile)
	return n.container, err
}

// orcaNameSet is every name an Orca Shell owns, from one profile check.
type orcaNameSet struct {
	container string
	// volume is the data volume: `toolbox-orca-data`, or
	// `toolbox-orca-data-<profile>`, so each Profile keeps its own pairing
	// identity. It outlives the container.
	volume string
	// init is the volume's initialiser container, `<volume>_init`. The
	// underscore is what no sanitized Profile suffix holds, so it never
	// equals an Orca Shell's name (`toolbox-orca-data-init` is the Orca
	// Shell of a Profile named data-init).
	init string
}

func orcaNames(profile *mountplan.Profile) (orcaNameSet, error) {
	suffix := ""
	if pn := mountplan.ProfileName(profile); pn != "" {
		suffix = SanitizeShellName(pn)
		if suffix == "" || hashShapedRe.MatchString(suffix) {
			return orcaNameSet{}, fmt.Errorf("profile %q cannot name an Orca Shell: it is empty once sanitized or shaped like a workspace hash; pick a different profile name", pn)
		}
		suffix = "-" + suffix
	}
	volume := ContainerNamePrefix + "orca-data" + suffix
	return orcaNameSet{container: ContainerNamePrefix + "orca" + suffix, volume: volume, init: volume + "_init"}, nil
}

// orcaWorkspace is the dir argument when given, else orca_workspace expanded
// against the declared host's home. Both routes pass the one check: a
// colon-free absolute path to an existing directory that is not the home
// itself (the Orca Shell would mount the whole host home).
func orcaWorkspace(in PlanInput) (string, error) {
	raw := in.Workspace
	if raw == "" {
		if in.Cfg.OrcaWorkspace == "" {
			return "", errors.New("toolbox orca has no workspace: set orca_workspace in ~/.toolbox.yaml or pass a dir argument")
		}
		raw = in.Host.Expand(in.Cfg.OrcaWorkspace)
	}
	ws, err := workspace.ResolveExplicit(raw)
	if err != nil {
		return "", fmt.Errorf("toolbox orca workspace: %w", err)
	}
	if ws == filepath.Clean(in.Host.Home) {
		return "", fmt.Errorf("toolbox orca workspace %s is the home directory: it would mount the whole host home into the Orca Shell; name the directory that holds your repositories", ws)
	}
	return ws, nil
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
