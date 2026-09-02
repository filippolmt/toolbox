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

// mockClient implements the three Docker calls one poll can make. The
// embedded nil client.APIClient is the point: an unstubbed call panics, so a
// probe or pull that should never have happened fails the test loudly instead
// of returning a silent zero value.
type mockClient struct {
	client.APIClient

	// inspects are returned in order, one per ImageInspect call, so a test can
	// distinguish the digest before a pull from the digest after it.
	inspects  []client.ImageInspectResult
	inspectN  int
	distErr   error
	distDigst string
	pullErr   error
	// pullHang makes ImagePull block until its context is cancelled, then
	// signals on the channel. It is how a registry that accepts the
	// connection and then stops talking is spelled in a test.
	pullHang chan struct{}

	distCalls int
	pullCalls int
}

func (m *mockClient) ImageInspect(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	if m.inspectN >= len(m.inspects) {
		return client.ImageInspectResult{}, errors.New("no such image")
	}
	res := m.inspects[m.inspectN]
	m.inspectN++
	return res, nil
}

func (m *mockClient) DistributionInspect(context.Context, string, client.DistributionInspectOptions) (client.DistributionInspectResult, error) {
	m.distCalls++
	if m.distErr != nil {
		return client.DistributionInspectResult{}, m.distErr
	}
	return dockertest.DistributionResult(m.distDigst), nil
}

func (m *mockClient) ImagePull(ctx context.Context, _ string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
	m.pullCalls++
	if m.pullHang != nil {
		<-ctx.Done()
		m.pullHang <- struct{}{}
		return nil, ctx.Err()
	}
	if m.pullErr != nil {
		return nil, m.pullErr
	}
	return dockertest.PullResponse{ReadCloser: io.NopCloser(bytes.NewReader(nil))}, nil
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
// alarm, so a tick that fires inside the window must not reach the daemon at
// all. The nil-embedded mock panics on any call, which is the assertion.
func TestPollSkipsWhileStampIsFresh(t *testing.T) {
	dir := stateDir(t)
	if err := os.WriteFile(filepath.Join(dir, stampFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cli := &mockClient{}
	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.distCalls != 0 {
		t.Errorf("DistributionInspect called %d times, want 0 inside the TTL", cli.distCalls)
	}
	if got := readCache(t, dir); got != "" {
		t.Errorf("cache written inside the TTL: %q", got)
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

	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith(digestOld)}, distDigst: digestOld}
	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.distCalls != 1 {
		t.Errorf("DistributionInspect called %d times, want 1 past the TTL", cli.distCalls)
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

	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith(digestOld)}, distErr: errors.New("dial tcp: no route to host")}
	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if _, err := os.Stat(filepath.Join(dir, stampFile)); err != nil {
		t.Errorf("stamp not written on a failed probe: %v", err)
	}
	if got := readCache(t, dir); got != seeded {
		t.Errorf("cache rewritten on a failed probe:\ngot  %q\nwant %q", got, seeded)
	}
	if cli.pullCalls != 0 {
		t.Errorf("pulled %d times after a failed probe, want 0", cli.pullCalls)
	}
}

// An image with no repo digest was produced by a local `toolbox build`. The
// prefetch abstains entirely: an explicit act by the developer is never
// undone by an automatic one.
func TestPollAbstainsOnLocallyBuiltImage(t *testing.T) {
	dir := stateDir(t)
	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith("")}}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.distCalls != 0 {
		t.Errorf("probed %d times for a locally built image, want 0", cli.distCalls)
	}
	if cli.pullCalls != 0 {
		t.Errorf("pulled %d times for a locally built image, want 0", cli.pullCalls)
	}
}

// The happy path: remote is ahead of the local store, so the bytes are
// fetched and the banner states that this session is behind what is now on
// disk — the local-store-versus-container comparison, not the remote one.
func TestPollPullsAndReportsTheSessionBehind(t *testing.T) {
	dir := stateDir(t)
	cli := &mockClient{
		inspects:  []client.ImageInspectResult{inspectWith(digestOld), inspectWith(digestNew)},
		distDigst: digestNew,
	}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.pullCalls != 1 {
		t.Errorf("pulled %d times, want 1", cli.pullCalls)
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
	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith(digestNew)}, distDigst: digestNew}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	if cli.pullCalls != 0 {
		t.Errorf("pulled %d times with the store already current, want 0", cli.pullCalls)
	}
	if got := readCache(t, dir); got != cacheBody("1", digestNew, stateReady, "0", "") {
		t.Errorf("cache: %q", got)
	}
}

// Nothing moved: the container runs what the store holds and what the
// registry serves. The banner must stay silent.
func TestPollStaysSilentWhenTheSessionIsCurrent(t *testing.T) {
	dir := stateDir(t)
	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith(digestNew)}, distDigst: digestNew}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestNew, StateDir: dir})

	if got := readCache(t, dir); got != cacheBody("0", digestNew, stateNone, "0", "") {
		t.Errorf("cache: %q", got)
	}
}

