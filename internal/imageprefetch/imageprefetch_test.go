package imageprefetch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/fsx"
)

const (
	testRef     = "ghcr.io/filippolmt/toolbox:latest"
	digestOld   = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	digestNew   = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	digestNewer = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

// pollStub is what one poll's daemon answers with. It implements no Docker
// method of its own: docker() wires the shared fake to this data, stubbing the
// three endpoints a poll may reach and no others, so a probe or pull that
// should never have happened panics on the method it named instead of
// returning a silent zero value.
type pollStub struct {
	// inspects are returned in order, one per ImageInspect call, so a test can
	// distinguish the digest before a pull from the digest after it.
	inspects   []client.ImageInspectResult
	distErr    error
	distDigest string
	pullErr    error
	// pullHang makes ImagePull block until its context is cancelled, then
	// signals on the channel. It is how a registry that accepts the
	// connection and then stops talking is spelled in a test.
	pullHang chan struct{}

	fake *dockertest.Fake
}

// docker returns the daemon a poll sees, built once and reused so both the
// inspect queue and the call counters survive across the poll and the
// assertions that read them. Called from the test goroutine only, before the
// pollers it hands the fake to exist.
func (m *pollStub) docker() *dockertest.Fake {
	if m.fake != nil {
		return m.fake
	}
	m.fake = &dockertest.Fake{
		ImageInspectFn: dockertest.InspectSeq(m.inspects...),
		DistributionInspectFn: func(context.Context, string) (client.DistributionInspectResult, error) {
			if m.distErr != nil {
				return client.DistributionInspectResult{}, m.distErr
			}
			return dockertest.DistributionResult(m.distDigest), nil
		},
		ImagePullFn: func(ctx context.Context, _ string) (client.ImagePullResponse, error) {
			if m.pullHang != nil {
				<-ctx.Done()
				m.pullHang <- struct{}{}
				return nil, ctx.Err()
			}
			if m.pullErr != nil {
				return nil, m.pullErr
			}
			return dockertest.PullResponse{ReadCloser: io.NopCloser(bytes.NewReader(nil))}, nil
		},
	}
	return m.fake
}

// inspectWith builds a local-store inspect for the test ref.
func inspectWith(repoDigest string) client.ImageInspectResult {
	return dockertest.ImageInspectResult("ghcr.io/filippolmt/toolbox", repoDigest)
}

// stateDir returns a throwaway state mount for one test.
func stateDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// cacheBody renders the expected cache in the writer's own field order, so a
// test states the four values it cares about instead of a format literal that
// every added field would have to be pasted into again.
func cacheBody(imageUpdate, imageLatest, imageState, cliUpdate, cliLatest string) string {
	return "image_update=" + imageUpdate +
		"\nimage_latest=" + imageLatest +
		"\nimage_state=" + imageState +
		"\ncli_update=" + cliUpdate +
		"\ncli_latest=" + cliLatest + "\n"
}

// readCache returns the update-check body, or "" when the poll wrote none.
func readCache(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, cacheFile))
	if err != nil {
		return ""
	}
	return string(raw)
}

// A stamp younger than the TTL is the whole cadence: the ticker is only an
// alarm, so a tick that fires inside the window must not reach the registry.
// The fake stubs the three endpoints a poll may reach and panics on the
// method a fourth would name, and a probe that did happen would show up as a
// DistributionInspect count — which is what makes the unasked question fail
// loudly.
func TestPollAsksTheRegistryNothingWhileStampIsFresh(t *testing.T) {
	dir := stateDir(t)
	if err := os.WriteFile(filepath.Join(dir, stampFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestOld)}}
	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.docker().DistributionInspectCalls() != 0 {
		t.Errorf("DistributionInspect called %d times, want 0 inside the TTL", cli.docker().DistributionInspectCalls())
	}
	if cli.docker().ImagePullCalls() != 0 {
		t.Errorf("pulled %d times inside the TTL, want 0", cli.docker().ImagePullCalls())
	}
}

