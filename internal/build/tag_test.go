package build

import "testing"

// TestResolveImageReturnsCanonicalTag asserts the single canonical image
// contract.
func TestResolveImageReturnsCanonicalTag(t *testing.T) {
	if got := ResolveImage(); got != DefaultRegistryImage {
		t.Errorf("ResolveImage() = %q, want %q", got, DefaultRegistryImage)
	}
}
