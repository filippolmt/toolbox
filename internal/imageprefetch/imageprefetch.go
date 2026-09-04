// Package imageprefetch owns the host-side "is there a newer runtime image or
// CLI, and are its bytes already here?" concern for the lifetime of an
// attached shell. It is the single detector: the in-container
// toolbox-update-check poller it replaces asked the same question over a
// different transport (curl against canonical GHCR) and could not know
// whether the bytes had landed in the local store, which is the fact the
// banner now states.
//
// One act, three steps, all silent — the host process's stdout is the
// attached tty, so anything printed here lands in the middle of the
// developer's work:
//
//   - probe: DistributionInspect resolves the remote digest through the
//     daemon, so a configured registry_mirror is honoured (a direct HEAD to
//     ghcr.io would not be) and no registry HTTP, token dance or Accept
//     header lives in this repo.
//   - prefetch: when the remote digest differs from the local store's repo
//     digest, ImagePull drained with ImagePullResponse.Wait — never
//     io.Copy(io.Discard, …), because the daemon writes errors *into* a
//     200 stream.
//   - publish: the comparison the banner renders is local store versus the
//     digest the running container was created from, written to the
//     update-check cache the zsh precmd hook already reads.
//
// Cadence lives on the filesystem, not in the process: the ticker is only an
// alarm and every tick re-decides by stat'ing an attempt stamp on the state
// mount, so sibling sessions share one probe per TTL and the cadence survives
// a future re-exec of the host CLI for free. The stamp records the attempt,
// not the success — an offline machine is capped at one failed probe per TTL
// rather than one per tick — which is why it is a separate file from the
// Image Plan's pull-cache marker, whose "successful pulls only" semantics
// gate a different act (the silent refresh a session reload runs).
package imageprefetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/fsx"
)

// TTL bounds how long a previous probe attempt is trusted before the registry
// is asked again. Half an hour is the map's target cadence: short enough that
// an image merged this morning is downloaded before the afternoon of a
// multi-day session, long enough that the probe is invisible. Deliberately
// not the Image Plan's `pullTTL`, which gates the synchronous refresh on
// every shell start — retuning that one would re-probe every ordinary shell
// twice as often.
const TTL = 30 * time.Minute

// tickInterval is the alarm, not the cadence. Every tick re-reads the shared
// attempt stamp, so the real period is TTL and this only bounds how late a
// poll can be (TTL + tickInterval worst case). Small enough to keep that
// bound tight, large enough that a stat per tick is free.
//
// A var, not a const, so a tick-driven test runs in milliseconds — the same
// seam this package uses for releasesURL.
var tickInterval = 5 * time.Minute

// releasesURL is the GitHub endpoint carrying the newest published CLI tag.
// The CLI axis moved host-side with the image axis: the host *is* the CLI, so
// the comparison needs no injected version and no curl+jq inside the image.
// A var, not a const, so tests can point it at an httptest server — the same
// seam the repo uses wherever a syscall or a network edge has to be faked.
var releasesURL = "https://api.github.com/repos/filippolmt/toolbox/releases/latest"

// httpTimeout caps the releases call. The probe and the pull are bounded by
// the caller's context (cancelled when the shell exits); the plain HTTP call
// has no such owner, so it carries its own deadline.
const httpTimeout = 10 * time.Second

// Cache file names under the state mount. The result file is the pre-existing
// contract with the zsh precmd renderer — only the writer moved to the other
// side of the bind mount. The stamp is this package's own. Nothing here
// records what a shell has been shown: that is per-session state and lives in
// the shell, never on a mount every session shares.
const (
	cacheFile       = "update-check"
	stampFile       = "update-check.stamp"
	unavailableFile = "update-check.unavailable-since"
)

// The three states of the image axis, written to the cache's image_state
// field. Added *alongside* image_update rather than replacing it: an image
// that predates this field renders only image_update, and its own advice
// ("exit the shell and reopen it") stays true there, whereas replacing the
// field would mute that renderer permanently. Four bytes per write buys both
// image/CLI version combinations saying something true.
const (
	stateNone        = "none"        // nothing to say
	stateReady       = "ready"       // the local store is ahead of this session
	stateUnavailable = "unavailable" // the registry is ahead of the store, persistently
)

