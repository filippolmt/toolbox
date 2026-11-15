package devrules

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// toolSkipWindow is how far below an exec.LookPath call a t.Skip still counts
// as skipping on that lookup.
const toolSkipWindow = 6

// skipsWithoutCIGuard returns the 1-based lines of every exec.LookPath call in
// a Go test source that a t.Skip follows within toolSkipWindow lines with no
// os.Getenv("CI") check between them. Such a test retires itself silently on a
// runner missing the tool; under $CI it must fail instead.
func skipsWithoutCIGuard(src string) []int {
	lines := strings.Split(src, "\n")
	var bad []int
	for i, line := range lines {
		if !strings.Contains(line, "exec.LookPath(") {
			continue
		}
		for _, next := range lines[i+1 : min(len(lines), i+1+toolSkipWindow)] {
			if strings.Contains(next, `os.Getenv("CI")`) {
				break
			}
			if strings.Contains(next, "t.Skip") {
				bad = append(bad, i+1)
				break
			}
		}
	}
	return bad
}

func TestSkipsWithoutCIGuard(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []int
	}{
		{"bare skip", "if _, err := exec.LookPath(\"jq\"); err != nil {\n\tt.Skip(\"no jq\")\n}", []int{1}},
		{"skipf in a loop", "for _, tool := range tools {\n\tif _, err := exec.LookPath(tool); err != nil {\n\t\tt.Skipf(\"%s\", tool)\n\t}\n}", []int{2}},
		{"guarded", "if _, err := exec.LookPath(\"zsh\"); err != nil {\n\tif os.Getenv(\"CI\") != \"\" {\n\t\tt.Fatal(\"x\")\n\t}\n\tt.Skip(\"no zsh\")\n}", nil},
		{"fatal only", "p, err := exec.LookPath(\"git\")\nif err != nil {\n\tt.Fatalf(\"%v\", err)\n}", nil},
		{"lookup passed as a value", "if _, _, err := pick(exec.LookPath); err == nil {\n\tt.Skip(\"player installed\")\n}", nil},
	} {
		if got := skipsWithoutCIGuard(tc.src); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestAMissingToolNeverSkipsUnderCI: a test that executes a tool and skips
// when PATH lacks it must fail under $CI instead, or a runner that lost the
// tool turns the test off with nothing red. In internal/build the guard is
// requireTools; elsewhere an inline os.Getenv("CI") check.
func TestAMissingToolNeverSkipsUnderCI(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// dist/ is gitignored build and scratch output: make go-dockergate
		// keeps a whole Go module cache under dist/.scratch.
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules" || path == filepath.Join(root, "dist")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") || path == filepath.Join(root, "internal/devrules/tool_skip_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range skipsWithoutCIGuard(string(b)) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s:%d skips on a missing tool without failing under $CI — use requireTools or an os.Getenv(\"CI\") check", rel, line)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
