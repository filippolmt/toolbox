package config

// EffectiveValue returns the post-fallback value of a scalar schema key — the
// value the runtime actually uses when the key is unset (e.g. an empty `agent`
// resolves to DefaultAgent) — with ok=true only for the fallback-bearing
// scalars this accessor owns: shell, agent, pull.
//
// It is the single seam answering "what does an unset key K resolve to";
// renderers (config show, the config UI) and consumers (worktree launch) SHALL
// derive from it instead of re-hardcoding the fallback, so the two renderers
// cannot drift on effective values.
//
// It returns ("", false) for the keys it deliberately does not own — collection
// keys, the no-fallback scalars (image / registry_mirror / mounts_root), and the
// host-derived tri-state toggles — each enumerated with its reason in
// TestEffectiveValueCoversSchema's exempt table, the single place that
// classification lives.
func EffectiveValue(c *Config, key string) (string, bool) {
	if c == nil {
		return "", false
	}
	switch key {
	case "shell":
		return orElse(c.Shell, SupportedShells[0]), true
	case "agent":
		return orElse(c.Agent, DefaultAgent), true
	case "pull":
		return orElse(c.Pull, PullAuto), true
	}
	return "", false
}

// orElse returns v, or def when v is empty.
func orElse(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
