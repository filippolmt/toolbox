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
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// pathsCoverPackage reports whether any of a rule's `paths:` globs scopes it to
// pkg.
func pathsCoverPackage(globs []string, pkg string) bool {
	for _, g := range globs {
		if globCoversPackage(g, pkg) {
			return true
		}
	}
	return false
}

// ruleMentionExemptions lists `<rule file>: <path>` pairs a rule may name in its
// body without scoping itself to that path and without pointing at the rule that
// does. Empty on purpose: the routine cross-reference is settled in the prose
// instead (see ruleBlockPointsAtOwner), so an entry here is the residue — a
// package no rule owns, or one whose ownership is genuinely undecided. Add one
// only after making that call, because the default reading of a named package is
// that the naming rule governs it.
var ruleMentionExemptions = map[string]bool{}

// internalPackages returns the top-level package directory names under
// internal/. The qualifier matcher below is built from this list rather than
// from a generic "lowercase word followed by a dot" pattern, which has the same
// shape as a filename (`config.yml`), a link target (`image-build.md`), a
// hostname and a dotted config key (`worktree.seed`) — none of which name a Go
// package. Only a name that is really a directory under internal/ can.
func internalPackages(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatalf("read internal/: %v", err)
	}
	var pkgs []string
	for _, e := range entries {
		if e.IsDir() {
			pkgs = append(pkgs, e.Name())
		}
	}
	if len(pkgs) == 0 {
		t.Fatal("no packages found under internal/")
	}
	// Longest first, so `configio.RemoveFence` matches configio and not config.
	slices.SortFunc(pkgs, func(a, b string) int {
		if n := cmp.Compare(len(b), len(a)); n != 0 {
			return n
		}
		return cmp.Compare(a, b)
	})
	return pkgs
}

// qualifierPattern builds the matcher for a Go qualifier naming one of pkgs:
// `<pkg>.<member>`. The rule files spell a package this way far more often than
// as a path, so a matcher blind to it is blind to most mentions.
func qualifierPattern(pkgs []string) *regexp.Regexp {
	quoted := make([]string, len(pkgs))
	for i, p := range pkgs {
		quoted[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile(`\b(` + strings.Join(quoted, "|") + `)\.([A-Za-z_][A-Za-z0-9_]*)\b`)
}

// namesAGoMember reports whether member reads as the Go identifier half of a
// qualifier rather than as a file extension, a TLD or a config-key segment,
// which share the `<pkg>.<word>` shape. The discriminator is a capital: a Go
// member these rules cite is either exported (`Ensure`) or unexported
// camelCase (`overlayBuilder`), while `md`, `yml`, `go`, `test` and `seed` are
// flat lowercase. Deliberately conservative — a lone-lowercase-word unexported
// member goes unseen, which costs a missed mention, not a false one.
func namesAGoMember(member string) bool {
	return strings.ToLower(member) != member
}

var (
	internalPkgPattern = regexp.MustCompile(`\binternal/([a-z][a-z0-9]*)\b`)
	cmdFilePattern     = regexp.MustCompile(`\bcmd/([a-z_]+\.go)\b`)
	// siblingRulePattern matches a Markdown link to a rule file next to this
	// one. The target must carry no slash, so the `docs/internals/image-build.md`
	// guide never passes for the `image-build.md` rule that shares its basename.
	siblingRulePattern = regexp.MustCompile(`\]\(([A-Za-z0-9._-]+\.md)(?:#[^)]*)?\)`)
)

// mentionedPaths returns every repo path a rule block names, in all three
// spellings: the `internal/<pkg>` path, the `cmd/<file>.go` path, and the Go
// qualifier `<pkg>.<Member>`. A bare `cmd.<Member>` qualifier is deliberately
// not resolved — `cmd/` is one package spread over many files, and a qualifier
// names no file to scope a rule to.
func mentionedPaths(block string, qualifier *regexp.Regexp) []string {
	var mentioned []string
	for _, m := range internalPkgPattern.FindAllStringSubmatch(block, -1) {
		mentioned = append(mentioned, "internal/"+m[1])
	}
	for _, m := range cmdFilePattern.FindAllStringSubmatch(block, -1) {
		mentioned = append(mentioned, "cmd/"+m[1])
	}
	for _, m := range qualifier.FindAllStringSubmatch(block, -1) {
		if namesAGoMember(m[2]) {
			mentioned = append(mentioned, "internal/"+m[1])
		}
	}
	return mentioned
}

// ruleBlocks splits a rule body into the units a pointer is scoped to: a
// heading, a top-level list item, or a paragraph. Anything else — a wrapped
// continuation line, an indented sub-item — belongs to the block it continues.
// The block, not the whole file, is the scope on purpose: rule files cross-link
// each other constantly, so a file-wide link would excuse every mention in it,
// and a reader lands on one bullet rather than on the file.
func ruleBlocks(body string) []string {
	var (
		blocks []string
		cur    strings.Builder
	)
	flush := func() {
		if strings.TrimSpace(cur.String()) != "" {
			blocks = append(blocks, cur.String())
		}
		cur.Reset()
	}
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			flush()
			continue
		case strings.HasPrefix(line, "#"), strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			flush()
		}
		cur.WriteString(line)
		cur.WriteString("\n")
	}
	flush()
	return blocks
}

// ruleBlockPointsAtOwner reports whether block hands pkg to the rule that
// governs it: a link to a sibling rule file whose own `paths:` scopes it there.
// That is the Rule Pointer convention — the mention is a signpost, not an
// ownership claim, and it cannot be asserted in the abstract, because the link
// target has to really cover the package for the pointer to count.
func ruleBlockPointsAtOwner(block, pkg, self string, globs map[string][]string) (string, bool) {
	for _, m := range siblingRulePattern.FindAllStringSubmatch(block, -1) {
		target := m[1]
		if target == self {
			continue
		}
		if pathsCoverPackage(globs[target], pkg) {
			return target, true
		}
	}
	return "", false
}