// Input is everything one poll needs, resolved by the caller at the container
// edge. ContainerDigest is read back from the running container rather than
// recomputed, so it stays true on the connect path — a host process attaching
// to a container someone else created never resolved that digest itself.
type Input struct {
	// Ref is the resolved base image reference (config `image` override or
	// `registry_mirror` host swap already applied). Never the `:local`
	// overlay tag: that one is built, not pulled.
	Ref string
	// ContainerDigest is the repo digest the attached container was created
	// from. Empty when unknown (a container created from a local build), in
	// which case no reload-worthiness claim is made.
	ContainerDigest string
	// StateDir is the host source of the state mount — the same directory the
	// container sees as ~/.toolbox-state. Resolved through the mount pipeline
	// so mounts_root and profiles are honoured; empty disables the whole act,
	// since there is then nowhere the renderer would read from.
	StateDir string
	// StartSynced records that the refresh at shell start established the
	// local store to be current with the registry as of a moment ago — a
	// successful pull, or a probe that found the store already current. A
	// synchronous probe is a probe: it takes this TTL's turn at the registry,
	// and the first poll publishes from the store instead of asking the same
	// question again. False for a failed probe, a failed pull and a declined
	// download, none of which leave the store provably current.
	StartSynced bool
	// CLIVersion is this process's own build version, the "current" half of
	// the CLI axis. Passed in rather than read from version.Version here: a
	// poll runs on its own goroutine for the life of the shell, and a package
	// var it dereferences there is a global read racing whoever writes it.
	// Empty, or "dev", abstains — an unreleased build has no tag to be behind.
	CLIVersion string
}

// registryStore is the pair this package compares: the local image store it
// reads a repo digest from, and the registry behind it — probed for the digest
// it serves and pulled from when the two differ. build.LocalRepoDigest is
// called with this value, and its own interface is a subset of it.
// → CONTEXT.md, Declared Docker Surface.
type registryStore interface {
	ImageInspect(ctx context.Context, ref string, opts ...client.ImageInspectOption) (client.ImageInspectResult, error)
	DistributionInspect(ctx context.Context, ref string, opts client.DistributionInspectOptions) (client.DistributionInspectResult, error)
	ImagePull(ctx context.Context, ref string, opts client.ImagePullOptions) (client.ImagePullResponse, error)
}

// Start runs the poller for the lifetime of ctx and returns immediately.
// Cancelling ctx on shell exit also cancels an in-flight pull, which #724
// measured to be safe: a partial ingest is never a blob and expires on its
// own, and the next pull resumes from what already landed.
func Start(ctx context.Context, cli registryStore, in Input) {
	if in.Ref == "" || in.StateDir == "" {
		return
	}
	// Read on the caller's goroutine: the alarm period is fixed for the
	// poller's life, and a poller that kept reading the package var would
	// race whoever rewinds it.
	tick := tickInterval
	go func() {
		// The shell start's synchronous refresh, when it actually reached the
		// registry, is this TTL's probe: stamping it here is what stops the
		// poller re-asking minutes after the shell just asked. The banner is
		// published straight away, from the store that refresh has just made
		// current, because it is the only moment image_latest may be restated
		// without a probe of our own.
		if in.StartSynced && stamp(in.StateDir) {
			publishFromStore(ctx, cli, in, true)
		}
		// Consumed: it describes the shell start, not the ticks that follow,
		// and a poll that kept reading it would re-stamp the attempt clock
		// from a digest nobody re-established.
		in.StartSynced = false

		// Poll straight away rather than waiting out the first tick: a
		// session shorter than tickInterval would otherwise never refresh the
		// banner at all. Redundant work is what the shared stamp prevents —
		// if a sibling probed within the TTL this returns without a syscall.
		reissue := newPoller(ctx, cli, in)
		reissue()

		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Cancel-and-reissue rather than a second concurrent poll: a
				// registry that accepts the connection and then stops talking
				// would otherwise hold the only poller goroutine inside
				// ImagePull for the rest of the session, and no later tick
				// would ever run. Cancelling is free — a partial ingest is
				// never a blob, it expires on its own, and the next pull
				// resumes from what already landed.
				reissue()
			}
		}
	}()
}

