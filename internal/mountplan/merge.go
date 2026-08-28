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

// mountsRootJoin joins name onto a mounts root, defaulting an empty root to
// the ~/.toolbox/ prefix and tolerating either spelling of the trailing
// separator. The result stays tilde-relative, like every configured source:
// ExpandTilde runs later, at resolve time.
func mountsRootJoin(root, name string) string {
	if root == "" {
		root = mountsRootPrefix
	}
	return strings.TrimSuffix(root, "/") + "/" + name
}

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
	acc := newMountAccumulator(base)
	for _, u := range user {
		if err := acc.merge(u); err != nil {
			return nil, err
		}
	}
	if len(acc.unknown) > 0 {
		sort.Strings(acc.unknown)
		return nil, fmt.Errorf("mounts: patch references unknown mount name(s): %s", strings.Join(acc.unknown, ", "))
	}
	return dropDisabled(acc.mounts), nil
}

// mountAccumulator is the in-progress merge: the mount list, the name→index
// map that makes a patch lookup O(1), and the unknown patch names seen so far.
// The three are mutated together by every merge step — the list can reallocate,
// which the index must survive — so they are one value rather than three
// parameters threaded through a free function.
type mountAccumulator struct {
	mounts  []config.Mount
	nameIdx map[string]int
	unknown []string
}

// newMountAccumulator seeds the merge with a copy of base, indexed by name.
func newMountAccumulator(base []config.Mount) *mountAccumulator {
	a := &mountAccumulator{
		mounts:  make([]config.Mount, len(base)),
		nameIdx: make(map[string]int, len(base)),
	}
	copy(a.mounts, base)
	for i, m := range a.mounts {
		if m.Name != "" {
			a.nameIdx[m.Name] = i
		}
	}
	return a
}

// merge folds a single user entry in, following the three rules in mergeMounts'
// doc comment. A patch naming a mount that does not exist is recorded on
// a.unknown rather than returned as an error, so mergeMounts can report every
// bad name in one message instead of failing on the first.
func (a *mountAccumulator) merge(u config.Mount) error {
	switch {
	case u.Name != "" && u.Target == "":
		idx, ok := a.nameIdx[u.Name]
		if !ok {
			a.unknown = append(a.unknown, u.Name)
			return nil
		}
		applyMountPatch(&a.mounts[idx], u)

	case u.Name != "":
		if u.Source == "" {
			return fmt.Errorf("mounts[%q]: source must not be empty when target is set", u.Name)
		}
		if idx, ok := a.nameIdx[u.Name]; ok {
			a.mounts[idx] = u
			return nil
		}
		a.add(u)

	default:
		if u.Source == "" {
			return fmt.Errorf("mounts: anonymous mount (target %q) must declare a non-empty source", u.Target)
		}
		a.add(u)
	}
	return nil
}

// add appends a new mount, indexing it by name so a later patch can find it.
// Anonymous mounts carry no name and are simply appended.
func (a *mountAccumulator) add(u config.Mount) {
	if u.Name != "" {
		a.nameIdx[u.Name] = len(a.mounts)
	}
	a.mounts = append(a.mounts, u)
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
