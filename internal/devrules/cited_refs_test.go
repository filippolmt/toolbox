package devrules

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	// citedMakeTarget matches a Make target cited as inline code: `make go-check`.
	// The trailing class rejects a variable (`make V=x`) or a glob (`make go-*`).
	citedMakeTarget = regexp.MustCompile("`make ([A-Za-z][A-Za-z0-9_-]*)(?:[^A-Za-z0-9_*=-]|$)")
	// fencedMakeTarget matches a command line inside a fenced block: make go-check.
	fencedMakeTarget = regexp.MustCompile(`^\s*(?:\$ )?make ([A-Za-z][A-Za-z0-9_-]*)(?:[^A-Za-z0-9_*=-]|$)`)
	// citedScript matches a repo-relative path under .github/scripts: the last
	// character drops a sentence's closing period, the trailing class a glob.
	citedScript = regexp.MustCompile(`(\.github/scripts/[A-Za-z0-9_.-]*[A-Za-z0-9_-])(?:[^A-Za-z0-9_*-]|$)`)
	// makeTargetRule matches a rule header, never a `name :=` assignment.
	makeTargetRule = regexp.MustCompile(`(?m)^([A-Za-z0-9_-]+):([^=]|$)`)
)

// citedDocRoots mirrors the roots of the Makefile's check-links target, so both
// guardrails cover the same prose; docs/superpowers mirrors its --exclude-path.
// docs/adr is skipped as well: an ADR records the names as of its decision.
var citedDocRoots = []string{
	"README.md", "CLAUDE.md", "CONTRIBUTING.md", "CONTEXT.md", "CODING_STANDARDS.md",
	"docs", ".claude/rules", ".claude/skills",
}

var citedDocSkips = map[string]bool{"docs/adr": true, "docs/superpowers": true}

// citedMakeTargets returns every Make target a Markdown body cites, as inline
// code or as a command line inside a fenced block.
func citedMakeTargets(body string) []string {
	var (
		targets []string
		fenced  bool
	)
	for line := range strings.Lines(body) {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		pattern := citedMakeTarget
		if fenced {
			pattern = fencedMakeTarget
		}
		for _, m := range pattern.FindAllStringSubmatch(line, -1) {
			targets = append(targets, m[1])
		}
	}
	return targets
}

// markdownDocs returns the repo-relative path and body of every Markdown file
// under citedDocRoots.
func markdownDocs(t *testing.T, root string) map[string]string {
	t.Helper()
	docs := map[string]string{}
	for _, start := range citedDocRoots {
		err := filepath.WalkDir(filepath.Join(root, start), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() && citedDocSkips[rel] {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(rel, ".md") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			docs[rel] = string(body)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", start, err)
		}
	}
	return docs
}

// TestCitedMakeTargetsAndScriptsExist holds prose to the Makefile and to
// .github/scripts: a renamed target or script breaks no Markdown link, so make
// check-links passes over a stale `make old-name` in CLAUDE.md.
func TestCitedMakeTargetsAndScriptsExist(t *testing.T) {
	root := repoRoot(t)
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	targets := map[string]bool{}
	for _, m := range makeTargetRule.FindAllStringSubmatch(string(makefile), -1) {
		targets[m[1]] = true
	}
	if !targets["go-check"] {
		t.Fatal("Makefile: the rule pattern found no go-check target; it no longer matches a rule header")
	}

	var makeCited, scriptCited int
	for rel, body := range markdownDocs(t, root) {
		for _, target := range citedMakeTargets(body) {
			makeCited++
			if !targets[target] {
				t.Errorf("%s cites `make %s`, which the Makefile does not define: cite the current target name", rel, target)
			}
		}
		for _, m := range citedScript.FindAllStringSubmatch(body, -1) {
			p := m[1]
			scriptCited++
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				t.Errorf("%s cites %s, which does not exist: cite the current script path", rel, p)
			}
		}
	}
	if makeCited == 0 {
		t.Error("no Markdown file cites a make target: the make patterns no longer match")
	}
	if scriptCited == 0 {
		t.Error("no Markdown file cites a .github/scripts path: the script pattern no longer matches")
	}
}

