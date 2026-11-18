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

// DataVolume is a singleton Shell's data volume: a Docker volume that outlives
// the container, owned by the session user before any session binds it.
type DataVolume struct {
	// Name is the volume, Init its initialiser container (see singletonNameSet).
	Name, Init string
	// Target is where the Shell mounts it.
	Target string
	// Label names it in progress lines and errors.
	Label string
}

// singleton is what an Orca or Herdr Core Shell input fixes before the Mount
// Plan: its container name and data volume. Zero for an ordinary session.
type singleton struct {
	container string
	volume    DataVolume
}

func (n singletonNameSet) with(target, label string) singleton {
	return singleton{container: n.container, volume: DataVolume{Name: n.volume, Init: n.init, Target: target, Label: label}}
}

// singletonInputs resolves what an Orca or Herdr Core Shell input fixes before
// the Mount Plan: its names, its workspace, and the Orca Shell's port.
func singletonInputs(in *PlanInput) (singleton, error) {
	switch {
	case in.OrcaVersion != "":
		n, err := orcaNames(in.Profile)
		if err != nil {
			return singleton{}, err
		}
		if in.Workspace, err = orcaWorkspace(*in); err != nil {
			return singleton{}, err
		}
		in.Ports = append(slices.Clone(in.Ports), defaultHostIP.String()+":"+OrcaPort+":"+OrcaPort)
		return n.with(OrcaDataTarget, "Orca data volume"), nil
	case in.HerdrCore:
		n, err := herdrCoreNames(in.Profile)
		if err != nil {
			return singleton{}, err
		}
		if in.Workspace, err = herdrCoreWorkspace(*in); err != nil {
			return singleton{}, err
		}
		return n.with(HerdrCoreStateTarget, "herdr-core state volume"), nil
	}
	return singleton{}, nil
}