// newPoller returns a function that cancels the poll it started last time and
// starts a fresh one. Each poll runs in its own goroutine under its own child
// context, so a hung one costs exactly one tick instead of the session; the
// child contexts derive from ctx, so shell exit still cancels whatever is in
// flight. A reissued poll usually finds the attempt stamp fresh and returns
// without a syscall — the cancel is the point, the reissue only keeps the
// alarm and the act on the same clock.
func newPoller(ctx context.Context, cli registryStore, in Input) func() {
	var cancelPrev context.CancelFunc
	return func() {
		if cancelPrev != nil {
			cancelPrev()
		}
		pollCtx, cancel := context.WithCancel(ctx)
		cancelPrev = cancel
		go Poll(pollCtx, cli, in)
	}
}

// publishFromStore refreshes the banner without asking the registry anything.
// The session axis is always this session's to state — whether the container
// is behind the local store is a local comparison, and the published result
// carries whoever wrote it last, which on a state mount shared by every
// workspace need not be a session running this container.
//
// synced says the shell start's refresh has just made the store current with
// the registry. It governs the two claims that are about the *registry* and
// therefore cannot be made from the store alone:
//
//   - image_latest is the digest the registry serves, and knownRemote reads it
//     back as exactly that for AheadOfStore. Only a sync may restate it — with
//     the store's digest, which at that moment is the registry's. Overwritten
//     without one, the start-up prompt would stop offering a refresh that is
//     genuinely available, for the rest of the TTL.
//   - imageState's "unavailable" verdict rides a first-failure clock that
//     imageState clears whenever the store is not behind. Saying "not behind"
//     on nothing established would restart that clock on every shell start,
//     and the word would never be earned. So the store's standing verdict is
//     asked for when a probe published one, and otherwise left alone — except
//     that an adoptable image still outranks it, which is imageState's own
//     rule and the one the renderer relies on to never print both lines.
func publishFromStore(ctx context.Context, cli registryStore, in Input, synced bool) {
	local, ok := localDigest(ctx, cli, in.Ref)
	if !ok {
		// The fingerprint of a local `toolbox build`: the prefetch abstains
		// on it everywhere, and this path is no exception.
		return
	}
	res := readResult(in.StateDir)
	res.imageUpdate = in.ContainerDigest != "" && local != in.ContainerDigest

	remote, known := knownRemote(in.StateDir)
	switch {
	case synced:
		res.imageLatest = local
		res.imageState = imageState(in.StateDir, false, res.imageUpdate)
	case known:
		res.imageState = imageState(in.StateDir, remote != local, res.imageUpdate)
	case res.imageUpdate:
		res.imageState = stateReady
	}
	writeResult(in.StateDir, res)
}

// AheadOfStore reports whether the registry holds a newer image than the local
// store for ref, and whether that could be established at all. It is the
// question the synchronous start-up refresh has to answer before it can decide
// whether to ask the developer anything, and it lives here because both ways
// of answering it already do: the shared probe cache and the probe itself.
//
// A warm attempt stamp answers from the published result — a sibling session
// probed a moment ago and the fact is established, so re-establishing it would
// reintroduce, one step higher, the latency the prompt exists to remove. Only
// a cold stamp probes, which is precisely the case where the answer is most
// likely to be "yes": no session has been open recently, so the store is
// probably behind.
//
// Deliberately does **not** stamp the shared attempt clock the way Poll does.
// The stamp gates the poller, and the poller's immediate first pass is what
// fetches the bytes a developer has just postponed: claiming this TTL's turn
// here would leave a declined download with nobody to download it. The caller
// says what it established through Input.StartSynced instead, which is a claim
// about the store rather than about the clock.
//
// Not established (false, false) on a store with no repo digest for ref — the
// fingerprint of a local `toolbox build`, which the prefetch abstains on
// everywhere — and on a probe that does not answer. Both leave the caller with
// nothing to offer, which is the honest outcome of a question that was never
// answered.
func AheadOfStore(ctx context.Context, cli registryStore, ref, stateDir string) StoreState {
	local, ok := localDigest(ctx, cli, ref)
	if !ok {
		return StoreState{}
	}
	remote, cached := knownRemote(stateDir)
	if !cached {
		var err error
		if remote, err = remoteDigest(ctx, cli, ref); err != nil {
			return StoreState{}
		}
	}
	return StoreState{Ahead: remote != local, Known: true, Probed: !cached}
}

