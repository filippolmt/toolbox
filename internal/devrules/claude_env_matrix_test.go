package devrules

import (
	"bufio"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// envVarName matches a backticked env var at the head of a matrix bullet, with
// or without its value: `DISABLE_UPDATES=1`, `BASH_MAX_TIMEOUT_MS`.
var envVarName = regexp.MustCompile("`([A-Z][A-Z0-9_]+)(?:=[^`]*)?`")

// claudeEnvKnobs returns the variables set by the ENV instruction that follows
// the "Claude Code env knobs" comment block in the Dockerfile.
func claudeEnvKnobs(t *testing.T) []string {
	t.Helper()
	const anchor = "# Claude Code env knobs"
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "build", "assets", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	body := string(b)
	start := strings.Index(body, anchor)
	if start < 0 {
		t.Fatalf("Dockerfile: cannot find %q — the block was renamed, and a missing anchor must not pass this test empty", anchor)
	}
	env := strings.Index(body[start:], "\nENV ")
	if env < 0 {
		t.Fatalf("Dockerfile: no ENV after %q", anchor)
	}

	var names []string
	sc := bufio.NewScanner(strings.NewReader(body[start+env+len("\nENV "):]))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		name, _, _ := strings.Cut(line, "=")
		names = append(names, name)
		if !strings.HasSuffix(line, `\`) {
			break
		}
	}
	return names
}

// privacyMatrixVars returns the variables named at the head of each bullet of
// the matrix in docs/internals/privacy.md — the part before " — ", so a knob a
// bullet merely mentions in its explanation does not count as listed.
func privacyMatrixVars(t *testing.T) []string {
	t.Helper()
	const heading = "## Claude Code env-var matrix"
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "internals", "privacy.md"))
	if err != nil {
		t.Fatalf("read privacy.md: %v", err)
	}
	_, section, ok := strings.Cut(string(b), heading)
	if !ok {
		t.Fatalf("privacy.md: cannot find %q", heading)
	}
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}

	var names []string
	for line := range strings.SplitSeq(section, "\n") {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		head, _, _ := strings.Cut(line, " — ")
		for _, m := range envVarName.FindAllStringSubmatch(head, -1) {
			names = append(names, m[1])
		}
	}
	return names
}

// TestClaudeEnvKnobsMatchThePrivacyMatrix holds the two copies of the Claude
// Code env-var list to each other: the ENV block the image ships, and the
// matrix in docs/internals/privacy.md that explains each knob. A knob added in
// one place only is how the matrix drifted before.
func TestClaudeEnvKnobsMatchThePrivacyMatrix(t *testing.T) {
	set := func(names []string) map[string]bool {
		m := map[string]bool{}
		for _, n := range names {
			m[n] = true
		}
		return m
	}
	knobs, matrix := set(claudeEnvKnobs(t)), set(privacyMatrixVars(t))
	if len(knobs) == 0 || len(matrix) == 0 {
		t.Fatalf("parsed %d knobs and %d matrix entries — a parser no longer matches its file", len(knobs), len(matrix))
	}
	for _, n := range slices.Sorted(maps.Keys(knobs)) {
		if !matrix[n] {
			t.Errorf("%s is set in the Dockerfile's Claude Code env knobs but missing from docs/internals/privacy.md#claude-code-env-var-matrix: add a bullet saying what it does and why", n)
		}
	}
	for _, n := range slices.Sorted(maps.Keys(matrix)) {
		if !knobs[n] {
			t.Errorf("%s is listed in docs/internals/privacy.md#claude-code-env-var-matrix but the Dockerfile's Claude Code env knobs no longer set it: drop the bullet or restore the knob", n)
		}
	}
}
