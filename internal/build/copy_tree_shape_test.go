package build

import (
	"regexp"
	"strings"
	"testing"
)

// TestFinalStageBuildStageCOPYsCopyTrees pins the shape that keeps a
// `COPY --link --from=<stage>` layer reproducible: it copies the stage's whole
// `/out/` tree to `/`, never a single file to a path.
//
// A --link layer is built independently of the filesystem beneath it, so it has
// to materialise every directory on the destination path itself. Copying one
// file to /usr/local/bin makes BuildKit synthesise usr/, usr/local/ and
// usr/local/bin/ inside the layer, and those carry the build's wall clock no
// matter how thoroughly the source file's own mtime was frozen. The layer digest
// then changes on every rebuild of the stage, with no version bump behind it —
// measured on rtk, whose amd64 binary comes from a checksummed tarball and is
// byte-identical across builds while its layer was not. Copying the tree instead
// carries the directories from /out, which `freeze-mtimes` (or the stage's own
// `touch -d @1`) has already stamped.
//
// Context COPYs (no --from) are deliberately out of scope: some of those layers
// move too, they are all under the gate's size filter, and the cause there is not
// the same one this test describes. → ADR 0002, CONTEXT.md "Fetch Nondeterminism"
func TestFinalStageBuildStageCOPYsCopyTrees(t *testing.T) {
	body := readAsset(t, "Dockerfile")

	from := strings.LastIndex(body, "\nFROM node:")
	if from < 0 {
		t.Fatal("Dockerfile: cannot locate the final `FROM node:` stage")
	}

	// `--from=` marks a copy out of another build stage. The alignment padding
	// between the flag and the source is cosmetic and varies in the file.
	stageCopyRE := regexp.MustCompile(`^COPY .*--from=`)
	wantRE := regexp.MustCompile(`^COPY --link --from=[A-Za-z0-9_.-]+\s+/out/ /$`)

	var seen int
	for _, line := range strings.Split(body[from+1:], "\n") {
		if !stageCopyRE.MatchString(line) {
			continue
		}
		seen++
		if !wantRE.MatchString(line) {
			t.Errorf("%q does not copy a tree — a single-file COPY --link synthesises its destination directories with the build's wall clock, so the layer digest moves on every rebuild; write into /out/<final path> in the stage and copy `/out/ /`", line)
		}
	}

	// Anti-vacuity only, in the repo's usual shape: a floor tied to today's stage
	// count would fail the day a tool is legitimately removed.
	if seen == 0 {
		t.Error("found no build-stage COPY in the final stage — the parse is broken, not the Dockerfile")
	}
}
