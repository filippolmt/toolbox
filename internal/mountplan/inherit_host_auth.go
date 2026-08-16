package mountplan

import (
	"fmt"
	"os"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
)

// applyInheritHostAuth rewrites the default mount set so that every CLI key
// listed in keys reads (and writes) its credentials from the host's standard
// path instead of the isolated ~/.toolbox/<key>/ default. Mount is
// read-write because most CLIs refresh tokens / session state during normal
// use — atuin appends history every command, claude/codex update session
// state, gh/docker rotate OAuth refresh tokens. RO would EROFS those writes
// silently. Users opt in explicitly via inherit_host_auth: [...] knowing
// their host credential dir is now writable by container processes.
//
// For each key:
//   - The catalog Entry's HostAuthMount supplies HostPath + ContainerPath.
//   - The default mount matching the host-auth ContainerPath is dropped and
//     replaced with a new mount sharing the same Name, so user `mounts:`
//     patches keyed on the same name continue to address the same logical
//     mount.
//   - If the host source path does not exist, return an error (silent
//     soft-skip would leave the container with no credential mount at all).
//
// If no default mount matches the entry's ContainerPath, a new mount is
// appended — covers catalog entries whose host inheritance points at a path
// not otherwise mounted in the default set.
func applyInheritHostAuth(base []config.Mount, keys []string, home string) ([]config.Mount, error) {
	if len(keys) == 0 {
		return base, nil
	}
	// Resolve the catalog once: keys the catalog does not know, or that are
	// not host-auth eligible, are silently skipped.
	type inherited struct {
		key   string
		mount *catalog.HostAuthMount
	}
	var wanted []inherited
	dropTargets := make(map[string]string, len(keys)) // target → mount name to preserve
	for _, k := range keys {
		entry, ok := catalog.Find(k)
		if !ok || entry.HostAuthMount == nil {
			continue
		}
		wanted = append(wanted, inherited{key: k, mount: entry.HostAuthMount})
		dropTargets[entry.HostAuthMount.ContainerPath] = ""
	}

	out := make([]config.Mount, 0, len(base))
	for _, m := range base {
		if _, drop := dropTargets[m.Target]; drop {
			dropTargets[m.Target] = m.Name
			continue
		}
		out = append(out, m)
	}

	for _, w := range wanted {
		m, err := hostAuthMountFor(w.key, w.mount, dropTargets[w.mount.ContainerPath], home)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// hostAuthMountFor builds the replacement mount for one inherited key, reusing
// name (the dropped default's name, empty when there was none) so user
// `mounts:` patches keep addressing the same logical mount.
//
// The host source is pre-stat'ed so a missing path fails loud here instead of
// soft-skipping in resolveAll and leaving the container with no credential
// mount at all. When home is empty (UserHomeDir failed upstream) ExpandTilde
// leaves the ~ in place and os.Stat reports the path missing — which surfaces
// the misconfiguration.
func hostAuthMountFor(key string, ham *catalog.HostAuthMount, name, home string) (config.Mount, error) {
	if _, err := os.Stat(fsx.ExpandTilde(ham.HostPath, home)); err != nil {
		return config.Mount{}, fmt.Errorf(
			"inherit_host_auth: %q host path %q is not accessible: %w (initialise the CLI on the host first, or remove it from inherit_host_auth)",
			key, ham.HostPath, err)
	}
	if name == "" {
		name = key
	}
	return config.Mount{Name: name, Source: ham.HostPath, Target: ham.ContainerPath}, nil
}
