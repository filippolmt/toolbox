package catalog_test

import (
	"io/fs"
	"regexp"
	"sort"
	"testing"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/catalog"
)

// argLineRE matches an `ARG INSTALL_<NAME>` line in the Dockerfile, with or
// without a `=true` / `=false` default. Capture group 1 is the suffix; the
// canonical ARG name is `INSTALL_<suffix>`. The pattern is anchored to the
// start of a line (multi-line mode) so commented-out lines (`# ARG …`) and
// `${INSTALL_…}` references inside RUN blocks are not matched.
var argLineRE = regexp.MustCompile(`(?m)^ARG INSTALL_([A-Z0-9_]+)(?:=(?:true|false))?\s*$`)

// TestCatalogDockerfileBijection enforces a strict set-equality between the
// `INSTALL_*` ARG names declared in the embedded Dockerfile and the BuildArg
// strings declared in catalog.Entries (CAT-04). Every catalog entry MUST have
// a matching Dockerfile ARG line and every `ARG INSTALL_*` line in the
// Dockerfile MUST map back to a catalog entry.
//
// Set semantics: the Dockerfile may legally repeat the same `ARG INSTALL_*`
// across stages (e.g. INSTALL_RTK currently appears at two stages because
// the rtk-builder stage scopes the value before it is re-declared in the
// runtime stage); duplicates collapse into a single set member. The dedupe
// pass in Plan 07-05 removes the redundant declaration but this test stays
// green either way.
//
// External-test-package form (`package catalog_test`) avoids the import
// cycle that would otherwise result: internal/build imports internal/catalog
// for WriteCanonical, and internal/catalog has no dependency on
// internal/build at the production level. Putting this test in the
// `catalog_test` package keeps that direction acyclic — the cycle is only
// permissible in a *_test package.
//
// The Dockerfile is read via build.Assets (the embed.FS already declared in
// internal/build/embed.go), not from disk, so the test is host-less and
// passes inside the `make go-test` golang container.
func TestCatalogDockerfileBijection(t *testing.T) {
	data, err := fs.ReadFile(build.Assets, build.AssetDir+"/Dockerfile")
	if err != nil {
		t.Fatalf("read embedded Dockerfile: %v", err)
	}

	// Build the Dockerfile-side set: every distinct `INSTALL_<X>` ARG name.
	dockerfileArgs := map[string]struct{}{}
	for _, m := range argLineRE.FindAllSubmatch(data, -1) {
		name := "INSTALL_" + string(m[1])
		dockerfileArgs[name] = struct{}{}
	}
	if len(dockerfileArgs) == 0 {
		t.Fatal("Dockerfile contains no `ARG INSTALL_*` lines — regex or embedded asset is wrong")
	}

	// Build the catalog-side set: every Entry.BuildArg.
	catalogArgs := map[string]struct{}{}
	for _, e := range catalog.Entries {
		catalogArgs[e.BuildArg] = struct{}{}
	}

	// Direction 1: catalog ⊆ Dockerfile.
	var missingFromDockerfile []string
	for name := range catalogArgs {
		if _, ok := dockerfileArgs[name]; !ok {
			missingFromDockerfile = append(missingFromDockerfile, name)
		}
	}
	sort.Strings(missingFromDockerfile)
	for _, name := range missingFromDockerfile {
		t.Errorf("catalog declares %q but no `ARG %s` line exists in the Dockerfile — add an install layer or remove the catalog entry", name, name)
	}

	// Direction 2: Dockerfile ⊆ catalog.
	var missingFromCatalog []string
	for name := range dockerfileArgs {
		if _, ok := catalogArgs[name]; !ok {
			missingFromCatalog = append(missingFromCatalog, name)
		}
	}
	sort.Strings(missingFromCatalog)
	for _, name := range missingFromCatalog {
		t.Errorf("Dockerfile declares `ARG %s` but no catalog Entry has BuildArg=%q — add an Entry to internal/catalog/catalog.go::Entries or remove the Dockerfile ARG", name, name)
	}
}
