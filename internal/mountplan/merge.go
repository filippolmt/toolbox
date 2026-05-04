package mountplan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/filippolmt/toolbox/internal/config"
)

// mountsRootPrefix is the source-path prefix that applyMountsRoot rewrites.
// Every default mount whose Source begins with this prefix is retargeted
// when the user sets mounts_root in their config.
const mountsRootPrefix = "~/.toolbox/"

// applyMountsRoot returns a copy of base with every Source under
// ~/.toolbox/ rewritten to live under root instead. Mounts whose Source
// is outside that prefix (e.g. /var/run/docker.sock) are left untouched,
// as is SymlinkFrom (which references the real host path, not the
// toolbox-managed mirror). Empty root returns base unchanged.
func applyMountsRoot(base []config.Mount, root string) []config.Mount {
	if root == "" {
		return base
	}
	// Strip a trailing slash so joining with the rest gives a clean path.
	trimmed := strings.TrimSuffix(root, "/")
	out := make([]config.Mount, len(base))
	copy(out, base)
	for i := range out {
		if !strings.HasPrefix(out[i].Source, mountsRootPrefix) {
			continue
		}
		rest := strings.TrimPrefix(out[i].Source, mountsRootPrefix)
		out[i].Source = trimmed + "/" + rest
	}
	return out
}

// mergeMounts combines a base mount set (typically defaults()) with a
// user-declared list, applying these rules per user entry:
//
//   - Name set, Target empty → patch the matching base entry. Only non-zero
//     user fields override the base; bool fields can flip false→true via the
//     patch but cannot flip true→false (mapstructure can't distinguish "not
//     set" from false). Use the replace form if you need that.
//   - Name set, Target set → if Name matches a base entry, replace it
//     entirely; otherwise append.
//   - Name empty → append (anonymous mount).
//
// After merging, any entry with Disabled=true is removed from the result so
// users can opt out of a default (e.g. docker-sock) without redeclaring the
// rest of the list. Patches referencing an unknown Name fail loudly.
func mergeMounts(base, user []config.Mount) ([]config.Mount, error) {
	out := make([]config.Mount, len(base))
	copy(out, base)
	nameIdx := map[string]int{}
	for i, m := range out {
		if m.Name != "" {
			nameIdx[m.Name] = i
		}
	}

	var unknown []string
	for _, u := range user {
		switch {
		case u.Name != "" && u.Target == "":
			idx, ok := nameIdx[u.Name]
			if !ok {
				unknown = append(unknown, u.Name)
				continue
			}
			if u.Source != "" {
				out[idx].Source = u.Source
			}
			if u.SymlinkFrom != "" {
				out[idx].SymlinkFrom = u.SymlinkFrom
			}
			if u.ReadOnly {
				out[idx].ReadOnly = true
			}
			if u.CreateIfMissing {
				out[idx].CreateIfMissing = true
			}
			if u.Disabled {
				out[idx].Disabled = true
			}
		case u.Name != "":
			if u.Source == "" {
				return nil, fmt.Errorf("mounts[%q]: source must not be empty when target is set", u.Name)
			}
			if idx, ok := nameIdx[u.Name]; ok {
				out[idx] = u
			} else {
				nameIdx[u.Name] = len(out)
				out = append(out, u)
			}
		default:
			if u.Source == "" {
				return nil, fmt.Errorf("mounts: anonymous mount (target %q) must declare a non-empty source", u.Target)
			}
			out = append(out, u)
		}
	}

	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("mounts: patch references unknown mount name(s): %s", strings.Join(unknown, ", "))
	}

	final := make([]config.Mount, 0, len(out))
	for _, m := range out {
		if m.Disabled {
			continue
		}
		final = append(final, m)
	}
	return final, nil
}