// The gate stops the registry, not the banner. #864: the published result is
// whichever session wrote last, and image_update is computed against *that*
// session's container — so a sibling already on the new image publishes a 0
// that is true only for it, while keeping the very stamp that holds this gate
// shut. Every gated pass restates the session axis from the local store, a
// comparison the registry has no part in, so the sibling never owns this
// session's banner. Repeated because a session outlives many ticks: fixing
// only the first pass would hand the sibling every one after it.
func TestPollRestatesTheSessionAxisOnEveryGatedPass(t *testing.T) {
	dir := stateDir(t)
	sibling := func() {
		t.Helper()
		// A workspace whose container already runs digestNew: true for it.
		writeResult(dir, result{imageLatest: digestNew, imageState: stateNone})
	}
	sibling()
	if !stamp(dir) {
		t.Fatal("seed the attempt stamp")
	}
	cli := &pollStub{inspects: []client.ImageInspectResult{
		inspectWith(digestNew), inspectWith(digestNew),
	}}
	in := Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir}

	want := cacheBody("1", digestNew, stateReady, "0", "")
	for pass := 1; pass <= 2; pass++ {
		Poll(t.Context(), cli.docker(), in)
		if got := readCache(t, dir); got != want {
			t.Fatalf("pass %d: cache = %q, want %q", pass, got, want)
		}
		sibling() // the sibling polls too, and publishes its own truth again
	}
	if cli.docker().DistributionInspectCalls() != 0 {
		t.Errorf("DistributionInspect called %d times, want 0 — the gate still holds", cli.docker().DistributionInspectCalls())
	}
}

// A stamp older than the TTL opens the window again.
func TestPollRunsOnceTheStampIsStale(t *testing.T) {
	dir := stateDir(t)
	path := filepath.Join(dir, stampFile)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * TTL)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestOld)}, distDigest: digestOld}
	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.docker().DistributionInspectCalls() != 1 {
		t.Errorf("DistributionInspect called %d times, want 1 past the TTL", cli.docker().DistributionInspectCalls())
	}
}

// The stamp records the attempt, not the success: an offline machine must be
// capped at one failed probe per TTL, not one per tick. A failed probe also
// leaves a previously valid result alone rather than blanking the banner.
func TestPollStampsAndPreservesCacheWhenTheProbeFails(t *testing.T) {
	dir := stateDir(t)
	seeded := cacheBody("1", digestNew, stateReady, "0", "")
	if err := os.WriteFile(filepath.Join(dir, cacheFile), []byte(seeded), 0o644); err != nil {
		t.Fatal(err)
	}

	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestOld)}, distErr: errors.New("dial tcp: no route to host")}
	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if _, err := os.Stat(filepath.Join(dir, stampFile)); err != nil {
		t.Errorf("stamp not written on a failed probe: %v", err)
	}
	if got := readCache(t, dir); got != seeded {
		t.Errorf("cache rewritten on a failed probe:\ngot  %q\nwant %q", got, seeded)
	}
	if cli.docker().ImagePullCalls() != 0 {
		t.Errorf("pulled %d times after a failed probe, want 0", cli.docker().ImagePullCalls())
	}
}

// An image with no repo digest was produced by a local `toolbox build`. The
// prefetch abstains entirely: an explicit act by the developer is never
// undone by an automatic one.
func TestPollAbstainsOnLocallyBuiltImage(t *testing.T) {
	dir := stateDir(t)
	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith("")}}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.docker().DistributionInspectCalls() != 0 {
		t.Errorf("probed %d times for a locally built image, want 0", cli.docker().DistributionInspectCalls())
	}
	if cli.docker().ImagePullCalls() != 0 {
		t.Errorf("pulled %d times for a locally built image, want 0", cli.docker().ImagePullCalls())
	}
}

// The happy path: remote is ahead of the local store, so the bytes are
// fetched and the banner states that this session is behind what is now on
// disk — the local-store-versus-container comparison, not the remote one.
func TestPollPullsAndReportsTheSessionBehind(t *testing.T) {
	dir := stateDir(t)
	cli := &pollStub{
		inspects:   []client.ImageInspectResult{inspectWith(digestOld), inspectWith(digestNew)},
		distDigest: digestNew,
	}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.docker().ImagePullCalls() != 1 {
		t.Errorf("pulled %d times, want 1", cli.docker().ImagePullCalls())
	}
	want := cacheBody("1", digestNew, stateReady, "0", "")
	if got := readCache(t, dir); got != want {
		t.Errorf("cache:\ngot  %q\nwant %q", got, want)
	}
}

// Bytes already in the store: no pull, but the session created before them is
// still behind, which is exactly the state a reload exists for.
func TestPollSkipsThePullWhenTheStoreIsAlreadyCurrent(t *testing.T) {
	dir := stateDir(t)
	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestNew)}, distDigest: digestNew}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.docker().ImagePullCalls() != 0 {
		t.Errorf("pulled %d times with the store already current, want 0", cli.docker().ImagePullCalls())
	}
	if got := readCache(t, dir); got != cacheBody("1", digestNew, stateReady, "0", "") {
		t.Errorf("cache: %q", got)
	}
}