// StoreState is how AheadOfStore answered, and the third field is why the type
// exists: an answer read from the shared cache says something true about the
// registry as of that probe, but it establishes nothing *now*, so a caller
// must not report the store as freshly synced on the strength of it. Doing so
// would let the poller re-stamp the attempt clock from a cached digest on
// every shell start, and a developer who opens shells more often than the TTL
// would never probe the registry again.
type StoreState struct {
	// Ahead is true when the registry holds a newer image than the store.
	Ahead bool
	// Known is false when the comparison could not be made at all: a probe
	// that did not answer, or a store with no repo digest for the ref.
	Known bool
	// Probed is true only when the remote digest came from a live round-trip
	// made here, rather than from the shared cache.
	Probed bool
}

// attemptFresh reports whether the shared attempt stamp is still inside the
// TTL — the one gate two callers read: the poll it turns away from the
// registry, and the cached remote digest it keeps valid.
func attemptFresh(stateDir string) bool {
	return fsx.MarkerFresh(filepath.Join(stateDir, stampFile), TTL)
}

// knownRemote returns the remote digest the last shared probe published, valid
// only while that probe's attempt stamp is still inside the TTL. The stamp
// records the attempt and the cache records the answer, so both are needed: a
// stamp with no published digest is a probe that failed.
func knownRemote(stateDir string) (string, bool) {
	if stateDir == "" || !attemptFresh(stateDir) {
		return "", false
	}
	remote := readResult(stateDir).imageLatest
	return remote, remote != ""
}

// Poll runs one gated attempt: it asks the registry nothing while the shared
// attempt stamp is younger than TTL. Exported because it, and not the ticker,
// is the unit under test — the alarm carries no decision.
//
// The gate stops the *registry*, not the banner. One state mount serves every
// workspace while image_update is computed against the publishing session's
// container, so the result on disk may be a sibling's — true for its container
// and false for this one — and the sibling keeping the stamp warm is exactly
// what holds this gate shut. Every gated pass therefore restates the session
// axis from the local store, which is a comparison the registry has no part
// in. Doing it here rather than once at start-up is the point: a session
// outlives many ticks, and a sibling that keeps probing would otherwise own
// the banner for the rest of that session — silently, on the connect branch,
// where the start-up refresh is never offered and the banner is all there is.
//
// Every failure path is silent and leaves the previous cached result in
// place. A banner is an advisory, and this runs beside a developer's cursor.
func Poll(ctx context.Context, cli registryStore, in Input) {
	if attemptFresh(in.StateDir) {
		publishFromStore(ctx, cli, in, false)
		return
	}
	// Stamp before the network, so a persistently failing probe is capped at
	// one attempt per TTL instead of one per tick.
	if !stamp(in.StateDir) {
		return
	}

	res, reached := collect(ctx, cli, in, readResult(in.StateDir))
	if !reached {
		// Offline, every axis failed, or nothing to ask: keep the last result
		// the registry actually answered for rather than blanking a still
		// valid banner.
		return
	}
	writeResult(in.StateDir, res)
}

// result is the cache body, field-for-field the contract the zsh precmd
// renderer parses. Names are bound to the shell side by
// TestUpdateCheckCacheContract.
type result struct {
	imageUpdate bool
	imageLatest string
	imageState  string
	cliUpdate   bool
	cliLatest   string
}

// collect runs both axes over the currently published result and reports
// whether either of them reached its registry. The axes are independent — either can
// fire, fail or abstain without the other — so each one overwrites only its
// own fields, and one that did not reach its registry leaves the previous
// answer standing instead of retracting a banner that is still true. An axis
// that abstains outright writes nothing at all.
func collect(ctx context.Context, cli registryStore, in Input, res result) (result, bool) {
	res, imageReached := collectImage(ctx, cli, in, res)
	res, cliReached := collectCLI(ctx, in, res)
	return res, imageReached || cliReached
}

// collectImage runs the image axis and reports whether it reached the
// registry. Two abstentions come first and cost nothing: a store with no repo
// digest for the ref is a local `toolbox build`, and a probe that does not
// answer leaves the previous result standing.
func collectImage(ctx context.Context, cli registryStore, in Input, res result) (result, bool) {
	local, ok := localDigest(ctx, cli, in.Ref)
	if !ok {
		return res, false
	}
	remote, err := remoteDigest(ctx, cli, in.Ref)
	if err != nil {
		return res, false
	}

	res.imageLatest = remote
	if remote != local {
		local = fetched(ctx, cli, in.Ref, local)
	}
	// The reload-worthiness comparison: local store against what this
	// container was created from, not against the remote.
	res.imageUpdate = in.ContainerDigest != "" && local != in.ContainerDigest
	res.imageState = imageState(in.StateDir, local != remote, res.imageUpdate)
	return res, true
}

