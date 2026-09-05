package configedit

import (
	"fmt"
	"path/filepath"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configio"
)

// Where selects which config file a writer command targets.
type Where int

const (
	// WhereGlobal targets ~/.toolbox.yaml (the default — shells/mounts are
	// naturally per-user).
	WhereGlobal Where = iota
	// WhereLocal targets the walked-up project .toolbox.yaml, creating
	// ./.toolbox.yaml in cwd when no walked-up file exists.
	WhereLocal
)

// ParseWhere maps the --where flag value onto a Where. Only "global" and
// "local" are accepted; the error enumerates both so the user can
// copy-paste a fix.
func ParseWhere(s string) (Where, error) {
	switch s {
	case "global":
		return WhereGlobal, nil
	case "local":
		return WhereLocal, nil
	default:
		return 0, fmt.Errorf("invalid --where %q: must be \"global\" or \"local\"", s)
	}
}

// Resolve returns the config-file path a writer should patch. global →
// configio.GlobalConfigPath(). local → the walked-up project file when one
// exists (patching in place avoids a stray ./.toolbox.yaml shadowing it),
// else ./.toolbox.yaml in cwd.
func Resolve(w Where, cwd string) (string, error) {
	switch w {
	case WhereGlobal:
		return configio.GlobalConfigPath()
	case WhereLocal:
		if path := config.WalkUpProjectConfig(cwd); path != "" {
			return path, nil
		}
		return filepath.Join(cwd, ".toolbox.yaml"), nil
	default:
		return "", fmt.Errorf("internal: unknown Where %d", w)
	}
}

// workspaceOnlyKeys lists the Config Schema keys whose effect is anchored to
// the workspace, so writing them into the global layer produces a flag with no
// coherent meaning. sdd is one: its install sentinel is keyed by the workspace
// hash, its artefacts materialise under /workspace, and its .gitignore fence
// has no global expression at all — a global flag would fence exactly the one
// repo the writer happened to be standing in.
//
// Adding a key here is not free: configui's reset reads this set to decide
// whether to reconcile the SDD .gitignore fences, so a second member needs its
// own artefact handling there rather than inheriting sdd's.
var workspaceOnlyKeys = map[string]bool{"sdd": true}

// WorkspaceOnlyKey reports whether a key may only be written to the workspace
// layer (WhereLocal). It is the single authority the write surfaces ask —
// the config UI before opening an editor, its reset before reconciling fences
// — rather than each restating which keys those are.
func WorkspaceOnlyKey(key string) bool { return workspaceOnlyKeys[key] }
