package sdd

import "testing"

// TestSkillEnvKey locks in the encode contract: SkillEnvKey concatenates
// the prefix + UPPERCASE(key) + field with no separator drift between the
// encode site (sessionplan.sddEnv) and the decode site (bash loop in
// entrypoint.sh).
func TestSkillEnvKey(t *testing.T) {
	cases := map[string]string{
		EnvFieldPkg:     "TOOLBOX_SDD_GSD_PKG",
		EnvFieldVersion: "TOOLBOX_SDD_GSD_VERSION",
		EnvFieldBin:     "TOOLBOX_SDD_GSD_BIN",
		EnvFieldSteps:   "TOOLBOX_SDD_GSD_STEPS",
		EnvFieldMarker:  "TOOLBOX_SDD_GSD_MARKER",
	}
	for field, want := range cases {
		if got := SkillEnvKey("gsd", field); got != want {
			t.Errorf("SkillEnvKey(\"gsd\", %q) = %q, want %q", field, got, want)
		}
	}
}

// TestLookupReturnsRegisteredSkills spot-checks the registry surface:
// every documented key is reachable via Lookup, and an unknown key fails.
func TestLookupReturnsRegisteredSkills(t *testing.T) {
	for _, key := range []string{"bmad", "gsd", "openspec"} {
		if _, ok := Lookup(key); !ok {
			t.Errorf("Lookup(%q) reported missing — registry drifted", key)
		}
	}
	if _, ok := Lookup("nonsense"); ok {
		t.Error("Lookup(nonsense) reported present — should be empty")
	}
}

// TestKeysMatchesSkills asserts Keys() mirrors the Skills slice in order.
// The cmd/sdd.go usage error formats Keys() into its message, so a silent
// reorder would change user-visible output.
func TestKeysMatchesSkills(t *testing.T) {
	keys := Keys()
	if len(keys) != len(Skills) {
		t.Fatalf("Keys() len=%d, Skills len=%d", len(keys), len(Skills))
	}
	for i, s := range Skills {
		if keys[i] != s.Key {
			t.Errorf("Keys()[%d] = %q, want %q", i, keys[i], s.Key)
		}
	}
}
