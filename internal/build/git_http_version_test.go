package build

import (
	"strings"
	"testing"
)

// TestGitHTTPVersionPinned holds the pin in both places the Dockerfile needs it.
//
// The git this base image ships mis-reads github.com's HTTP/2 response to
// protocol v2's POST /git-upload-pack: the ref listing comes back truncated or
// as a 401, so the command dies with "expected flush after ref listing" or a
// bogus "could not read Username". It fails per request rather than always (15
// of 20 ls-remote runs over h2, 0 of 20 with the pin), so a clone issuing
// several requests is nearly certain to die while any single retry can pass by
// luck. Measured to be git's own bug rather than this image's or the network's — bare debian:bookworm-slim with none of our
// config fails identically, a much newer git succeeds over the same Docker
// network, and curl reproduces nothing (the same POST over h2 returns 200).
//
// Two independent pins, because the failure has two independent blast radii:
//
//   - fetch-base, for the build itself. fetch-omz and fetch-brew clone from
//     github instead of curling a release artefact, six fetches between them,
//     so without the pin the build effectively cannot complete.
//   - the final stage, for the container. Without it HTTPS clones and fetches
//     from github fail most of the time inside a toolbox shell.
//
// A needle test rather than a behavioural one on purpose: reproducing the
// failure needs a real HTTP/2 peer, and neither `go test` nor smoke-test.sh
// makes network calls — and a behavioural check on a per-request failure would
// be flaky in the one direction that matters, passing on the runs that get
// lucky. What makes the needle worth having is that the registry build cache
// masks the build-time half: a dropped pin surfaces only on a cold cache, on
// whichever unlucky run rebuilds fetch-base, which is exactly how it stayed
// latent before it was found.
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