// Nothing moved: the container runs what the store holds and what the
// registry serves. The banner must stay silent.
func TestPollStaysSilentWhenTheSessionIsCurrent(t *testing.T) {
	dir := stateDir(t)
	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestNew)}, distDigest: digestNew}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestNew, StateDir: dir})

	if got := readCache(t, dir); got != cacheBody("0", digestNew, stateNone, "0", "") {
		t.Errorf("cache: %q", got)
	}
}

// A probe that succeeds while the pull fails — expired registry credentials,
// the case the pull half warns about — must not announce bytes that never
// landed.
// (The dedicated "unavailable" wording is the banner ticket's, not this one's.)
func TestPollStaysSilentWhenThePullFails(t *testing.T) {
	dir := stateDir(t)
	cli := &pollStub{
		inspects:   []client.ImageInspectResult{inspectWith(digestOld)},
		distDigest: digestNew,
		pullErr:    errors.New("unauthorized"),
	}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	want := cacheBody("0", digestNew, stateNone, "0", "")
	if got := readCache(t, dir); got != want {
		t.Errorf("cache:\ngot  %q\nwant %q", got, want)
	}
}

// A container created from an image with no repo digest has no baseline, so
// no claim is made about it either way.
func TestPollMakesNoClaimWithoutAContainerDigest(t *testing.T) {
	dir := stateDir(t)
	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestNew)}, distDigest: digestNew}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, StateDir: dir})

	if got := readCache(t, dir); got != cacheBody("0", digestNew, stateNone, "0", "") {
		t.Errorf("cache: %q", got)
	}
}

// Start refuses the two inputs that would give the poller nowhere to write or
// nothing to watch, and must do so without touching the client — which is nil
// here, so any call is a panic.
func TestStartRefusesAnIncompleteInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Input
	}{
		{"no state dir", Input{Ref: testRef}},
		{"no image ref", Input{StateDir: t.TempDir()}},
		{"neither", Input{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			Start(t.Context(), nil, tc.in)
		})
	}
}

// The ticker is an alarm, but the goroutine it drives must still stop with
// the session: a leaked poller would outlive the shell it belongs to.
func TestStartStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	before := goroutineCount()
	Start(ctx, (&pollStub{}).docker(), Input{Ref: testRef, StateDir: t.TempDir()})
	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if goroutineCount() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("poller goroutine still running after cancel (%d, was %d)", goroutineCount(), before)
}

// goroutineCount is the leak probe for the poller goroutine.
func goroutineCount() int { return runtime.NumGoroutine() }

// newerVersion is the whole CLI axis: a wrong answer either nags about a
// release that does not exist or hides one that does.
func TestNewerVersion(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.3.0", true},
		{"v1.2.3", "v2.0.0", true},
		{"1.2.3", "v1.2.3", false},
		{"v1.2.3", "v1.2.2", false},
		{"v1.2.3", "v1.2", false},
		{"v1.2", "v1.2.0", false},
		{"v1.2", "v1.2.1", true},
		// Double-digit fields must compare numerically, not lexically.
		{"v1.9.0", "v1.10.0", true},
		{"v1.10.0", "v1.9.0", false},
		// A pre-release suffix is dropped, so it never reads as an upgrade.
		{"v1.2.3", "v1.2.3-rc1", false},
		// A tag we cannot parse degrades to "not newer" rather than nagging.
		{"v1.2.3", "nightly", false},
		{"v1.2.3", "", false},
	} {
		if got := newerVersion(tc.current, tc.latest); got != tc.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

// releasesServer stands in for the GitHub releases endpoint and points the
// package at it for one test.
func releasesServer(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want the GitHub media type", got)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	orig := releasesURL
	releasesURL = srv.URL
	t.Cleanup(func() { releasesURL = orig })
}

// The CLI axis end to end: a published tag ahead of this build reaches the
// cache with its version, which is what the banner prints.
func TestPollReportsANewerCLI(t *testing.T) {
	dir := stateDir(t)
	releasesServer(t, 200, `{"tag_name":"v1.2.3"}`)

	// A locally built image abstains on the image axis, isolating the CLI one.
	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith("")}}
	Poll(t.Context(), cli.docker(), Input{Ref: testRef, StateDir: dir, CLIVersion: "v1.0.0"})

	want := cacheBody("0", "", stateNone, "1", "v1.2.3")
	if got := readCache(t, dir); got != want {
		t.Errorf("cache:\ngot  %q\nwant %q", got, want)
	}
}

