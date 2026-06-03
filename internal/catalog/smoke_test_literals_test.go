// Drift guards for the hand-maintained integer literals in
// assets/smoke-test.sh. TestCatalogInitDBijection proves catalog↔disk SET
// equality; the smoke test additionally hardcodes *counts* that no Go test
// touched until now — CLAUDE.md flags both as "drifts silently — count by
// hand". These tests pin each literal to its derivable source of truth so a
// forgotten bump fails `make go-test` instead of slipping into the image.
//
// smoke-test.sh is read from disk (it is deliberately NOT embedded — embed.go
// ships only the Docker build context; the smoke test runs from a checkout in
// CI). Go runs tests with the package directory as CWD, so the relative path
// is stable.

package catalog_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// TestSmokeTestVendorCompletionsFloor keeps the vendor-completions floor in
// smoke-test.sh internally consistent: the `-ge N` runtime gate, the
// "expect >= N files" comment, and the inventory of tool names the comment
// lists must all agree. The runtime check validates the real file count in
// CI; this guards the literal against drift from its own documented inventory
// (e.g. adding a tool to the list but forgetting to bump the floor).
func TestSmokeTestVendorCompletionsFloor(t *testing.T) {
	src := readSmokeTest(t)

	gate := regexp.MustCompile(`(?s)_zsh_vendor_completions_check\(\).*?-ge\s+(\d+)`).FindStringSubmatch(src)
	if gate == nil {
		t.Fatal(`vendor-completions ` + "`-ge N`" + ` gate not found in smoke-test.sh`)
	}
	gateN := mustAtoi(t, gate[1])

	comment := regexp.MustCompile(`>=\s*(\d+)\s+files`).FindStringSubmatch(src)
	if comment == nil {
		t.Fatal(`vendor-completions "expect >= N files" comment not found in smoke-test.sh`)
	}
	if commentN := mustAtoi(t, comment[1]); commentN != gateN {
		t.Errorf("vendor-completions comment floor = %d but `-ge` gate = %d; keep them in sync", commentN, gateN)
	}

	inv := vendorCompletionInventory(t, src)
	if len(inv) != gateN {
		t.Errorf("vendor-completions inventory lists %d tools %v but floor is %d; bump the `-ge`/comment literals or fix the list", len(inv), inv, gateN)
	}
}

// vendorCompletionInventory extracts the comma-separated tool names the
// "default build:" comment enumerates, up to the closing paren. Comment `#`
// markers and surrounding whitespace are stripped; empty fragments (e.g. a
// trailing comma) are dropped.
func vendorCompletionInventory(t *testing.T, src string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)default build:(.*?)\)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal(`vendor-completions inventory ("default build: …)") not found in smoke-test.sh`)
	}
	var names []string
	for raw := range strings.SplitSeq(m[1], ",") {
		name := strings.TrimSpace(strings.ReplaceAll(raw, "#", ""))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
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