// A probe that succeeds while the pull fails — expired registry credentials,
// the case imagepull warns about — must not announce bytes that never landed.
// (The dedicated "unavailable" wording is the banner ticket's, not this one's.)
func TestPollStaysSilentWhenThePullFails(t *testing.T) {
	dir := stateDir(t)
	cli := &mockClient{
		inspects:  []client.ImageInspectResult{inspectWith(digestOld)},
		distDigst: digestNew,
		pullErr:   errors.New("unauthorized"),
	}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	want := cacheBody("0", digestNew, stateNone, "0", "")
	if got := readCache(t, dir); got != want {
		t.Errorf("cache:\ngot  %q\nwant %q", got, want)
	}
}

// A container created from an image with no repo digest has no baseline, so
// no claim is made about it either way.
func TestPollMakesNoClaimWithoutAContainerDigest(t *testing.T) {
	dir := stateDir(t)
	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith(digestNew)}, distDigst: digestNew}

	Poll(t.Context(), cli, Input{Ref: testRef, StateDir: dir})

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
	Start(ctx, &mockClient{}, Input{Ref: testRef, StateDir: t.TempDir()})
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
	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith("")}}
	Poll(t.Context(), cli, Input{Ref: testRef, StateDir: dir, CLIVersion: "v1.0.0"})

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

	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith("")}}
	Poll(t.Context(), cli, Input{Ref: testRef, StateDir: dir, CLIVersion: "dev"})

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
	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith(digestNew)}, distDigst: digestNew}
	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir, CLIVersion: "v1.0.0"})

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
	cli := &mockClient{inspects: []client.ImageInspectResult{inspectWith(digestOld)}, distDigst: digestNew}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

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
	cli := &mockClient{
		inspects:  []client.ImageInspectResult{inspectWith(digestNew)},
		distDigst: digestNewer,
		pullErr:   errors.New("unauthorized"),
	}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestNew, StateDir: dir})

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
	cli := &mockClient{
		inspects:  []client.ImageInspectResult{inspectWith(digestNew)},
		distDigst: digestNewer,
		pullErr:   errors.New("unauthorized"),
	}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestNew, StateDir: dir})

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

	cli := &mockClient{
		inspects:  []client.ImageInspectResult{inspectWith(digestNew)},
		distDigst: digestNewer,
		pullErr:   errors.New("unauthorized"),
	}
	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestNew, StateDir: dir})

	if after := mtime(t, filepath.Join(dir, unavailableFile)); !after.Equal(before) {
		t.Errorf("first-failure marker rewritten: %v -> %v", before, after)
	}
}

// A successful download clears the history: `unavailable` must not outlive
// the condition it describes.
func TestPollClearsUnavailableOnceTheBytesLand(t *testing.T) {
	dir := stateDir(t)
	unavailableAge(t, dir, 2*TTL)
	cli := &mockClient{
		inspects:  []client.ImageInspectResult{inspectWith(digestOld), inspectWith(digestNew)},
		distDigst: digestNew,
	}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

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
	cli := &mockClient{
		inspects:  []client.ImageInspectResult{inspectWith(digestNew)},
		distDigst: digestNewer,
		pullErr:   errors.New("unauthorized"),
	}

	Poll(t.Context(), cli, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

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
// #834 settled it as a named set rather than a sweep. The result and the shown
// signature describe the container the reload just retired; the attempt stamp
// gates the next probe behind up to a full cadence, and the reload has just
// invalidated the answer that cadence was throttling — deleting it is what
// makes the documented "costs one extra probe" true.
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
	m := &mockClient{inspects: []client.ImageInspectResult{inspectWith(digestNew)}}

	startPoller(t, m, Input{
		Ref:             testRef,
		ContainerDigest: digestOld,
		StateDir:        dir,
		StartSynced:     true,
	})

	waitFor(t, func() bool { return readCache(t, dir) != "" })

	if got, want := readCache(t, dir), cacheBody("1", digestNew, stateReady, "0", ""); got != want {
		t.Errorf("cache = %q, want %q", got, want)
	}
	if m.distCalls != 0 {
		t.Errorf("DistributionInspect calls = %d, want 0 — the shell start already probed", m.distCalls)
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
	m := &mockClient{
		inspects:  []client.ImageInspectResult{inspectWith(digestNew)},
		distDigst: digestNew,
	}

	startPoller(t, m, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

	waitFor(t, func() bool { return readCache(t, dir) != "" })
	if m.distCalls == 0 {
		t.Error("DistributionInspect calls = 0, want the poller's own probe")
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
	m := &mockClient{
		inspects:  []client.ImageInspectResult{inspectWith(digestOld)},
		distDigst: digestNew,
		pullHang:  cancelled,
	}

	startPoller(t, m, Input{Ref: testRef, ContainerDigest: digestOld, StateDir: dir})

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
func startPoller(t *testing.T, cli client.APIClient, in Input) {
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
