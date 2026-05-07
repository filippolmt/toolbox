// Phase 10 Plan 01 — Wave 0 Go-side INIT-04 bijection test.
//
// Adapted from internal/catalog/dockerfile_bijection_test.go (the Phase 07
// CAT-04 pattern) — instead of regex-on-Dockerfile this test ReadDirs the
// embedded `assets/init.d/` subtree and asserts strict set-equality between
// the *.sh basenames present and the non-empty `Entry.InitScript` values
// declared in catalog.Entries.
//
// External-test-package form (`package catalog_test`) keeps internal/catalog
// production-side acyclic with internal/build (which depends on
// internal/catalog for WriteCanonical).

package catalog_test

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/catalog"
)

// TestCatalogInitDBijection enforces strict set-equality between
// catalog.Entries[*].InitScript (non-empty values) and the *.sh files shipped
// under internal/build/assets/init.d/ via the build.Assets embed.FS.
//
// Direction A: every catalog InitScript value MUST exist as a file in init.d/.
// Direction B: every *.sh file in init.d/ MUST have a matching catalog entry.
//
// Failure modes flagged:
//   - Catalog declares InitScript without a backing file → orphan declaration.
//   - File in init.d/ without a catalog declaration → unreachable script
//     (the entrypoint iterator runs everything regardless of catalog ownership,
//     but the bijection invariant exists so the catalog is the single
//     discoverable list of "what runs at boot").
func TestCatalogInitDBijection(t *testing.T) {
	entries, err := fs.ReadDir(build.Assets, build.AssetDir+"/init.d")
	if err != nil {
		t.Fatalf("read embedded init.d: %v", err)
	}

	fileSet := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		fileSet[e.Name()] = struct{}{}
	}

	catalogSet := map[string]struct{}{}
	for _, e := range catalog.Entries {
		if e.InitScript != "" {
			catalogSet[e.InitScript] = struct{}{}
		}
	}

	var missing, extra []string
	for n := range catalogSet {
		if _, ok := fileSet[n]; !ok {
			missing = append(missing, n)
		}
	}
	for n := range fileSet {
		if _, ok := catalogSet[n]; !ok {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	for _, n := range missing {
		t.Errorf("catalog declares InitScript=%q but internal/build/assets/init.d/%s does not exist", n, n)
	}
	for _, n := range extra {
		t.Errorf("internal/build/assets/init.d/%s exists but no catalog Entry has InitScript=%q", n, n)
	}
}
