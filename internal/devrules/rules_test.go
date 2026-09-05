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
	"regexp"
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

// ruleFiles returns the path of every rule file under .claude/rules/. Three
// tests walk the same directory; the shared listing keeps them agreeing on
// what counts as a rule file.
func ruleFiles(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), ".claude", "rules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	if len(files) == 0 {
		t.Fatalf("no rule files found in %s", dir)
	}
	return files
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

	for _, rule := range ruleFiles(t) {
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

// ruleBody returns everything after a rule file's YAML frontmatter (the block
// between the first two `---` lines), i.e. the prose the rule actually states.
func ruleBody(t *testing.T, file string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	parts := strings.SplitN(string(raw), "---\n", 3)
	if len(parts) < 3 {
		t.Fatalf("%s: no frontmatter fence found", filepath.Base(file))
	}
	return parts[2]
}

// globCoversPackage reports whether a `paths:` glob scopes a rule to pkg. Only
// the two shapes the rule files use are understood: a `<dir>/**` subtree and an
// exact path.
func globCoversPackage(glob, pkg string) bool {
	if dir := strings.TrimSuffix(glob, "/**"); dir != glob {
		return pkg == dir || strings.HasPrefix(pkg, dir+"/")
	}
	return pkg == glob
}

// ruleMentionExemptions lists `<rule file>: <path>` pairs a rule may name in its
// body without scoping itself to that path — a passing cross-reference to a
// neighbouring package rather than material the rule governs. Empty on purpose:
// add an entry only after deciding the rule does not own edits in that package,
// because the default reading of a named package is that it does.
var ruleMentionExemptions = map[string]bool{}

var (
	internalPkgPattern = regexp.MustCompile(`\binternal/([a-z][a-z0-9]*)\b`)
	cmdFilePattern     = regexp.MustCompile(`\bcmd/([a-z_]+\.go)\b`)
)

// TestRuleMentionsAreCovered asserts that every package a rule file names in its
// body is matched by that same file's `paths:` frontmatter, so the rule loads on
// the edits it governs.
//
// TestRulePathsResolve checks the opposite direction — that each glob still
// points at something on disk — and cannot catch this: config-mounts-sdd.md
// documented `configedit.ApplyChecked` as the only validated config write lane
// while listing neither internal/configui, internal/configrender nor the cmd/
// files that write through it, so editing cmd/config.go loaded a different rule
// (via cmd/**) and never that one. Every glob it did list resolved fine.
func TestRuleMentionsAreCovered(t *testing.T) {
	for _, rule := range ruleFiles(t) {
		name := filepath.Base(rule)
		globs := frontmatterPaths(t, rule)
		body := ruleBody(t, rule)

		var mentioned []string
		for _, m := range internalPkgPattern.FindAllStringSubmatch(body, -1) {
			mentioned = append(mentioned, "internal/"+m[1])
		}
		for _, m := range cmdFilePattern.FindAllStringSubmatch(body, -1) {
			mentioned = append(mentioned, "cmd/"+m[1])
		}

		seen := make(map[string]bool, len(mentioned))
		for _, pkg := range mentioned {
			if seen[pkg] || ruleMentionExemptions[name+": "+pkg] {
				continue
			}
			seen[pkg] = true

			covered := false
			for _, g := range globs {
				if globCoversPackage(g, pkg) {
					covered = true
					break
				}
			}
			if !covered {
				t.Errorf("%s: names %q in its body but no paths: entry scopes it there — "+
					"add a glob, or exempt it in ruleMentionExemptions",
					name, pkg)
			}
		}
	}
}

// rulePathExemptions lists `internal/<pkg>` directories deliberately left
// unclaimed by every rule's `paths:` frontmatter. Empty on purpose: a package
// with no guardrail worth loading is a claim about the package, so make it
// here explicitly rather than by silently omitting a glob.
var rulePathExemptions = map[string]bool{}

// TestEveryPackageIsClaimedByARule asserts every internal/ package is scoped by
// at least one rule file, so editing it loads a guardrail.
//
// The third direction, and the one neither sibling covers.
// TestRulePathsResolve walks rule → disk (does this glob still point at
// something?) and TestRuleMentionsAreCovered walks body → frontmatter (is the
// package this rule talks about in its own paths?). Both are satisfied by a
// package no rule has ever heard of: internal/fsx owned the filesystem
// primitives CLAUDE.md tells every package to call, and an edit there loaded
// nothing at all.
func TestEveryPackageIsClaimedByARule(t *testing.T) {
	root := repoRoot(t)

	var globs []string
	for _, rule := range ruleFiles(t) {
		globs = append(globs, frontmatterPaths(t, rule)...)
	}

	pkgs, err := os.ReadDir(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatalf("read internal/: %v", err)
	}
	for _, p := range pkgs {
		if !p.IsDir() {
			continue
		}
		pkg := "internal/" + p.Name()
		if rulePathExemptions[pkg] {
			continue
		}
		claimed := false
		for _, g := range globs {
			if globCoversPackage(g, pkg) {
				claimed = true
				break
			}
		}
		if !claimed {
			t.Errorf("%s is claimed by no rule's paths: — editing it loads no guardrail; "+
				"add a glob to the rule that owns it, or exempt it in rulePathExemptions", pkg)
		}
	}
}

// TestRuleFilesAreListedInCLAUDEMd asserts CLAUDE.md's index of the rule files
// names every file that exists. That list is how a reader (and Codex, which
// loads no rule automatically) learns a rule is there at all, so a rule missing
// from it is a rule nobody opens — and adding the sixth rule file while leaving
// the list at five is exactly the drift these tests exist to catch.
func TestRuleFilesAreListedInCLAUDEMd(t *testing.T) {
	root := repoRoot(t)
	claudeMd, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	index := string(claudeMd)

	for _, rule := range ruleFiles(t) {
		link := ".claude/rules/" + filepath.Base(rule)
		if !strings.Contains(index, link) {
			t.Errorf("CLAUDE.md does not link %s — a rule absent from that index is one "+
				"no reader knows to open; add a line describing what it governs", link)
		}
	}
}
