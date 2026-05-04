package mountplan

import (
	"path"
	"sort"
	"strings"

	"github.com/filippolmt/toolbox/internal/config"
)

// ParentDirs returns the distinct parent directories of mount targets
// under /home/toolbox/, excluding /home/toolbox itself. These are the dirs
// Docker would otherwise auto-create as root:root 0755 at runtime (as the
// parent of a bind mount), blocking the non-root runtime user from writing
// sibling subdirs — e.g. helm under ~/.config, starship under ~/.cache. The
// image must pre-create them (Dockerfile Layer 21). A Go test cross-checks
// the Dockerfile against this function so a new default mount can't
// silently regress the fix.
func ParentDirs(mounts []config.Mount) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range mounts {
		if !strings.HasPrefix(m.Target, config.HomeMountParents) {
			continue
		}
		parent := path.Dir(m.Target)
		if parent == strings.TrimSuffix(config.HomeMountParents, "/") {
			continue
		}
		if _, ok := seen[parent]; ok {
			continue
		}
		seen[parent] = struct{}{}
		out = append(out, parent)
	}
	sort.Strings(out)
	return out
}
