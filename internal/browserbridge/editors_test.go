package browserbridge

import "testing"

// TestEditorAppsCoversAllowlist enforces the bijection between the /edit
// allowlist and the macOS app-bundle fallback map: an editor added to one
// without the other either dead-ends the darwin `open -a` fallback or ships
// an unreachable mapping.
func TestEditorAppsCoversAllowlist(t *testing.T) {
	for editor := range editorAllowlist {
		if _, ok := editorApps[editor]; !ok {
			t.Errorf("editor %q allowlisted but missing from editorApps (darwin open -a fallback)", editor)
		}
	}
	for editor := range editorApps {
		if _, ok := editorAllowlist[editor]; !ok {
			t.Errorf("editor %q in editorApps but not allowlisted — unreachable mapping", editor)
		}
	}
}
