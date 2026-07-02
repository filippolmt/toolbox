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
//
// A mount whose Name is covered by the shared skip-set (see shareCovers) is
// left on its ~/.toolbox/ source even when root is set — the profile-level
// opt-out that keeps a tool shared with the host (see cmd `--share`).
func applyMountsRoot(base []config.Mount, root string, shared []string) []config.Mount {
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
		if shareCovers(shared, out[i].Name) {
			continue
		}
		rest := strings.TrimPrefix(out[i].Source, mountsRootPrefix)
		out[i].Source = trimmed + "/" + rest
	}
	return out
}

// matchesShareToken reports whether a --share token covers the mount named
// name: an exact match, or a "<token>-" prefix so a single token covers a
// tool's split mounts (e.g. "cf" covers "cf-auth"/"cf-config", "rtk" covers
// "rtk"/"rtk-data"). The one place the token-matching rule lives.
func matchesShareToken(token, name string) bool {
	return token == name || strings.HasPrefix(name, token+"-")
}

// shareCovers reports whether any --share token keeps the mount named name on
// the host root.
func shareCovers(shared []string, name string) bool {
	for _, s := range shared {
		if matchesShareToken(s, name) {
			return true
		}
	}
	return false
}

// validateShare rejects --share tokens that match no retargetable mount in
// base, so a typo (e.g. "ghh") fails loudly instead of silently isolating
// everything. Only mounts whose Source lives under ~/.toolbox/ and are not
// SymlinkFrom identity mounts (ssh/gitconfig — always host-shared, not
// selectable) count as shareable.
func validateShare(base []config.Mount, shared []string) error {
	var unknown []string
	for _, s := range shared {
		matched := false
		for _, m := range base {
			if m.SymlinkFrom != "" || !strings.HasPrefix(m.Source, mountsRootPrefix) {
				continue
			}
			if matchesShareToken(s, m.Name) {
				matched = true
				break
			}
		}
		if !matched {
			unknown = append(unknown, s)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("share references unknown or non-shareable mount name(s): %s", strings.Join(unknown, ", "))
	}
	return nil
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
