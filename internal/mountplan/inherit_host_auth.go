package mountplan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
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
	out := make([]config.Mount, 0, len(base))
	dropTargets := make(map[string]string, len(keys)) // target → mount name to preserve
	for _, k := range keys {
		entry, ok := catalog.Find(k)
		if !ok || entry.HostAuthMount == nil {
			continue
		}
		dropTargets[entry.HostAuthMount.ContainerPath] = ""
	}
	for _, m := range base {
		if _, drop := dropTargets[m.Target]; drop {
			dropTargets[m.Target] = m.Name
			continue
		}
		out = append(out, m)
	}
	for _, k := range keys {
		entry, ok := catalog.Find(k)
		if !ok || entry.HostAuthMount == nil {
			continue
		}
		// Pre-stat host source so a missing path fails loud instead of
		// silently soft-skipping the mount in resolveAll.
		expanded := entry.HostAuthMount.HostPath
		if home != "" && strings.HasPrefix(expanded, "~/") {
			expanded = filepath.Join(home, expanded[2:])
		}
		if _, err := os.Stat(expanded); err != nil {
			return nil, fmt.Errorf(
				"inherit_host_auth: %q host path %q is not accessible: %w (initialise the CLI on the host first, or remove it from inherit_host_auth)",
				k, entry.HostAuthMount.HostPath, err)
		}
		name := dropTargets[entry.HostAuthMount.ContainerPath]
		if name == "" {
			name = k
		}
		out = append(out, config.Mount{
			Name:   name,
			Source: entry.HostAuthMount.HostPath,
			Target: entry.HostAuthMount.ContainerPath,
		})
	}
	return out, nil
}