// TestRuleMentionsAreCovered asserts that every package a rule file names in its
// body is either matched by that same file's `paths:` frontmatter — so the rule
// loads on the edits it governs — or handed, in the block that names it, to the
// rule whose frontmatter does.
//
// TestRulePathsResolve checks the opposite direction — that each glob still
// points at something on disk — and cannot catch this: config-mounts-sdd.md
// documented `configedit.ApplyChecked` as the only validated config write lane
// while listing neither internal/configui, internal/configrender nor the cmd/
// files that write through it, so editing cmd/config.go loaded a different rule
// (via cmd/**) and never that one. Every glob it did list resolved fine.
func TestRuleMentionsAreCovered(t *testing.T) {
	root := repoRoot(t)
	qualifier := qualifierPattern(internalPackages(t, root))

	rules := ruleFiles(t)
	globs := make(map[string][]string, len(rules))
	for _, rule := range rules {
		globs[filepath.Base(rule)] = frontmatterPaths(t, rule)
	}

	for _, rule := range rules {
		name := filepath.Base(rule)
		reported := map[string]bool{}

		for _, block := range ruleBlocks(ruleBody(t, rule)) {
			seen := map[string]bool{}
			for _, pkg := range mentionedPaths(block, qualifier) {
				if seen[pkg] || reported[pkg] || ruleMentionExemptions[name+": "+pkg] {
					continue
				}
				seen[pkg] = true

				if pathsCoverPackage(globs[name], pkg) {
					continue
				}
				if _, ok := ruleBlockPointsAtOwner(block, pkg, name, globs); ok {
					continue
				}
				reported[pkg] = true
				t.Errorf("%s: names %q but no paths: entry scopes it there and the block "+
					"that names it links no rule that does — add a glob if this rule governs "+
					"those edits, link the rule that does if it is a pointer, or exempt it in "+
					"ruleMentionExemptions",
					name, pkg)
			}
		}
	}
}

// TestAMentionIsAQualifierNotItsLookalikes pins the classifier
// TestRuleMentionsAreCovered rests on, so it cannot go green by seeing nothing.
// Its predecessor read only the `internal/<pkg>` path spelling while the rule
// files overwhelmingly write the Go qualifier, and a mention it cannot see is
// neither enforced nor exempted — it passes by being invisible. The other half
// is what it must keep refusing: a filename, a link target, a hostname and a
// dotted config key all have the shape of a qualifier and name no package.
func TestAMentionIsAQualifierNotItsLookalikes(t *testing.T) {
	qualifier := qualifierPattern([]string{
		"sessionplan", "localimage", "configio", "worktree", "config", "build",
	})

	cases := []struct {
		name  string
		block string
		want  []string
	}{
		{"exported qualifier", "`localimage.Ensure` builds the overlay", []string{"internal/localimage"}},
		{"unexported camelCase qualifier", "applied by `sessionplan.composeEnv`", []string{"internal/sessionplan"}},
		{"the longest package wins", "`configio.RemoveFence`", []string{"internal/configio"}},
		{"path spelling still read", "the primitives in internal/config", []string{"internal/config"}},
		{"cmd file spelling still read", "`cmd/worktree.go` resolves the agent", []string{"cmd/worktree.go"}},
		{"a link to a sibling rule", "governed by [image-build.md](image-build.md)", nil},
		{"a filename", "`~/.config/glab-cli/config.yml` is one host mount", nil},
		{"a dotted config key", "plus `worktree.seed` config extras", nil},
		{"a bare cmd qualifier names no file", "`cmd.startSession` resolves it", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mentionedPaths(c.block, qualifier)
			if !slices.Equal(got, c.want) {
				t.Errorf("mentionedPaths(%q) = %v, want %v", c.block, got, c.want)
			}
		})
	}
}

// TestARulePointerNamesARuleThatCoversThePackage pins the other half of the
// settlement: a pointer is a link whose target really governs the package, in
// the block that names it. Anything looser stops being a check — every rule
// file links its neighbours, so a pointer taken on trust, or read file-wide,
// would excuse every mention in the tree.
func TestARulePointerNamesARuleThatCoversThePackage(t *testing.T) {
	globs := map[string][]string{
		"mounts.md":            {"internal/mountplan/**"},
		"container-runtime.md": {"internal/container/**"},
	}

	cases := []struct {
		name string
		body string
		want bool
	}{
		{"the rule that covers it", "- `mountplan.Merge`, governed by [mounts.md](mounts.md)\n", true},
		{"a rule that does not", "- `mountplan.Merge`, see [container-runtime.md](container-runtime.md)\n", false},
		{"the same-basename guide under docs/", "- `mountplan.Merge`, see [mounts](../../docs/mounts.md)\n", false},
		{"itself", "- `mountplan.Merge`, see [self.md](self.md)\n", false},
		{"a pointer on the previous bullet", "- governed by [mounts.md](mounts.md)\n- `mountplan.Merge` here\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got bool
			for _, block := range ruleBlocks(c.body) {
				if !strings.Contains(block, "`mountplan.Merge`") {
					continue
				}
				_, got = ruleBlockPointsAtOwner(block, "internal/mountplan", "self.md", globs)
			}
			if got != c.want {
				t.Errorf("pointer accepted = %v, want %v, for %q", got, c.want, c.body)
			}
		})
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