// An un-stamped local build has no release to compare against, so the axis is
// skipped outright — the endpoint must not even be called. With the image
// axis abstaining too, a poll that asked nothing publishes nothing: a `dev`
// CLI attached to a locally built image must not blank the result a
// release-built sibling session wrote minutes earlier.
func TestPollSkipsTheCLIAxisForADevBuild(t *testing.T) {
	dir := stateDir(t)
	releasesServer(t, 500, "boom")

	seeded := cacheBody("1", digestNew, stateReady, "0", "")
	if err := os.WriteFile(filepath.Join(dir, cacheFile), []byte(seeded), 0o644); err != nil {
		t.Fatal(err)
	}

	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith("")}}
	Poll(t.Context(), cli.docker(), Input{Ref: testRef, StateDir: dir, CLIVersion: "dev"})

	if got := readCache(t, dir); got != seeded {
		t.Errorf("an abstaining poll rewrote the cache:\ngot  %q\nwant %q", got, seeded)
	}
}

// One axis failing must not retract the other axis's still-true answer. The
// renderer keys its de-nag on the whole file, so a blanked field both hides a
// real banner and re-fires the surviving one.
func TestPollKeepsTheAxisThatDidNotReach(t *testing.T) {
	dir := stateDir(t)
	releasesServer(t, 403, `{"message":"rate limited"}`)

	seeded := cacheBody("0", digestOld, stateNone, "1", "v1.2.3")
	if err := os.WriteFile(filepath.Join(dir, cacheFile), []byte(seeded), 0o644); err != nil {
		t.Fatal(err)
	}

	// The image axis reaches and moves; the CLI axis is rate-limited.
	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestNew)}, distDigest: digestNew}
	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir, CLIVersion: "v1.0.0"})

	want := cacheBody("1", digestNew, stateReady, "1", "v1.2.3")
	if got := readCache(t, dir); got != want {
		t.Errorf("cache:\ngot  %q\nwant %q", got, want)
	}
}

// A pull that lands but whose follow-up inspect fails leaves the pre-pull
// digest standing. Reading the failure as an empty digest would make the
// comparison against the container's digest true for the wrong reason, and
// the banner would announce an update nothing can substantiate.
func TestPollStaysSilentWhenTheStoreCannotBeReRead(t *testing.T) {
	dir := stateDir(t)
	// Only one inspect is stubbed; the post-pull one falls through to an error.
	cli := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestOld)}, distDigest: digestNew}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	want := cacheBody("0", digestNew, stateNone, "0", "")
	if got := readCache(t, dir); got != want {
		t.Errorf("cache:\ngot  %q\nwant %q", got, want)
	}
}

// latestRelease answers only on a usable response: a non-200 (the anonymous
// rate limit included) and a body with no tag are both errors, so the caller
// counts the axis as unreached and preserves the previous result.
func TestLatestRelease(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		want    string
		wantErr bool
	}{
		{"tag", 200, `{"tag_name":"v9.9.9"}`, "v9.9.9", false},
		{"rate limited", 429, `{"message":"rate limited"}`, "", true},
		{"no tag", 200, `{}`, "", true},
		{"not json", 200, `<html>`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			releasesServer(t, tc.status, tc.body)
			got, err := latestRelease(t.Context())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("tag = %q, want %q", got, tc.want)
			}
			if err != nil && err.Error() == "" {
				t.Error("error carries no message")
			}
		})
	}
}

// unavailableAge back-dates the first-failure marker so a test can stand at
// either side of the grace window without sleeping.
func unavailableAge(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	path := filepath.Join(dir, unavailableFile)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// `unavailable` must not fire on the first failed download: a dropped Wi-Fi
// connection would otherwise produce a banner accusing the registry.
func TestPollWithholdsUnavailableOnTheFirstFailure(t *testing.T) {
	dir := stateDir(t)
	cli := &pollStub{
		inspects:   []client.ImageInspectResult{inspectWith(digestNew)},
		distDigest: digestNewer,
		pullErr:    errors.New("unauthorized"),
	}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestNew, StateDir: dir})

	if got := readCache(t, dir); got != cacheBody("0", digestNewer, stateNone, "0", "") {
		t.Errorf("cache: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, unavailableFile)); err != nil {
		t.Errorf("first failure not recorded: %v", err)
	}
}

// Once the failure has persisted a full cadence — at least two consecutive
// attempts — the state is earned. This is the case the host cannot report any
// other way: its stdout is the attached tty.
func TestPollReportsUnavailableOnceTheFailurePersists(t *testing.T) {
	dir := stateDir(t)
	unavailableAge(t, dir, 2*TTL)
	cli := &pollStub{
		inspects:   []client.ImageInspectResult{inspectWith(digestNew)},
		distDigest: digestNewer,
		pullErr:    errors.New("unauthorized"),
	}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestNew, StateDir: dir})

	if got := readCache(t, dir); got != cacheBody("0", digestNewer, stateUnavailable, "0", "") {
		t.Errorf("cache: %q", got)
	}
}

