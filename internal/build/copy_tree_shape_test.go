package build

import (
	"regexp"
	"strings"
	"testing"
)

// TestBuildStageCOPYsCopyWholeTree pins the shape that keeps a
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
// the same one this test describes. → ADR 0002 follow-up 3
func TestBuildStageCOPYsCopyWholeTree(t *testing.T) {
	// `--from=` marks a copy out of another build stage. The alignment padding
	// between the flag and the source is cosmetic and varies in the file.
	stageCopyRE := regexp.MustCompile(`^COPY .*--from=`)
	wantRE := regexp.MustCompile(`^COPY --link --from=[A-Za-z0-9_.-]+\s+/out/ /$`)

	var seen int
	for _, line := range strings.Split(finalStage(t), "\n") {
		if !stageCopyRE.MatchString(line) {
			continue
		}
		seen++
		if !wantRE.MatchString(line) {
			// Deliberately exact, flags included: `--chmod`/`--chown` on such a
			// COPY would mean the mode is being decided here rather than in the
			// stage, which is the shape that made the single-file COPY look
			// harmless. Set the mode in the stage and copy the tree.
			t.Errorf("%q is not `COPY --link --from=<stage> /out/ /` — a single-file COPY --link synthesises its destination directories with the build's wall clock, so the layer digest moves on every rebuild; write into /out/<final path> in the stage and copy the tree", line)
		}
	}

	// Anti-vacuity only, in the repo's usual shape: a floor tied to today's stage
	// count would fail the day a tool is legitimately removed.
	if seen == 0 {
		t.Error("found no build-stage COPY in the final stage — the parse is broken, not the Dockerfile")
	}
}
