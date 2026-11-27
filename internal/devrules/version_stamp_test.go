package devrules

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// versionVar matches a string var in internal/version's var block.
var versionVar = regexp.MustCompile(`(?m)^\t(\w+)\s+=\s+"`)

// goreleaserStamp matches one `-X <module>/internal/version.<Var>=` ldflag.
var goreleaserStamp = regexp.MustCompile(`-X github\.com/filippolmt/toolbox/internal/version\.(\w+)=`)

// makeStamp matches one `-X $(VERSION_PKG).<Var>=` ldflag in the Makefile.
var makeStamp = regexp.MustCompile(`-X \$\(VERSION_PKG\)\.(\w+)=`)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func submatches(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// TestBuildsStampTheBuildIdentity pins the ldflags the bridge refresh depends
// on: the daemon and the CLI compare version.Build(), so a build that stops
// stamping Commit and Date makes every rebuilt checkout look alike and the
// session-start refresh silently stops firing.
func TestBuildsStampTheBuildIdentity(t *testing.T) {
	vars := submatches(versionVar, readRepoFile(t, "internal/version/version.go"))

	for _, tc := range []struct {
		file string
		re   *regexp.Regexp
		want []string
	}{
		{".goreleaser.yaml", goreleaserStamp, []string{"Commit", "Date", "Version"}},
		// A local build keeps Version `dev` on purpose (imageprefetch abstains on it).
		{"Makefile", makeStamp, []string{"Commit", "Date"}},
	} {
		got := submatches(tc.re, readRepoFile(t, tc.file))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s stamps internal/version %v, want %v", tc.file, got, tc.want)
		}
		for _, v := range got {
			if !slices.Contains(vars, v) {
				t.Errorf("%s stamps version.%s, which internal/version/version.go does not declare (has %v)", tc.file, v, vars)
			}
		}
	}
}
