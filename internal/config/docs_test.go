package config

import (
	"os"
	"strings"
	"testing"
)

// TestEveryKeyHasAKeyReferenceRow: docs/configuration.md's key reference is the
// one table a developer reads to learn which keys exist. A key whose row is
// missing works but stays undiscovered, and no other test notices; a
// deprecated alias has no row of its own.
func TestEveryKeyHasAKeyReferenceRow(t *testing.T) {
	b, err := os.ReadFile("../../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for _, k := range Keys() {
		if k.Kind == KindAlias {
			continue
		}
		if !strings.Contains(doc, "| [`"+k.Name+"`]") {
			t.Errorf("docs/configuration.md has no key reference row for %q", k.Name)
		}
	}
}