// A first-failure marker must record the *first* failure, not the latest, or
// the grace window never elapses and `unavailable` never fires.
func TestPollDoesNotResetTheFirstFailureClock(t *testing.T) {
	dir := stateDir(t)
	unavailableAge(t, dir, 2*TTL)
	before := mtime(t, filepath.Join(dir, unavailableFile))

	cli := &pollStub{
		inspects:   []client.ImageInspectResult{inspectWith(digestNew)},
		distDigest: digestNewer,
		pullErr:    errors.New("unauthorized"),
	}
	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestNew, StateDir: dir})

	if after := mtime(t, filepath.Join(dir, unavailableFile)); !after.Equal(before) {
		t.Errorf("first-failure marker rewritten: %v -> %v", before, after)
	}
}

// A successful download clears the history: `unavailable` must not outlive
// the condition it describes.
func TestPollClearsUnavailableOnceTheBytesLand(t *testing.T) {
	dir := stateDir(t)
	unavailableAge(t, dir, 2*TTL)
	cli := &pollStub{
		inspects:   []client.ImageInspectResult{inspectWith(digestOld), inspectWith(digestNew)},
		distDigest: digestNew,
	}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if got := readCache(t, dir); got != cacheBody("1", digestNew, stateReady, "0", "") {
		t.Errorf("cache: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, unavailableFile)); !os.IsNotExist(err) {
		t.Errorf("first-failure marker survived a successful pull: %v", err)
	}
}

// An adoptable image outranks a failed download: `ready` is something the
// developer can act on now, and one line is all the renderer prints.
func TestPollPrefersReadyOverUnavailable(t *testing.T) {
	dir := stateDir(t)
	unavailableAge(t, dir, 2*TTL)
	// The store holds digestNew, the session is on digestOld, the registry has
	// moved on to digestNewer and the pull for it fails.
	cli := &pollStub{
		inspects:   []client.ImageInspectResult{inspectWith(digestNew)},
		distDigest: digestNewer,
		pullErr:    errors.New("unauthorized"),
	}

	Poll(t.Context(), cli.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if got := readCache(t, dir); got != cacheBody("1", digestNewer, stateReady, "0", "") {
		t.Errorf("cache: %q", got)
	}
}

// mtime reads a marker's timestamp.
func mtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

// TestClearResult pins exactly which files a session reload drops, because
// #834 settled it as a named set rather than a sweep. The result and the
// legacy shown-signature — written by no renderer this image ships, still read
// by an older one — describe the container the reload just retired; the
// attempt stamp gates the next probe behind up to a full cadence, and the
// reload has just invalidated the answer that cadence was throttling —
// deleting it is what makes the documented "costs one extra probe" true.
//
// The unavailable-since marker stays: it records whether the registry can be
// reached, which a reload does not change, and resetting it would restart a
// clock that has to elapse before the word "unavailable" is earned.
func TestClearResult(t *testing.T) {
	dir := t.TempDir()
	seed := func(name string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		return p
	}
	gone := []string{seed(cacheFile), seed(cacheFile + ".shown"), seed(stampFile)}
	kept := seed(unavailableFile)

	ClearResult(dir)

	for _, p := range gone {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the reload (stat = %v)", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("%s was cleared, restarting a clock the reload did not change: %v", filepath.Base(kept), err)
	}
}

// An empty state dir is the session that mounts none: there is nothing to
// clear, and joining onto "" would reach for the filesystem root.
func TestClearResultWithoutAStateDir(t *testing.T) {
	ClearResult("") // must not panic, must not touch anything
}

// TestStartPublishesFromTheStoreAfterAShellStartSync is #725's cold start.
// The synchronous refresh at shell start is a probe, so it takes this TTL's
// turn at the registry and the poller must not re-ask minutes later. What it
// must still do is publish: on a connect the container can be behind a store
// a sibling session just advanced, and that banner is the whole point.
func TestStartPublishesFromTheStoreAfterAShellStartSync(t *testing.T) {
	dir := stateDir(t)
	// Two inspects: the sync publishes, and the first poll — gated by the
	// stamp the sync just left — restates the same session axis from the same
	// store. Idempotent, and the second one is what proves it.
	m := &pollStub{inspects: []client.ImageInspectResult{
		inspectWith(digestNew), inspectWith(digestNew),
	}}

	startPoller(t, m.docker(), Input{
		Ref:             testRef,
		ContainerDigest: digestOld,
		StateDir:        dir,
		StartSynced:     true,
	})

	waitFor(t, func() bool { return readCache(t, dir) != "" })

	if got, want := readCache(t, dir), cacheBody("1", digestNew, stateReady, "0", ""); got != want {
		t.Errorf("cache = %q, want %q", got, want)
	}
	if m.docker().DistributionInspectCalls() != 0 {
		t.Errorf("DistributionInspect calls = %d, want 0 — the shell start already probed", m.docker().DistributionInspectCalls())
	}
	if !fsx.MarkerFresh(filepath.Join(dir, stampFile), TTL) {
		t.Error("the shell-start sync left no attempt stamp, so the next tick will re-probe")
	}
}

// TestStartProbesWhenTheShellStartDidNot is the other half: a cache hit or a
// failed pull at shell start syncs nothing, so the poller owes the registry
// its own probe rather than trusting a round trip that did not happen.
func TestStartProbesWhenTheShellStartDidNot(t *testing.T) {
	dir := stateDir(t)
	m := &pollStub{
		inspects:   []client.ImageInspectResult{inspectWith(digestNew)},
		distDigest: digestNew,
	}

	startPoller(t, m.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	waitFor(t, func() bool { return readCache(t, dir) != "" })
	if m.docker().DistributionInspectCalls() == 0 {
		t.Error("DistributionInspect calls = 0, want the poller's own probe")
	}
}

// TestStartPublishesTheSessionAxisWhileThePollIsGated is #864's never-case.
// One state mount serves every workspace, so the published result is whatever
// session wrote last — and image_update is computed against *that* session's
// container. A sibling already running the new image publishes image_update=0
// and keeps the shared attempt stamp warm; this session's container is older,
// its first poll returns at the gate, and it would render the sibling's answer
// for as long as the sibling keeps probing. On the connect branch the banner
// is the only channel there is, so that silence has nothing to break it.
//
// The comparison this session owes itself — the local store against the digest
// its own container was created from — costs no registry round trip, so the
// gate is no reason to skip it. Two things must hold: the session axis is
// restated, and image_latest is left exactly as the last real probe published
// it, because knownRemote reads that field back as the *registry's* digest and
// AheadOfStore decides the start-up prompt on it.
func TestStartPublishesTheSessionAxisWhileThePollIsGated(t *testing.T) {
	dir := stateDir(t)
	// A sibling on the new image published a moment ago, and claimed the turn.
	writeResult(dir, result{imageLatest: digestNew, imageState: stateNone})
	if !stamp(dir) {
		t.Fatal("seed the attempt stamp")
	}
	// distDigest is left unset on purpose: a probe would answer the empty
	// digest rather than panic, so what pins the unasked question is the
	// DistributionInspect count asserted below.
	m := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestNew)}}

	startPoller(t, m.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	want := cacheBody("1", digestNew, stateReady, "0", "")
	waitFor(t, func() bool { return readCache(t, dir) == want })
	if m.docker().DistributionInspectCalls() != 0 {
		t.Errorf("DistributionInspect calls = %d, want 0 — the gate still holds, only the local comparison was redone", m.docker().DistributionInspectCalls())
	}
}

// TestStartLeavesTheRegistryDigestAloneWhileGated guards the other half of the
// same publish: an ungated re-statement that overwrote image_latest with the
// *store's* digest would make knownRemote answer "the registry serves what we
// already have", and AheadOfStore would stop offering the start-up refresh for
// the rest of the TTL.
func TestStartLeavesTheRegistryDigestAloneWhileGated(t *testing.T) {
	dir := stateDir(t)
	// The registry is ahead of the store, and the last probe said so.
	writeResult(dir, result{imageLatest: digestNewer, imageState: stateNone})
	if !stamp(dir) {
		t.Fatal("seed the attempt stamp")
	}
	m := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestNew)}}

	startPoller(t, m.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	want := cacheBody("1", digestNewer, stateReady, "0", "")
	waitFor(t, func() bool { return readCache(t, dir) == want })

	store := AheadOfStore(t.Context(), (&pollStub{
		inspects: []client.ImageInspectResult{inspectWith(digestNew)},
	}).docker(), testRef, dir)
	if !store.Ahead {
		t.Error("AheadOfStore stopped seeing the registry ahead — image_latest was overwritten with the store digest")
	}
}

