package sessionplan

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"

	"github.com/filippolmt/toolbox/internal/mountplan"
)

// Orca Shell: the Detached Shell that hosts Orca's runtime, one per host and
// Profile. → CONTEXT.md, Orca Shell.
const (
	// OrcaPort is the port Orca's server listens on in the container and the
	// one published on host loopback.
	OrcaPort = "6768"
	// OrcaVersionEnv is the env gate the init script reads: only the Orca
	// Shell carries it, and its value is the release tag to run.
	OrcaVersionEnv = "TOOLBOX_ORCA_VERSION"
	// orcaDataDir is Orca's data directory, relative to the state mount on
	// both sides of the bind.
	orcaDataDir = "orca"
	// orcaLogName is the server log the init script appends to, in orcaDataDir.
	orcaLogName = "serve.log"
)

var hashShapedRe = regexp.MustCompile(fmt.Sprintf(`^[a-f0-9]{%d}$`, WorkspaceHashLen))

// OrcaContainerName is `toolbox-orca`, or `toolbox-orca-<profile>`. A profile
// suffix shaped like the workspace hash is refused: `toolbox-orca-<8hex>` is
// the workspace container of a directory named "orca".
func OrcaContainerName(profile *mountplan.Profile) (string, error) {
	name := ContainerNamePrefix + "orca"
	pn := mountplan.ProfileName(profile)
	if pn == "" {
		return name, nil
	}
	suffix := SanitizeShellName(pn)
	if suffix == "" || hashShapedRe.MatchString(suffix) {
		return "", fmt.Errorf("profile %q cannot name an Orca Shell: it is empty once sanitized or shaped like a workspace hash; pick a different profile name", pn)
	}
	return name + "-" + suffix, nil
}

// orcaWorkspace is the dir argument when given, else orca_workspace expanded
// against the declared host's home.
func orcaWorkspace(in PlanInput) (string, error) {
	if in.Workspace != "" {
		return in.Workspace, nil
	}
	if in.Cfg.OrcaWorkspace == "" {
		return "", errors.New("toolbox orca has no workspace: set orca_workspace in ~/.toolbox.yaml or pass a dir argument")
	}
	return in.Host.Expand(in.Cfg.OrcaWorkspace), nil
}

// orcaEnv is the gate plus Orca's data directory on the state mount.
func orcaEnv(version string) []string {
	return []string{
		OrcaVersionEnv + "=" + version,
		"ORCA_USER_DATA_PATH=" + path.Join(mountplan.StateMountTarget, orcaDataDir),
	}
}

// OrcaLogPath is the host-side path of the Orca server log: the state mount's
// host source plus the same relative path the container writes. Empty when
// the plan carries no state mount.
func (p *SessionPlan) OrcaLogPath() string {
	if p.StateDir == "" {
		return ""
	}
	return filepath.Join(p.StateDir, orcaDataDir, orcaLogName)
}
