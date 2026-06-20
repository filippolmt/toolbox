// Bijection test: catalog.Entry.InitScript ↔ assets/init.d/*.sh.
// External test package (catalog_test) keeps the production import graph
// acyclic — internal/build depends on internal/catalog for WriteCanonical.

package catalog_test

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/catalog"
)

// systemInitScripts lists init.d/*.sh files that are NOT tied to a catalog
// tool entry — they implement system-level features driven by host-CLI
// flags or always-on infrastructure. Each entry MUST be present on disk
// (TestSystemInitScriptsPresent locks that direction) but has no matching
// catalog.Entry.InitScript. Keep this set tiny — every addition is an
// implicit carve-out from the "catalog is the single discoverable list of
// what runs at boot" invariant.
var systemInitScripts = map[string]struct{}{
	// `toolbox shell -B` loopback bridge — host-CLI flag, not a tool toggle.
	"70-loopback-bridge.sh": {},
	// LSP version-drift heal — system hygiene, not a tool toggle.
	"15-npm-lsp-dedupe.sh": {},
	// Managed Claude Code statusline — image-owned policy, not a tool toggle.
	"35-statusline.sh": {},
	// Reconcile ponytail/caveman mode flags with enabledPlugins — image policy.
	"36-mode-flags.sh": {},
}

// TestCatalogInitDBijection enforces strict set-equality between
// (catalog.Entries[*].InitScript ∪ systemInitScripts) and the *.sh files
// shipped under internal/build/assets/init.d/. The entrypoint iterator
// globs every file regardless of catalog membership; this invariant exists
// so the catalog (plus the explicit systemInitScripts carve-out) stays the
// single discoverable list of "what runs at boot".
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
	for name := range systemInitScripts {
		catalogSet[name] = struct{}{}
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
