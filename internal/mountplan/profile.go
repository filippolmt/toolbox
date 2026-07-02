package mountplan

// profilesRoot is the host parent under which each `toolbox shell --profile
// <name>` gets its own isolated ~/.toolbox/ mirror.
const profilesRoot = "~/.toolbox/profiles/"

// Profile is an active `toolbox shell --profile` selection: the isolated
// credential-root name plus the --share opt-out tokens. A nil *Profile means
// no profile (the default ~/.toolbox/ root). Bundling Name and Share in one
// type keeps the "share only under a profile" invariant in a single place —
// Share is meaningless without a Name.
type Profile struct {
	// Name is validated by the caller before construction: non-empty, no path
	// separator, not "." / "..". It becomes both the root sub-path and a
	// container-name discriminator.
	Name string
	// Share holds the --share tokens whose mounts stay on the host root even
	// under this profile.
	Share []string
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

// ProfileName returns p.Name, or "" for a nil profile — the container-name
// discriminator, safe to call without a nil check at the call site.
func ProfileName(p *Profile) string {
	if p == nil {
		return ""
	}
	return p.Name
}
