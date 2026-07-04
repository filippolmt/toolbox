package config

import "testing"

// TestKeyDocsCoverSchema is the anti-drift guard: every SchemaKeys() field
// except the deprecated browser_bridge alias must have a KeyDoc with a
// non-empty Summary and Default, so a new config key can't ship undocumented.
func TestKeyDocsCoverSchema(t *testing.T) {
	docs := KeyDocs()
	for _, key := range SchemaKeys() {
		if key == "browser_bridge" {
			if _, ok := docs[key]; ok {
				t.Errorf("deprecated key %q must not have a KeyDoc", key)
			}
			continue
		}
		d, ok := docs[key]
		if !ok {
			t.Errorf("KeyDocs missing key %q", key)
			continue
		}
		if d.Summary == "" {
			t.Errorf("KeyDoc %q has empty Summary", key)
		}
		if d.Default == "" {
			t.Errorf("KeyDoc %q has empty Default", key)
		}
	}
}
