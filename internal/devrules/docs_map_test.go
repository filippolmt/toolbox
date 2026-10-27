package devrules

import (
	"bufio"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// docsMapLink matches a relative Markdown link in docs/README.md: the target
// file and its optional #anchor.
var docsMapLink = regexp.MustCompile(`\]\(([^)#\s:]+\.md)(#[^)\s]*)?\)`)

// githubSlugDrop is every character GitHub strips when it derives a heading
// anchor; what survives is lower-cased and has its spaces turned into hyphens.
var githubSlugDrop = regexp.MustCompile(`[^\p{L}\p{N}_\- ]`)

func githubSlug(heading string) string {
	s := githubSlugDrop.ReplaceAllString(strings.ToLower(strings.TrimSpace(heading)), "")
	return strings.ReplaceAll(s, " ", "-")
}

// h2Headings returns the `## ` headings of a Markdown file, skipping fenced
// code blocks.
func h2Headings(t *testing.T, file string) []string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	defer func() { _ = f.Close() }()

	var (
		headings []string
		fenced   bool
	)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "## ") {
			headings = append(headings, strings.TrimPrefix(line, "## "))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return headings
}

// TestDocsMapListsEveryGuideSection holds docs/README.md to its own header:
// the map is section-level, so every `##` of a guide it links to has an entry.
// make check-links cannot see a section missing from the map — it breaks no
// link. `###` entries stay an editorial choice.
func TestDocsMapListsEveryGuideSection(t *testing.T) {
	docs := filepath.Join(repoRoot(t), "docs")
	readme, err := os.ReadFile(filepath.Join(docs, "README.md"))
	if err != nil {
		t.Fatalf("read docs/README.md: %v", err)
	}

	guides := map[string]bool{}
	anchors := map[string]bool{}
	for _, m := range docsMapLink.FindAllStringSubmatch(string(readme), -1) {
		guides[m[1]] = true
		if m[2] != "" {
			anchors[m[1]+m[2]] = true
		}
	}
	if len(guides) == 0 {
		t.Fatal("docs/README.md links no guide: the link pattern no longer matches the map")
	}

	for _, guide := range slices.Sorted(maps.Keys(guides)) {
		seen := map[string]int{}
		for _, h := range h2Headings(t, filepath.Join(docs, filepath.FromSlash(guide))) {
			// GitHub suffixes a repeated slug with -1, -2, … in document order.
			slug := githubSlug(h)
			if n := seen[slug]; n > 0 {
				seen[slug]++
				slug += "-" + strconv.Itoa(n)
			} else {
				seen[slug] = 1
			}
			if anchor := guide + "#" + slug; !anchors[anchor] {
				t.Errorf("docs/README.md has no entry for %q: add `- [%s](%s)` under the %s section", h, h, anchor, guide)
			}
		}
	}
}

func TestGithubSlug(t *testing.T) {
	for heading, want := range map[string]string{
		"DO_NOT_TRACK + claude wrapper":              "do_not_track--claude-wrapper",
		"git safe.directory (\"dubious ownership\")": "git-safedirectory-dubious-ownership",
		"Baseline: the 56 → 0 sweep":                 "baseline-the-56--0-sweep",
		"npx reports `ECOMPROMISED`":                 "npx-reports-ecompromised",
	} {
		if got := githubSlug(heading); got != want {
			t.Errorf("githubSlug(%q) = %q, want %q", heading, got, want)
		}
	}
}