func TestCitedMakeTargets(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"inline", "Run `make go-check` first.", []string{"go-check"}},
		{"inline with arguments", "`make go-test-one PKG=./x`", []string{"go-test-one"}},
		{"backtick fence", "```sh\nmake go-dockergate PKG=./x\n$ make scratch\n```", []string{"go-dockergate", "scratch"}},
		{"tilde fence", "~~~\nmake build\n~~~", []string{"build"}},
		{"prose outside a fence", "make sure the gate\nruns", nil},
		{"variable, flag and glob", "`make V=x t` `make -C d t` `make go-*`", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := citedMakeTargets(tc.body); !slices.Equal(got, tc.want) {
				t.Errorf("citedMakeTargets(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

var (
	// citedTest matches a Go test named as inline code: `TestFoo`, a subtest
	// `TestFoo/case`, or a family `TestFoo*` (every test starting with TestFoo).
	citedTest = regexp.MustCompile("`(Test[A-Z][A-Za-z0-9_]*)(\\*)?(?:/[^`]*)?`")
	// definedTest matches a top-level test function declaration.
	definedTest = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
)

// citedTestRetired names the tests prose cites on purpose although no code
// defines them any more, each with the reason; anything else cited must exist.
var citedTestRetired = map[string]string{
	"TestShellInspectNilContainerJSONBase": "CONTEXT.md's Run Plan entry names it as the tripwire the term replaced",
}

// TestCitedTestNamesExist holds prose to the tests it names: a rule pins its
// guardrail by test name, and a renamed or deleted test leaves that pin
// pointing at nothing with every other check green.
func TestCitedTestNamesExist(t *testing.T) {
	root := repoRoot(t)
	var defined []string
	walkTestFiles(t, root, func(_, body string) {
		for _, m := range definedTest.FindAllStringSubmatch(body, -1) {
			defined = append(defined, m[1])
		}
	})
	if !slices.Contains(defined, "TestCitedTestNamesExist") {
		t.Fatal("the declaration pattern did not find this test: it no longer matches a test function")
	}

	cited := 0
	for rel, body := range markdownDocs(t, root) {
		for _, m := range citedTest.FindAllStringSubmatch(body, -1) {
			name := m[1]
			cited++
			if _, ok := citedTestRetired[name]; ok {
				continue
			}
			if m[2] == "*" {
				if !slices.ContainsFunc(defined, func(d string) bool { return strings.HasPrefix(d, name) }) {
					t.Errorf("%s cites the test family `%s*`, but no test starts with %s: cite the current names", rel, name, name)
				}
				continue
			}
			if !slices.Contains(defined, name) {
				t.Errorf("%s cites `%s`, which no test defines: cite the current test name", rel, name)
			}
		}
	}
	if cited == 0 {
		t.Error("no Markdown file cites a test: the citation pattern no longer matches")
	}
}

func TestCitedTestPattern(t *testing.T) {
	cases := []struct {
		body, name string
		family     bool
	}{
		{"pinned by `TestFoo`.", "TestFoo", false},
		{"`TestFoo/sub case`", "TestFoo", false},
		{"the `TestBridgeContract*` tests", "TestBridgeContract", true},
		{"`TestBridgeContract_ShimMatchesGo`", "TestBridgeContract_ShimMatchesGo", false},
	}
	for _, tc := range cases {
		m := citedTest.FindStringSubmatch(tc.body)
		if m == nil || m[1] != tc.name || (m[2] == "*") != tc.family {
			t.Errorf("citedTest on %q = %q, want name %q family %v", tc.body, m, tc.name, tc.family)
		}
	}
	for _, body := range []string{"`Tests`", "TestFoo without backticks", "`TestFoo bar`"} {
		if m := citedTest.FindStringSubmatch(body); m != nil {
			t.Errorf("citedTest on %q = %q, want no match", body, m)
		}
	}
}

var (
	// citedInitScript matches an Init Sequence member: init.d/30-graphify.sh.
	citedInitScript = regexp.MustCompile(`init\.d/([0-9]{2}-[A-Za-z0-9_.-]*\.sh)`)
	// citedAssetBin matches an image asset under bin/, written as inline code
	// (`bin/toolbox-refresh`) or as its repo path. A bare /usr/local/bin/x is
	// the installed path, and bin/x/y a directory, so both are left alone.
	citedAssetBin = regexp.MustCompile("(?:`|internal/build/assets/)bin/([A-Za-z0-9_.-]*[A-Za-z0-9_-])(?:[^/A-Za-z0-9_.-]|$)")
)

// citedBinNotAnAsset names the bin/ files prose cites that are not image
// assets, each with the reason; anything else cited must exist.
var citedBinNotAnAsset = map[string]string{
	"brew":                 "Homebrew's own bin/brew, in its prefix",
	"toolbox-update-check": "the retired helper CONTEXT.md names in a term's history",
}

// TestCitedImageAssetsExist holds prose to the image assets it names: a removed
// or renamed init.d member or bin/ asset breaks no Markdown link, so the docs
// describing it go stale with every check green.
func TestCitedImageAssetsExist(t *testing.T) {
	root := repoRoot(t)
	assets := filepath.Join(root, "internal", "build", "assets")
	var initCited, binCited int
	for rel, body := range markdownDocs(t, root) {
		for _, m := range citedInitScript.FindAllStringSubmatch(body, -1) {
			initCited++
			if _, err := os.Stat(filepath.Join(assets, "init.d", m[1])); err != nil {
				t.Errorf("%s cites init.d/%s, which does not exist: describe what replaced it", rel, m[1])
			}
		}
		for _, m := range citedAssetBin.FindAllStringSubmatch(body, -1) {
			if _, ok := citedBinNotAnAsset[m[1]]; ok {
				continue
			}
			binCited++
			if _, err := os.Stat(filepath.Join(assets, "bin", m[1])); err != nil {
				t.Errorf("%s cites bin/%s, which does not exist: describe what replaced it", rel, m[1])
			}
		}
	}
	if initCited == 0 || binCited == 0 {
		t.Errorf("init.d citations %d, bin/ citations %d: a pattern no longer matches the docs", initCited, binCited)
	}
}
