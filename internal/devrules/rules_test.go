// Package devrules holds repo-hygiene tests for the path-scoped Claude Code
// rule files under .claude/rules/. They are not product code; the package
// exists only so `go test ./...` (and thus ci.yml) validates the rules on the
// changes that can silently break them — directory renames under internal/,
// cmd/, etc. lychee (docs.yml) checks the rules' Markdown links, but never the
// glob patterns in their `paths:` frontmatter, so a renamed package leaves an
// orphaned glob that nothing else catches.
package devrules

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from the test's working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}

// frontmatterPaths returns the `paths:` glob entries from a rule file's YAML
// frontmatter (the block between the first two `---` lines). It hand-parses
// rather than pulling in a YAML dependency: the frontmatter is a flat list
// under a single `paths:` key.
func frontmatterPaths(t *testing.T, file string) []string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	defer func() { _ = f.Close() }()

	var (
		paths   []string
		fences  int
		inPaths bool
	)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			fences++
			if fences == 2 {
				break // end of frontmatter
			}
			continue
		}
		if fences != 1 {
			continue // outside frontmatter
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // comment
		}
		if trimmed == "paths:" {
			inPaths = true
			continue
		}
		// A new top-level key (no leading space, ends with ':') closes the list.
		if inPaths && !strings.HasPrefix(line, " ") && strings.HasSuffix(trimmed, ":") {
			inPaths = false
		}
		if inPaths && strings.HasPrefix(trimmed, "- ") {
			entry := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			entry = strings.Trim(entry, `"'`)
			if entry != "" {
				paths = append(paths, entry)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", file, err)
	}
	return paths
}

// TestRulePathsResolve asserts every `paths:` glob in .claude/rules/*.md still
// points at something that exists in the repo. A glob like "internal/foo/**"
// is checked by its literal prefix (the dir before the first wildcard); an
// exact path like "go.mod" is checked as-is. Catches rule files left orphaned
// by a package rename or deletion.
func TestRulePathsResolve(t *testing.T) {
	root := repoRoot(t)
	rulesDir := filepath.Join(root, ".claude", "rules")
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		t.Fatalf("read %s: %v", rulesDir, err)
	}

	var rules []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			rules = append(rules, filepath.Join(rulesDir, e.Name()))
		}
	}
	if len(rules) == 0 {
		t.Fatalf("no rule files found in %s", rulesDir)
	}

	for _, rule := range rules {
		globs := frontmatterPaths(t, rule)
		if len(globs) == 0 {
			t.Errorf("%s: no paths: entries parsed from frontmatter", filepath.Base(rule))
			continue
		}
		for _, g := range globs {
			// Reduce the glob to its literal prefix: the path up to the first
			// path segment that contains a wildcard.
			var lit []string
			for seg := range strings.SplitSeq(g, "/") {
				if strings.ContainsAny(seg, "*?[") {
					break
				}
				lit = append(lit, seg)
			}
			if len(lit) == 0 {
				continue // glob with a wildcard in its first segment — skip
			}
			target := filepath.Join(root, filepath.Join(lit...))
			if _, err := os.Stat(target); err != nil {
				t.Errorf("%s: paths: entry %q resolves to missing path %q",
					filepath.Base(rule), g, filepath.Join(lit...))
			}
		}
	}
}