// fetched pulls ref and reports the digest the local store holds afterwards.
// A failed pull, or a store that can no longer be read, returns `have`
// unchanged: the banner stays silent rather than announcing bytes that never
// landed.
func fetched(ctx context.Context, cli registryStore, ref, have string) string {
	if pull(ctx, cli, ref) != nil {
		return have
	}
	if landed, ok := localDigest(ctx, cli, ref); ok {
		return landed
	}
	return have
}

// collectCLI runs the CLI axis and reports whether it reached GitHub. An
// unstamped build ("dev", or empty in a test binary) has no release to be
// behind, so it abstains before the network.
func collectCLI(ctx context.Context, in Input, res result) (result, bool) {
	if in.CLIVersion == "" || in.CLIVersion == "dev" {
		return res, false
	}
	tag, err := latestRelease(ctx)
	if err != nil {
		return res, false
	}
	res.cliLatest = tag
	res.cliUpdate = newerVersion(in.CLIVersion, tag)
	return res, true
}

// localDigest reports the repo digest the local store holds for ref. The
// second return is false when there is none, which is both the "image not
// pulled yet" case and the fingerprint of a local `toolbox build`: a repo
// digest exists only once an image has been pushed or pulled. The prefetch
// abstains on it, so an explicit build is never silently overwritten by an
// automatic pull — and it self-heals, since a manual `docker pull` restores
// the digest and the prefetch resumes.
func localDigest(ctx context.Context, cli registryStore, ref string) (string, bool) {
	d, _ := build.LocalRepoDigest(ctx, cli, ref)
	return d, d != ""
}

// remoteDigest asks the daemon for the registry's current digest for ref.
// DistributionInspect needs no credentials against a public GHCR package and
// routes through the daemon's pull endpoints, so a registry_mirror is
// authoritative here — deliberately, since the only probe that survives is
// the one that leads to a pull.
func remoteDigest(ctx context.Context, cli registryStore, ref string) (string, error) {
	res, err := cli.DistributionInspect(ctx, ref, client.DistributionInspectOptions{})
	if err != nil {
		return "", err
	}
	return res.Descriptor.Digest.String(), nil
}

// pull fetches ref quietly. Wait decodes the progress stream and surfaces the
// in-band error the daemon writes into an already-200 response; draining to
// io.Discard would report success on a failed pull.
func pull(ctx context.Context, cli registryStore, ref string) error {
	resp, err := cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer resp.Close()
	return resp.Wait(ctx)
}

// latestRelease returns the newest published CLI tag name.
func latestRelease(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.TagName == "" {
		return "", errors.New("github releases: response carries no tag_name")
	}
	return body.TagName, nil
}

// newerVersion reports whether latest is strictly ahead of current, comparing
// dot-separated numeric fields (the shape every toolbox release tag has). A
// leading "v" and any pre-release/build suffix are stripped; a field that is
// not a number compares as 0, so a malformed tag degrades to "not newer"
// rather than nagging about a release that does not exist.
func newerVersion(current, latest string) bool {
	cur, next := versionFields(current), versionFields(latest)
	for i := 0; i < len(cur) || i < len(next); i++ {
		a, b := fieldAt(cur, i), fieldAt(next, i)
		if a != b {
			return b > a
		}
	}
	return false
}

