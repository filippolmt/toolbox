package sdd

import "testing"

// TestSkillFieldsMutex enforces the registry contract that GitignoreEntries
// (static fence) and ManifestPaths (manifest-driven fence) are mutually
// exclusive per skill. Co-populating both fields would leave the host and
// the entrypoint each owning the fence — guaranteed drift.
func TestSkillFieldsMutex(t *testing.T) {
	for _, s := range Skills {
		if len(s.GitignoreEntries) > 0 && len(s.ManifestPaths) > 0 {
			t.Errorf(
				"skill %q: GitignoreEntries and ManifestPaths are mutually exclusive (got %d and %d)",
				s.Key, len(s.GitignoreEntries), len(s.ManifestPaths),
			)
		}
	}
}

// TestExtraGitignoreEntriesRequireManifest enforces that
// ExtraGitignoreEntries is consumed only by the manifest-driven entrypoint
// regen path; populating it without ManifestPaths is dead config.
func TestExtraGitignoreEntriesRequireManifest(t *testing.T) {
	for _, s := range Skills {
		if len(s.ExtraGitignoreEntries) > 0 && len(s.ManifestPaths) == 0 {
			t.Errorf(
				"skill %q: ExtraGitignoreEntries (%d) requires ManifestPaths to be non-empty",
				s.Key, len(s.ExtraGitignoreEntries),
			)
		}
	}
}

// TestIsManifestManaged spot-checks the helper against the registry's
// current shape: gsd is manifest-driven, bmad and openspec are not.
func TestIsManifestManaged(t *testing.T) {
	cases := map[string]bool{
		"gsd":      true,
		"bmad":     false,
		"openspec": false,
	}
	for key, want := range cases {
		s, ok := Lookup(key)
		if !ok {
			t.Fatalf("skill %q not registered", key)
		}
		if got := s.IsManifestManaged(); got != want {
			t.Errorf("Lookup(%q).IsManifestManaged() = %v, want %v", key, got, want)
		}
	}
}

// TestSkillEnvKeyNewFields keeps the new env field constants aligned with
// the legacy ones: same prefix, same uppercase shape — the entrypoint
// decoder builds variable names from them by string concatenation.
func TestSkillEnvKeyNewFields(t *testing.T) {
	cases := map[string]string{
		EnvFieldManifests: "TOOLBOX_SDD_GSD_MANIFESTS",
		EnvFieldExtras:    "TOOLBOX_SDD_GSD_EXTRAS",
	}
	for field, want := range cases {
		if got := SkillEnvKey("gsd", field); got != want {
			t.Errorf("SkillEnvKey(\"gsd\", %q) = %q, want %q", field, got, want)
		}
	}
}
