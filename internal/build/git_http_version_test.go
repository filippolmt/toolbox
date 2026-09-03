package build

import (
	"strings"
	"testing"
)

// TestGitHTTPVersionPinned holds the pin in both places the Dockerfile needs it.
//
// This git mis-reads the HTTP/2 response github returns to the POST
// /git-upload-pack that protocol v2 issues, and fails most of those requests.
// Why that is git's own fault rather than the image's or the network's, and the
// numbers behind it, live in docs/internals/image-build.md#system-git-settings.
//
// What this test is for is that the pin has two independent blast radii, so one
// pin cannot cover for the other:
//
//   - fetch-base, for the build itself. fetch-omz and fetch-brew clone from
//     github instead of curling a release artefact, several times over, so
//     without the pin the build does not complete.
//   - the final stage, for the container. Without it, clones and fetches from
//     github fail inside a toolbox shell.
//
// A needle test rather than a behavioural one on purpose: reproducing the
// failure needs a real HTTP/2 peer, neither `go test` nor smoke-test.sh makes
// network calls, and a behavioural check on a failure this intermittent would
// pass on the runs that get lucky. What makes the needle worth having is that
// the registry build cache masks the build-time half: a dropped pin surfaces
// only on a cold cache, on whichever unlucky run rebuilds fetch-base, which is
// exactly how it stayed latent before it was found.
func TestGitHTTPVersionPinned(t *testing.T) {
	const pin = "git config --system http.version HTTP/1.1"
	body := readAsset(t, "Dockerfile")

	// Bracket by stage rather than counting occurrences: two pins in the final
	// stage and none in fetch-base would satisfy any count.
	const fetchBase = "FROM debian:bookworm-slim AS fetch-base"
	start := strings.Index(body, fetchBase)
	if start < 0 {
		t.Fatalf("Dockerfile: cannot find %q — the stage was renamed, and a missing anchor must not pass this test by arithmetic", fetchBase)
	}
	stage := body[start+len(fetchBase):]
	if next := strings.Index(stage, "\nFROM "); next >= 0 {
		stage = stage[:next]
	}
	if !strings.Contains(stage, pin) {
		t.Errorf("fetch-base does not pin http.version (%q) — fetch-omz and fetch-brew clone from github, so the image does not build without it", pin)
	}

	// The final stage is the last one in the file; it is unnamed, so its own
	// header is not a usable needle.
	last := strings.LastIndex(body, "\nFROM ")
	if last < 0 {
		t.Fatal("Dockerfile: no FROM at all — cannot locate the final stage")
	}
	if last < start {
		t.Fatalf("Dockerfile: fetch-base (offset %d) is the last stage (offset %d) — the stage order was rewritten and this test no longer knows what it is reading", start, last)
	}
	if !strings.Contains(body[last:], pin) {
		t.Errorf("the final stage does not pin http.version (%q) — the container's own git needs it for every clone and fetch from github", pin)
	}
}
