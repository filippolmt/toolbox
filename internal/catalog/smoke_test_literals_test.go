// Drift guards for the integer literals in assets/smoke-test.sh.
// TestCatalogInitDBijection proves catalog↔disk SET equality; the smoke test
// additionally hardcodes *counts*, which no runtime check can bump for you.
// These tests pin each literal to its derivable source of truth — the catalog
// and the embedded assets — so a forgotten bump fails `make go-test` instead
// of slipping into the image.
//
// smoke-test.sh is read from disk (it is deliberately NOT embedded — embed.go
// ships only the Docker build context; the smoke test runs from a checkout in
// CI). Go runs tests with the package directory as CWD, so the relative path
// is stable. The Dockerfile, being part of that build context, is read from
// build.Assets instead.

package catalog_test

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/catalog"
)

const smokeTestPath = "../build/assets/smoke-test.sh"

func readSmokeTest(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(smokeTestPath))
	if err != nil {
		t.Fatalf("read smoke-test.sh: %v", err)
	}
	return string(b)
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("parse int %q: %v", s, err)
	}
	return n
}

// TestSmokeTestInitDCountLiteral pins the three init.d integer literals in
// smoke-test.sh to derivable truth: the `-ne N` gate, plus the "N (M catalog
// InitScripts + K system …)" message. Total must equal the number of
// init.d/*.sh files in the embedded assets; the M/K split must match the
// catalog InitScript count and the systemInitScripts carve-out.
func TestSmokeTestInitDCountLiteral(t *testing.T) {
	src := readSmokeTest(t)

	wantTotal := countInitDFiles(t)
	wantCatalog := countCatalogInitScripts()
	wantSystem := len(systemInitScripts)

	gate := regexp.MustCompile(`"\$count"\s+-ne\s+(\d+)`).FindStringSubmatch(src)
	if gate == nil {
		t.Fatal(`init.d count gate ("$count" -ne N) not found in smoke-test.sh`)
	}
	if got := mustAtoi(t, gate[1]); got != wantTotal {
		t.Errorf("smoke-test.sh init.d `-ne` gate = %d, want %d (init.d/*.sh files); bump the literal", got, wantTotal)
	}

	msg := regexp.MustCompile(`expected exactly (\d+) \((\d+) catalog InitScripts \+ (\d+) system`).FindStringSubmatch(src)
	if msg == nil {
		t.Fatal(`init.d count message ("expected exactly N (M catalog InitScripts + K system") not found in smoke-test.sh`)
	}
	if got := mustAtoi(t, msg[1]); got != wantTotal {
		t.Errorf("smoke-test.sh message total = %d, want %d", got, wantTotal)
	}
	if got := mustAtoi(t, msg[2]); got != wantCatalog {
		t.Errorf("smoke-test.sh message catalog count = %d, want %d (catalog Entries with InitScript)", got, wantCatalog)
	}
	if got := mustAtoi(t, msg[3]); got != wantSystem {
		t.Errorf("smoke-test.sh message system count = %d, want %d (len(systemInitScripts))", got, wantSystem)
	}
}

// vendorCompletionsUnparseable lists the completions no Dockerfile parse can
// see. kubectx/kubens are fetched inside `for tool in kubectx kubens` with
// `-o ".../_${tool}"`, so their write site is a shell variable, not a literal
// path. bwrap/curl/rg have no Dockerfile write site at all — their completions
// ride the apt packages bubblewrap, curl and ripgrep. Anything added here is a
// claim the runtime gate cannot verify per-name; keep it short.
var vendorCompletionsUnparseable = []string{"kubectx", "kubens", "bwrap", "curl", "rg"}

// TestSmokeTestVendorCompletionsFloor pins the vendor-completions `-ge N`
// literal in smoke-test.sh to the number of completions the image is expected
// to ship: every statically parseable `_<tool>` write site in the Dockerfile
// plus the declared unparseable set above.
//
// The gate stays a floor, not set-equality: both Dockerfile write paths run
// under `set -eux`, so a generator that breaks (the cf 0.0.6 regression) fails
// the build long before the smoke test. What the floor adds is a net-drop
// alarm — e.g. a base-image bump that stops shipping bwrap/curl/rg
// completions. Deriving N here means adding a completion and forgetting the
// literal fails `make go-test` instead of passing silently.
func TestSmokeTestVendorCompletionsFloor(t *testing.T) {
	src := readSmokeTest(t)

	gate := regexp.MustCompile(`(?s)_zsh_vendor_completions_check\(\).*?-ge\s+(\d+)`).FindStringSubmatch(src)
	if gate == nil {
		t.Fatal(`vendor-completions ` + "`-ge N`" + ` gate not found in smoke-test.sh`)
	}

	want := dockerfileVendorCompletionPaths(t)
	for _, name := range vendorCompletionsUnparseable {
		want[name] = struct{}{}
	}
	names := slices.Sorted(maps.Keys(want))

	if got := mustAtoi(t, gate[1]); got != len(names) {
		t.Errorf("smoke-test.sh vendor-completions `-ge` gate = %d, want %d; expected completions: %v",
			got, len(names), names)
	}
}

// dockerfileVendorCompletionPaths returns every tool name appearing in a
// literal `vendor-completions/_<tool>` path in the embedded Dockerfile: the
// final-stage precompute redirects plus the fetch-stage mv/cp/-o targets.
// Today every such path is a write site; the parse does not distinguish, so a
// future `rm`/`test -f` on one of them would inflate the count — visibly, in
// the failure message. Keeping it dumb is the point: `_${tool}` does not
// match either, which is why the loop-written pair is declared in
// vendorCompletionsUnparseable instead of parsed.
func dockerfileVendorCompletionPaths(t *testing.T) map[string]struct{} {
	t.Helper()
	out := map[string]struct{}{}
	for _, m := range regexp.MustCompile(`vendor-completions/_([a-zA-Z0-9_-]+)`).FindAllStringSubmatch(readDockerfile(t), -1) {
		out[m[1]] = struct{}{}
	}
	if len(out) == 0 {
		t.Fatal("no vendor-completions/_<tool> write sites found in the Dockerfile; the parse is broken")
	}
	return out
}

func countInitDFiles(t *testing.T) int {
	t.Helper()
	entries, err := fs.ReadDir(build.Assets, build.AssetDir+"/init.d")
	if err != nil {
		t.Fatalf("read embedded init.d: %v", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sh") {
			n++
		}
	}
	return n
}

func countCatalogInitScripts() int {
	n := 0
	for _, e := range catalog.Entries {
		if e.InitScript != "" {
			n++
		}
	}
	return n
}