// TestStartKeepsTheFailureClockWhenNoProbeAnswered is the third branch of the
// gated publish, and the one with a clock behind it. A warm stamp with no
// published digest is a probe that *failed*: nothing is established about the
// registry, so the store-only publish may state the session axis and must
// state nothing else. Routing it through imageState would take the not-behind
// branch, clear the first-failure marker, and restart the window that has to
// elapse before a persistently failing download is ever called unavailable —
// on every shell start, which means never.
func TestStartKeepsTheFailureClockWhenNoProbeAnswered(t *testing.T) {
	dir := stateDir(t)
	writeResult(dir, result{imageState: stateNone}) // a probe that answered nothing
	if !stamp(dir) {
		t.Fatal("seed the attempt stamp")
	}
	marker := filepath.Join(dir, unavailableFile)
	if err := fsx.TouchMarker(marker); err != nil {
		t.Fatalf("seed the failure clock: %v", err)
	}
	started := mtime(t, marker)

	m := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestNew)}}
	startPoller(t, m.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	// The bytes in the store are adoptable whatever the registry is doing, and
	// the renderer never prints "ready" beside "unavailable".
	want := cacheBody("1", "", stateReady, "0", "")
	waitFor(t, func() bool { return readCache(t, dir) == want })

	if got, err := os.Stat(marker); err != nil {
		t.Fatalf("the failure clock was cleared on an answer nobody gave: %v", err)
	} else if !got.ModTime().Equal(started) {
		t.Error("the failure clock was restarted, so `unavailable` would never be earned")
	}
}

