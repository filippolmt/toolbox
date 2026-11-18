package sessionplan

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/workspace"
)

// Singleton Shell: a Detached Shell kept one per host and Profile under a
// fixed name, with its own data volume (the Orca Shell). The parts below are
// what such a Shell shares; its name and wording are the caller's.

var hashShapedRe = regexp.MustCompile(fmt.Sprintf(`^[a-f0-9]{%d}$`, WorkspaceHashLen))

// singletonNameSet is every name a singleton Shell owns, from one profile check.
type singletonNameSet struct {
	container string
	// volume is the data volume: `toolbox-<base>-data`, or
	// `toolbox-<base>-data-<profile>`, so each Profile keeps its own state.
	// It outlives the container.
	volume string
	// init is the volume's initialiser container, `<volume>_init`. The
	// underscore is what no sanitized Profile suffix holds, so it never
	// equals a singleton Shell's name (`toolbox-<base>-data-init` is the
	// Shell of a Profile named data-init).
	init string
}

// singletonNames names the Shell `toolbox-<base>`, or `toolbox-<base>-<profile>`.
// A profile suffix shaped like the workspace hash is refused:
// `toolbox-<base>-<8hex>` is the workspace container of a directory named
// <base>.
func singletonNames(shell config.SingletonShell, profile *mountplan.Profile) (singletonNameSet, error) {
	suffix := ""
	if pn := mountplan.ProfileName(profile); pn != "" {
		suffix = SanitizeShellName(pn)
		if suffix == "" || hashShapedRe.MatchString(suffix) {
			return singletonNameSet{}, fmt.Errorf("profile %q cannot name %s: it is empty once sanitized or shaped like a workspace hash; pick a different profile name", pn, shell.Article+" "+shell.Label)
		}
		suffix = "-" + suffix
	}
	volume := ContainerNamePrefix + shell.Base + "-data" + suffix
	return singletonNameSet{container: ContainerNamePrefix + shell.Base + suffix, volume: volume, init: volume + "_init"}, nil
}

// singletonWorkspace is the dir argument when given, else the configured key
// expanded against the declared host's home. Both routes pass the one check:
// a colon-free absolute path to an existing directory that is not the home
// itself (the Shell would mount the whole host home). configured is the
// value of shell's workspace key.
func singletonWorkspace(in PlanInput, shell config.SingletonShell, configured string) (string, error) {
	command := shell.Command
	raw := in.Workspace
	if raw == "" {
		if configured == "" {
			return "", errors.New(command + " has no workspace: set " + shell.Key + " in ~/.toolbox.yaml or pass a dir argument")
		}
		raw = in.Host.Expand(configured)
	}
	ws, err := workspace.ResolveExplicit(raw)
	if err != nil {
		return "", fmt.Errorf("%s workspace: %w", command, err)
	}
	if ws == filepath.Clean(in.Host.Home) {
		return "", fmt.Errorf("%s workspace %s is the home directory: it would mount the whole host home into %s; name the directory that holds your repositories", command, ws, "the "+shell.Label)
	}
	return ws, nil
}

// singletonInputs resolves what an Orca or Herdr Core Shell input fixes before
// the Mount Plan: its names, its workspace, and the Orca Shell's port. Zero
// name sets for an ordinary session.
func singletonInputs(in *PlanInput) (orcaN, herdrN singletonNameSet, err error) {
	if in.OrcaVersion != "" {
		if orcaN, err = orcaNames(in.Profile); err != nil {
			return orcaN, herdrN, err
		}
		if in.Workspace, err = orcaWorkspace(*in); err != nil {
			return orcaN, herdrN, err
		}
		in.Ports = append(slices.Clone(in.Ports), defaultHostIP.String()+":"+OrcaPort+":"+OrcaPort)
	}
	if in.HerdrCore {
		if herdrN, err = herdrCoreNames(in.Profile); err != nil {
			return orcaN, herdrN, err
		}
		in.Workspace, err = herdrCoreWorkspace(*in)
	}
	return orcaN, herdrN, err
}
