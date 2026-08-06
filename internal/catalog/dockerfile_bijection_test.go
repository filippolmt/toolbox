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

// readDockerfile returns the embedded Dockerfile source. Sibling of
// readSmokeTest (smoke_test_literals_test.go) — the smoke test is read from
// disk, the Dockerfile from the embedded build context.
func readDockerfile(t *testing.T) string {
	t.Helper()
	data, err := fs.ReadFile(build.Assets, build.AssetDir+"/Dockerfile")
	if err != nil {
		t.Fatalf("read embedded Dockerfile: %v", err)
	}
	return string(data)
}

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
	dockerfile := readDockerfile(t)

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

// TestFetchStageCopyBijection asserts every `FROM fetch-base AS fetch-<x>`
// stage in the embedded Dockerfile has a matching `COPY --from=fetch-<x>` in
// the final stage, and vice versa. An unreferenced stage is silently skipped
// by BuildKit (the tool never lands in the image, nothing fails at build
// time); a COPY from a nonexistent stage fails the build but only after a
// full local rebuild. Same drift class the init.d bijection test covers.
// fetch-base itself is the shared parent and is exempt.
func TestFetchStageCopyBijection(t *testing.T) {
	dockerfile := readDockerfile(t)

	stageRE := regexp.MustCompile(`(?im)^FROM\s+\S+\s+AS\s+(fetch-[a-z0-9-]+)\s*$`)
	copyRE := regexp.MustCompile(`(?im)^COPY\s+(?:--\S+\s+)*--from=(fetch-[a-z0-9-]+)\s`)

	stages := map[string]bool{}
	for _, m := range stageRE.FindAllStringSubmatch(dockerfile, -1) {
		if m[1] != "fetch-base" {
			stages[m[1]] = true
		}
	}
	copies := map[string]bool{}
	for _, m := range copyRE.FindAllStringSubmatch(dockerfile, -1) {
		copies[m[1]] = true
	}

	if len(stages) == 0 {
		t.Fatal("no fetch-* stages found — regex or Dockerfile structure drifted")
	}

	var missing []string
	for s := range stages {
		if !copies[s] {
			missing = append(missing, s)
		}
	}
	sort.Strings(missing)
	for _, s := range missing {
		t.Errorf("stage %q has no COPY --from=%s in the final stage — the tool never lands in the image", s, s)
	}

	var orphan []string
	for c := range copies {
		if !stages[c] {
			orphan = append(orphan, c)
		}
	}
	sort.Strings(orphan)
	for _, c := range orphan {
		t.Errorf("COPY --from=%q references a stage that does not exist", c)
	}
}
