package config

import (
	"slices"
	"testing"
)

// TestSchemaKeys pins the full field set in declaration order. It is
// deliberately exact: adding or renaming a Config field forces an update here,
// which is the prompt to also account for the new field in validation, the
// resolved renderer, the annotated example, and provenance (each guarded
// against SchemaKeys elsewhere).
func TestSchemaKeys(t *testing.T) {
	want := []string{
		"mounts", "inherit_host_auth", "shells", "shell", "agent",
		"image", "registry_mirror", "pull", "mounts_root", "sdd",
		"bridge", "browser_bridge", "proximo", "managed_statusline", "image_reclaim",
		"env", "peer_messaging", "worktree",
	}
	if got := SchemaKeys(); !slices.Equal(got, want) {
		t.Errorf("SchemaKeys drifted:\n got %v\nwant %v", got, want)
	}
}

// TestValidatorsCoverSchema is the anti-drift guard for validation: every
// schema key must either have a validator or be explicitly exempted. A new
// field that is neither turns this red instead of shipping unvalidated.
func TestValidatorsCoverSchema(t *testing.T) {
	validated := make(map[string]bool, len(fieldValidators))
	for _, v := range fieldValidators {
		if validated[v.key] {
			t.Errorf("duplicate validator for key %q", v.key)
		}
		validated[v.key] = true
	}
	for _, key := range SchemaKeys() {
		if !validated[key] && !noValidationKeys[key] {
			t.Errorf("config key %q has no validator and is not in noValidationKeys — classify it", key)
		}
	}
	// No stale entries: every validator/exemption must name a real schema key.
	schema := make(map[string]bool, len(SchemaKeys()))
	for _, k := range SchemaKeys() {
		schema[k] = true
	}
	for key := range validated {
		if !schema[key] {
			t.Errorf("validator names unknown key %q", key)
		}
	}
	for key := range noValidationKeys {
		if !schema[key] {
			t.Errorf("noValidationKeys names unknown key %q", key)
		}
	}
}
