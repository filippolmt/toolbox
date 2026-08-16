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
		if s == "" {
			// An empty token would only ever match a mount named "-…"; reject it
			// explicitly instead of relying on that naming coincidence.
			unknown = append(unknown, `""`)
			continue
		}
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

// profileHostSharedWarnings flags any post-merge mount left on the host
// ~/.toolbox/ root under an active profile — a user-declared credential mount
// the profile cannot auto-isolate. Toolbox-managed defaults are already
// retargeted into the profile root or intentionally shared (--share / bridge /
// ssh-git symlinks), so they are excluded; only a custom `mounts:` entry with
// an explicit ~/.toolbox/ source remains, and we surface it rather than
// silently leaking it or rewriting the user's explicit path.
func profileHostSharedWarnings(merged []config.Mount, profile *Profile) []string {
	if profile == nil {
		return nil
	}
	shared := profile.EffectiveShare()
	var out []string
	for _, m := range merged {
		switch {
		case m.SymlinkFrom != "": // ssh/gitconfig: host identity by design
		case !strings.HasPrefix(m.Source, mountsRootPrefix): // not under ~/.toolbox/
		case strings.HasPrefix(m.Source, profile.Root()+"/"): // already isolated
		case shareCovers(shared, m.Name): // intentionally kept on host (--share / bridge)
		default:
			out = append(out, fmt.Sprintf(
				"mount %q keeps its host source %q under profile %q (custom mounts are not auto-isolated); set a profile-specific source in mounts: if you want it isolated",
				m.Name, m.Source, profile.Name))
		}
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
		next, unknownName, err := mergeOne(out, nameIdx, u)
		if err != nil {
			return nil, err
		}
		out = next
		if unknownName != "" {
			unknown = append(unknown, unknownName)
		}
	}

	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("mounts: patch references unknown mount name(s): %s", strings.Join(unknown, ", "))
	}

	return dropDisabled(out), nil
}

// mergeOne folds a single user entry into acc, following the three rules in
// mergeMounts' doc comment, and returns the (possibly reallocated) slice. An
// unknown patch name is reported as unknownName rather than an error, so
// mergeMounts can gather every bad name into one message instead of failing on
// the first.
func mergeOne(acc []config.Mount, nameIdx map[string]int, u config.Mount) (out []config.Mount, unknownName string, err error) {
	switch {
	case u.Name != "" && u.Target == "":
		idx, ok := nameIdx[u.Name]
		if !ok {
			return acc, u.Name, nil
		}
		applyMountPatch(&acc[idx], u)
		return acc, "", nil

	case u.Name != "":
		if u.Source == "" {
			return nil, "", fmt.Errorf("mounts[%q]: source must not be empty when target is set", u.Name)
		}
		if idx, ok := nameIdx[u.Name]; ok {
			acc[idx] = u
			return acc, "", nil
		}
		nameIdx[u.Name] = len(acc)
		return append(acc, u), "", nil

	default:
		if u.Source == "" {
			return nil, "", fmt.Errorf("mounts: anonymous mount (target %q) must declare a non-empty source", u.Target)
		}
		return append(acc, u), "", nil
	}
}

// applyMountPatch overlays the non-zero fields of patch onto dst. Bool fields
// can only ever flip false→true: mapstructure cannot distinguish "not set" from
// an explicit false, so a patch adds flags but never clears them — the replace
// form (Name + Target) is how you turn one off.
func applyMountPatch(dst *config.Mount, patch config.Mount) {
	if patch.Source != "" {
		dst.Source = patch.Source
	}
	if patch.SymlinkFrom != "" {
		dst.SymlinkFrom = patch.SymlinkFrom
	}
	dst.ReadOnly = dst.ReadOnly || patch.ReadOnly
	dst.CreateIfMissing = dst.CreateIfMissing || patch.CreateIfMissing
	dst.Disabled = dst.Disabled || patch.Disabled
}

// dropDisabled removes every opted-out entry, so a user can disable a default
// (e.g. docker-sock) without redeclaring the rest of the list.
func dropDisabled(mounts []config.Mount) []config.Mount {
	final := make([]config.Mount, 0, len(mounts))
	for _, m := range mounts {
		if !m.Disabled {
			final = append(final, m)
		}
	}
	return final
}
