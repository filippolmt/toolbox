package catalog_test

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/catalog"
)

// keyTokenRE matches a catalog key as a whole word. `\b` anchors apply to
// both alternation branches so the underscore key `playwright_cli` only
// matches the exact tokens `playwright_cli` or `playwright-cli`, never a
// substring of an unrelated identifier.
func keyTokenRE(key string) *regexp.Regexp {
	dashed := strings.ReplaceAll(key, "_", "-")
	if dashed == key {
		return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(key) + `\b`)
	}
	return regexp.MustCompile(`(?i)(\b` + regexp.QuoteMeta(key) + `\b|\b` + regexp.QuoteMeta(dashed) + `\b)`)
}

// TestCatalogDockerfilePresence asserts every catalog Entry's Key appears
// as a whole-word token in the embedded Dockerfile. Catches the simple
// drift mode: a catalog entry whose install layer was forgotten.
//
// The reverse direction (orphan install layer in the Dockerfile without a
// catalog entry) cannot be reliably detected via regex over arbitrary
// install verbs (flag-tokens, multi-line continuations, and inline
// comments produce false positives faster than they catch real drift).
// The convention is: catalog is the source of truth — every CLI installed
// in the Dockerfile MUST be declared in catalog.Entries. Reviewers and the
// add-cli skill enforce this socially.
func TestCatalogDockerfilePresence(t *testing.T) {
	data, err := fs.ReadFile(build.Assets, build.AssetDir+"/Dockerfile")
	if err != nil {
		t.Fatalf("read embedded Dockerfile: %v", err)
	}
	dockerfile := string(data)

	var missing []string
	for _, e := range catalog.Entries {
		if !keyTokenRE(e.Key).MatchString(dockerfile) {
			missing = append(missing, e.Key)
		}
	}
	sort.Strings(missing)
	for _, k := range missing {
		t.Errorf("catalog entry %q has no token in the Dockerfile — add an install layer or remove the entry", k)
	}
}
