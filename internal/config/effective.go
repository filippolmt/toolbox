package config

// EffectiveValue returns the post-fallback value of a scalar schema key — the
// value the runtime actually uses when the key is unset (e.g. an empty `agent`
// resolves to DefaultAgent) — with ok=true only for the keys whose row declares
// an Effective fallback.
//
// It is the single seam answering "what does an unset key K resolve to";
// renderers (config show, the config UI) and consumers (worktree launch) SHALL
// derive from it instead of re-hardcoding the fallback, so the two renderers
// cannot drift on effective values.
//
// It returns ("", false) for every key whose row declares no fallback: the
// collection keys, the tri-state toggles whose effective value is host-derived,
// and the scalars (image / registry_mirror / mounts_root) whose empty value
// already *is* the effective one.
func EffectiveValue(c *Config, key string) (string, bool) {
	if c == nil {
		return "", false
	}
	k, ok := KeyByName(key)
	if !ok || k.Effective == nil {
		return "", false
	}
	return k.Effective(c), true
}