// versionFields splits a tag into its numeric components.
func versionFields(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// fieldAt reads a version component, treating a missing one as 0 so "1.2"
// and "1.2.0" compare equal.
func fieldAt(fields []int, i int) int {
	if i < len(fields) {
		return fields[i]
	}
	return 0
}

// stamp records this attempt and reports whether the state dir is usable at
// all. An un-writable state dir means the renderer would never see a result
// either, so the poll is abandoned rather than run for nothing.
func stamp(stateDir string) bool {
	return fsx.TouchMarker(filepath.Join(stateDir, stampFile)) == nil
}

// imageState classifies the image axis for the banner. Single-valued by
// design — the renderer prints one line — so an adoptable image outranks a
// failed download: "ready" is something the developer can act on, and the
// registry being unreachable is worth saying only when there is nothing
// better to say.
//
// storeBehind means the registry is ahead of the local store: the bytes did
// not land. That is not theoretical — the probe works anonymously against a
// public package while the pull can fail on expired credentials, so it can
// hold for days. But one failure is a dropped connection, not a broken
// registry, so the word is earned only once a *first* failure is a full
// cadence old — at least two consecutive attempts. A timestamp rather than a
// consecutive-failure counter, so retuning the cadence does not change what
// it means; on the state mount beside the attempt stamp, so it is shared
// across sibling sessions.
func imageState(stateDir string, storeBehind, sessionBehind bool) string {
	marker := filepath.Join(stateDir, unavailableFile)
	if !storeBehind {
		// The bytes are here: the failure history must not outlive the
		// condition it describes.
		_ = os.Remove(marker)
		if sessionBehind {
			return stateReady
		}
		return stateNone
	}
	if sessionBehind {
		// Older bytes are still adoptable; say the useful thing.
		return stateReady
	}
	if fsx.MarkerOlderThan(marker, TTL) {
		return stateUnavailable
	}
	// First sight of the failure — start the clock without resetting it on
	// every later attempt, or the window would never elapse.
	if _, err := os.Stat(marker); err != nil {
		_ = fsx.TouchMarker(marker)
	}
	return stateNone
}

// readResult returns the currently published result, or the zero value when
// there is none. Parsed rather than assumed so an axis that could not reach
// its registry carries its previous answer forward instead of retracting it —
// the renderer keys its show-once on the whole file, so a blanked field would
// both hide a true banner and re-fire the remaining one.
func readResult(stateDir string) result {
	var res result
	raw, err := os.ReadFile(filepath.Join(stateDir, cacheFile))
	if err != nil {
		return res
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "image_update":
			res.imageUpdate = value == "1"
		case "image_latest":
			res.imageLatest = value
		case "image_state":
			res.imageState = value
		case "cli_update":
			res.cliUpdate = value == "1"
		case "cli_latest":
			res.cliLatest = value
		}
	}
	return res
}

// writeResult publishes the comparison for the renderer. Atomic because
// sibling sessions are N host processes writing one file, and the reader is a
// prompt hook that must never observe a half-written body.
func writeResult(stateDir string, res result) {
	if res.imageState == "" {
		res.imageState = stateNone
	}
	body := strings.Join([]string{
		"image_update=" + boolField(res.imageUpdate),
		"image_latest=" + res.imageLatest,
		"image_state=" + res.imageState,
		"cli_update=" + boolField(res.cliUpdate),
		"cli_latest=" + res.cliLatest,
		"",
	}, "\n")
	// Best-effort: a full or read-only state mount costs a banner, not a shell.
	_ = fsx.AtomicWriteFile(filepath.Join(stateDir, cacheFile), []byte(body), 0o644)
}

// ClearResult drops the published result, the attempt stamp and the legacy
// shown-signature. Called by a session reload, whose container is new while
// all of them still describe the old one.
//
// Deletion, never a rewrite with the digest just landed on: the state mount is
// shared across every session the user runs, so a rewritten result would tell
// a sibling still on the old image that it is up to date. Deletion stays true
// for every reader, and it costs exactly one extra probe — which is the point
// of clearing the stamp too rather than leaving the next poll gated behind up
// to a full TTL of a cadence the reload has just invalidated.
//
// The unavailable-since marker is deliberately left: it records whether the
// registry can be reached, which a reload does not change.
func ClearResult(stateDir string) {
	if stateDir == "" {
		return
	}
	// The `.shown` sibling is no longer written by this image's renderer, which
	// keeps its signature in the shell. It is still removed: a new CLI driving
	// an older image is skew this repo supports, that renderer does read the
	// file, and the removal also retires the orphan left on existing mounts.
	for _, name := range []string{cacheFile, cacheFile + ".shown", stampFile} {
		// Best-effort: a stale banner is the whole cost of failing here.
		_ = os.Remove(filepath.Join(stateDir, name))
	}
}

// boolField renders a flag in the cache's 0/1 spelling, which the shell-side
// renderer compares as a string.
func boolField(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
