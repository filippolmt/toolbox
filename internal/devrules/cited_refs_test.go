package devrules

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// citedMakeTarget matches a Make target cited as inline code: `make go-check`.
	citedMakeTarget = regexp.MustCompile("`make ([A-Za-z0-9_-]+)")
	// citedScript matches a repo-relative path under .github/scripts.
	citedScript = regexp.MustCompile(`\.github/scripts/[A-Za-z0-9_.-]+[A-Za-z0-9_-]`)
	// makeTargetRule matches a rule header, never a `name :=` assignment.
	makeTargetRule = regexp.MustCompile(`(?m)^([A-Za-z0-9_-]+):([^=]|$)`)
)

// TestCitedMakeTargetsAndScriptsExist holds prose to the Makefile and to
// .github/scripts: a renamed target or script leaves no broken Markdown link,
// so make check-links passes over a `make old-name` in the pre-push table. ADRs
// are exempt — they record the names as of their decision.
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
		t.Fatal("Makefile: rule pattern found no go-check target; the pattern no longer matches")
	}

	var cited int
	check := func(rel string, body string, makeTargets bool) {
		if makeTargets {
			for _, m := range citedMakeTarget.FindAllStringSubmatch(body, -1) {
				cited++
				if !targets[m[1]] {
					t.Errorf("%s cites `make %s`, which the Makefile does not define", rel, m[1])
				}
			}
		}
		for _, p := range citedScript.FindAllString(body, -1) {
			cited++
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				t.Errorf("%s cites %s, which does not exist", rel, p)
			}
		}
	}

	check("Makefile", string(makefile), false)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist":
				return filepath.SkipDir
			}
			if rel == "docs/adr" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".md") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		check(rel, string(body), true)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if cited == 0 {
		t.Fatal("no Markdown file cites a make target or script: the citation patterns no longer match")
	}
}
