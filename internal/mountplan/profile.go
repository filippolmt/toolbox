package mountplan

import (
	"fmt"
	"sort"
	"strings"
)

// profilesRoot is the host parent under which each `toolbox shell --profile
// <name>` gets its own isolated ~/.toolbox/ mirror.
const profilesRoot = "~/.toolbox/profiles/"

// Profile is an active `toolbox shell --profile` selection: the isolated
// credential-root name plus the --share opt-out tokens. A nil *Profile means
// no profile (the default ~/.toolbox/ root). Bundling Name and Share in one
// type keeps the "share only under a profile" invariant in a single place —
// Share is meaningless without a Name.
type Profile struct {
	// Name is the profile identifier — validated by NewProfile (non-empty, no
	// path separator, not "." / ".."), since it becomes both a host filesystem
	// sub-path via Root() and a container-name discriminator.
	Name string
	// Share holds the --share tokens whose mounts stay on the host root even
	// under this profile.
	Share []string
}

// NewProfile validates name and returns the profile. Name is a trust boundary
// — it becomes a host path under ~/.toolbox/profiles/ — so path separators and
// the "." / ".." / empty cases are rejected here rather than trusting callers.
func NewProfile(name string, share []string) (*Profile, error) {
	if name == "" {
		return nil, fmt.Errorf("profile name must not be empty")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("profile name %q must not be '.', '..', or contain a path separator", name)
	}
	return &Profile{Name: name, Share: share}, nil
}

// Root is the mounts root a profile retargets ~/.toolbox/ sources to. It wins
// over a config-level mounts_root for the invocation.
func (p *Profile) Root() string { return profilesRoot + p.Name }

// EffectiveShare is the skip-set applyMountsRoot honours: the user's --share
// tokens plus "bridge". The bridge daemon dir is host infrastructure, not a
// per-account credential — retargeting it into the profile would bind empty
// dirs and break in-container URL/editor/proximo forwarding, so it always
// stays on the host root.
func (p *Profile) EffectiveShare() []string {
	return append(append([]string(nil), p.Share...), "bridge")
}

// ProfileName returns p.Name, or "" for a nil profile — safe to call without a
// nil check at the call site.
func ProfileName(p *Profile) string {
	if p == nil {
		return ""
	}
	return p.Name
}

// ContainerDiscriminator returns the string folded into a session's container
// identity so distinct profiles — and distinct --share sets within one profile
// — never share a container (mounts are fixed at ContainerCreate). Empty for a
// nil profile, reproducing the pre-profile container name. The share set is
// sorted so `--share gh,docker` and `--share docker,gh` map to one container.
func ContainerDiscriminator(p *Profile) string {
	if p == nil {
		return ""
	}
	share := p.EffectiveShare()
	sort.Strings(share)
	return p.Name + "\x00share=" + strings.Join(share, ",")
}