// TestStartPublishesNothingForALocallyBuiltImage holds the abstention on the
// new path too. A store with no repo digest for the ref is the fingerprint of
// a `toolbox build`, and the prefetch says nothing about one anywhere — a
// store-only publish is still a publish, and inventing an image_update for an
// image the developer built by hand would advise a reload onto their own work.
func TestStartPublishesNothingForALocallyBuiltImage(t *testing.T) {
	dir := stateDir(t)
	seeded := cacheBody("0", digestNew, stateNone, "0", "")
	if err := os.WriteFile(filepath.Join(dir, cacheFile), []byte(seeded), 0o644); err != nil {
		t.Fatal(err)
	}
	if !stamp(dir) {
		t.Fatal("seed the attempt stamp")
	}
	m := &pollStub{inspects: []client.ImageInspectResult{inspectWith("")}}

	startPoller(t, m.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	// The poll is gated and the publish abstains, so nothing may move. Give
	// the goroutine a tick's worth of chances to prove otherwise.
	for range 20 {
		if got := readCache(t, dir); got != seeded {
			t.Fatalf("cache = %q, want it untouched %q", got, seeded)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if m.docker().DistributionInspectCalls() != 0 {
		t.Errorf("the registry was probed %d times for a locally built image", m.docker().DistributionInspectCalls())
	}
}

// TestStartCancelsAHungPollAtTheNextTick is #726's cancel-and-reissue, which
// #840 leant on when it refused every form of backoff: a registry that
// accepts the connection and then stops talking must cost one tick, not the
// session. Without it the poller's single goroutine sits inside ImagePull for
// as long as the shell lives and no later tick ever runs.
func TestStartCancelsAHungPollAtTheNextTick(t *testing.T) {
	shortenTick(t, 20*time.Millisecond)

	dir := stateDir(t)
	cancelled := make(chan struct{}, 1)
	m := &pollStub{
		inspects:   []client.ImageInspectResult{inspectWith(digestOld)},
		distDigest: digestNew,
		pullHang:   cancelled,
	}

	startPoller(t, m.docker(), Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the hung pull was never cancelled: a stalled registry blocks the poller for the whole session")
	}

	// The cancelled poll finishes on its own — a cancelled pull is an error
	// like any other, so it still publishes. Waiting for that last write is
	// what keeps the goroutine from racing the temp dir's removal.
	waitFor(t, func() bool { return readCache(t, dir) != "" })
}

// startPoller runs the poller under a context this test owns and does not let
// the test finish until its goroutines are gone. Everything a poll reads from
// package scope — tickInterval, releasesURL, version.Version — is written by
// some other test's setup, so a poller outliving its own test races the next
// one rather than merely wasting cycles.
func startPoller(t *testing.T, cli registryStore, in Input) {
	t.Helper()
	before := goroutineCount()
	ctx, cancel := context.WithCancel(t.Context())
	Start(ctx, cli, in)
	t.Cleanup(func() {
		cancel()
		deadline := time.Now().Add(5 * time.Second)
		for goroutineCount() > before && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	})
}

// waitFor polls cond until it holds, failing the test on timeout. The poller
// is a goroutine, so every assertion about what it wrote is eventual.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never held within the deadline")
}

// shortenTick rewinds the poller's alarm so a tick-driven assertion runs in
// milliseconds. The interval is a var for exactly this reason — the same seam
// the package uses for releasesURL.
func shortenTick(t *testing.T, d time.Duration) {
	t.Helper()
	orig := tickInterval
	tickInterval = d
	t.Cleanup(func() { tickInterval = orig })
}

// AheadOfStore is the question the start-up refresh prompt has to answer
// before it can ask anything, and the reason it lives here is that both ways
// of answering it — the shared probe cache and the probe itself — already do.
// A warm stamp means a sibling session established the fact a moment ago:
// re-establishing it would reintroduce, one step higher, the latency the
// prompt exists to remove, and the DistributionInspect count staying at zero
// is what says the registry was never reached.
func TestAheadOfStoreAnswersFromTheWarmStamp(t *testing.T) {
	dir := stateDir(t)
	if err := os.WriteFile(filepath.Join(dir, cacheFile),
		[]byte(cacheBody("0", digestNew, stateNone, "0", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsx.TouchMarker(filepath.Join(dir, stampFile)); err != nil {
		t.Fatal(err)
	}
	mock := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestOld)}}

	got := AheadOfStore(context.Background(), mock.docker(), testRef, dir)
	if want := (StoreState{Ahead: true, Known: true}); got != want {
		t.Errorf("AheadOfStore() = %+v, want %+v", got, want)
	}
	if mock.docker().DistributionInspectCalls() != 0 {
		t.Errorf("the registry was probed %d times despite a warm stamp", mock.docker().DistributionInspectCalls())
	}
}

// A cold stamp is precisely the case where the question is most likely worth
// asking — no session has been open recently, so the store is probably behind
// — so it probes, and the probe is metadata rather than a download.
func TestAheadOfStoreProbesOnAColdStamp(t *testing.T) {
	for _, tc := range []struct {
		name    string
		local   string
		remote  string
		distErr error
		want    StoreState
	}{
		{name: "registry ahead", local: digestOld, remote: digestNew, want: StoreState{Ahead: true, Known: true, Probed: true}},
		{name: "store current", local: digestNew, remote: digestNew, want: StoreState{Known: true, Probed: true}},
		{name: "probe failed", local: digestOld, remote: digestNew, distErr: errors.New("offline"), want: StoreState{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &pollStub{
				inspects:   []client.ImageInspectResult{inspectWith(tc.local)},
				distDigest: tc.remote,
				distErr:    tc.distErr,
			}
			got := AheadOfStore(context.Background(), mock.docker(), testRef, stateDir(t))
			if got != tc.want {
				t.Errorf("AheadOfStore() = %+v, want %+v", got, tc.want)
			}
			if mock.docker().DistributionInspectCalls() != 1 {
				t.Errorf("the registry was probed %d times, want 1", mock.docker().DistributionInspectCalls())
			}
		})
	}
}

// A ref with no repo digest in the store is the fingerprint of a local
// `toolbox build`. The prefetch abstains on it everywhere, and the question
// abstains too: there is nothing a remote digest could be compared against,
// and an automatic pull must never undo an explicit build.
func TestAheadOfStoreAbstainsOnALocalBuild(t *testing.T) {
	mock := &pollStub{inspects: []client.ImageInspectResult{inspectWith("")}}

	if got := AheadOfStore(context.Background(), mock.docker(), testRef, stateDir(t)); got != (StoreState{}) {
		t.Errorf("AheadOfStore() = %+v, want nothing established", got)
	}
	if mock.docker().DistributionInspectCalls() != 0 {
		t.Errorf("the registry was probed %d times for a locally built image", mock.docker().DistributionInspectCalls())
	}
}

// Probed is what keeps a cached answer from being reported as a fresh sync.
// Without it every shell opened inside the TTL would re-stamp the poller's
// attempt clock from a digest nobody re-established, and a developer who opens
// shells more often than the cadence would never probe the registry again.
func TestAheadOfStoreDoesNotClaimAProbeItReadFromTheCache(t *testing.T) {
	dir := stateDir(t)
	if err := os.WriteFile(filepath.Join(dir, cacheFile),
		[]byte(cacheBody("0", digestOld, stateNone, "0", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsx.TouchMarker(filepath.Join(dir, stampFile)); err != nil {
		t.Fatal(err)
	}
	mock := &pollStub{inspects: []client.ImageInspectResult{inspectWith(digestOld)}}

	got := AheadOfStore(context.Background(), mock.docker(), testRef, dir)
	if want := (StoreState{Known: true}); got != want {
		t.Errorf("AheadOfStore() = %+v, want %+v — current, but not established here", got, want)
	}
}
