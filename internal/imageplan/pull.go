// The pull half of the Image Plan: "refresh the canonical image, best-effort,
// with a TTL cache". A file rather than a package, because its whole interface
// was two functions differing by one cache check and no code outside this
// package ever called either — the policy switch that chooses between them
// lives in imageplan.go, and the asymmetry between them (one stamps a marker
// only the other reads) is only legible beside it.
//
// The cache marker file lives at <state dir>/pull-cache/<sha256-of-ref> so it
// survives across CLI runs; only successful pulls record, so a network blip
// doesn't poison the next invocation into staleness.
//
// The state dir is passed in, not resolved here: it is the host source of the
// toolbox state mount, which a mounts_root or a --profile retargets. Deriving
// it from $HOME would pin the cache to the default location while every other
// toolbox-managed marker followed the retarget — see cache.markerPath.
//
// Both seams are best-effort (callers proceed with the local image
// regardless): refreshIfStale is the cache-aware form (cache-hit fast-path,
// pull on miss, record on success), and forcePull pulls unconditionally — for
// the "always" policy, and for a shell start whose probe already established
// that the registry is ahead. UI surfacing (per-layer progress,
// success/warning lines) stays here so the pull concern owns its own
// observability end-to-end.

package imageplan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/jsonmessage"
	"golang.org/x/term"

	"github.com/filippolmt/toolbox/internal/build"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/ui"
)

// registry is the registry the pull half reaches, as narrow as the act: one
// method, and every caller inside this package passes something that has it.
// Narrower than the tree's own imageSource on purpose — a function that only
// pulls should not be able to inspect. → CONTEXT.md, Declared Docker Surface.
type registry interface {
	ImagePull(ctx context.Context, ref string, opts client.ImagePullOptions) (client.ImagePullResponse, error)
}

// pullTTL bounds how long a previous successful manifest check is trusted
// before the registry is asked again. One hour is short enough that a freshly
// pushed image lands on developer machines within the same work block, and long
// enough that rapid `toolbox shell` cycles (open → exit → open) don't each
// pay a round-trip to GHCR. It gates refreshIfStale, which is the session
// reload's refresh — a shell start decides from a digest probe instead, and
// deliberately: a warm cache there is what let a released image go unoffered
// for a whole window. Not to be confused with imageprefetch's `probeTTL`,
// which paces the background probe. Override is intentional fs-only: delete
// <state dir>/pull-cache/* to force a fresh pull on next invocation — the
// session's resolved state dir, which a mounts_root or a --profile moves
// (~/.toolbox/toolbox/state only when neither does).
const pullTTL = 1 * time.Hour

// refreshIfStale refreshes the registry image at ref, best-effort, unless
// a recent successful pull is still within pullTTL. Errors are logged as
// warnings and swallowed: the caller proceeds with the local image.
//
// Reports whether a registry round trip actually succeeded now. Only that
// answer lets the background update prefetch skip its own probe: a cache hit
// did no work at all, and a failed pull leaves the local store possibly
// behind the registry, which is the one thing the prefetch exists to notice.
func refreshIfStale(ctx context.Context, cli registry, ref, stateDir string) bool {
	c := cache{dir: stateDir}
	if c.fresh(ref) {
		return false
	}
	return pullAndRecord(ctx, cli, ref, c)
}

// forcePull pulls ref unconditionally, ignoring the TTL cache. Backs the
// "always" pull policy: the user has asked for a registry round-trip on every
// shell, so a recent cache hit must not short-circuit it. Best-effort like
// refreshIfStale — failures are warned and the caller falls back to the local
// image.
func forcePull(ctx context.Context, cli registry, ref, stateDir string) bool {
	return pullAndRecord(ctx, cli, ref, cache{dir: stateDir})
}

// pullAndRecord pulls ref and stamps the cache marker on success — the shared
// tail of refreshIfStale (after the cache check) and forcePull.
func pullAndRecord(ctx context.Context, cli registry, ref string, c cache) bool {
	if !pull(ctx, cli, ref) {
		return false
	}
	c.stamp(ref)
	return true
}

// pull attempts to pull the image from its remote registry. The pull
// stream is rendered with per-layer progress bars on a TTY, or as plain
// status lines otherwise — the caller gets real-time feedback instead of
// a silent hang while layers download. Returns true on a clean pull so
// the caller can record it in the cache; returns false on any failure
// path so a poisoned cache never silently masks broken connectivity.
//
// Auth failures get a dedicated warning with the remediation path because
// "using local image if present" silently hides the expired-token case —
// the user keeps running a stale image and only finds out when the next
// release fails to land. The actionable hint (`docker login ghcr.io`)
// turns an opaque warning into a single-command fix.
func pull(ctx context.Context, cli registry, ref string) bool {
	ui.Info("Checking for image updates: " + ref + "...")
	rc, err := cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		warnPullError(ref, "image pull failed", err)
		return false
	}
	defer rc.Close()

	// Pull progress is diagnostic; keep stdout clean for program output.
	fd := os.Stderr.Fd()
	isTerm := term.IsTerminal(int(fd))
	if err := jsonmessage.DisplayJSONMessagesStream(rc, os.Stderr, fd, isTerm, nil); err != nil {
		warnPullError(ref, "image pull stream error", err)
		return false
	}
	ui.Success("Image up to date: " + ref)
	return true
}

// warnPullError dispatches between auth-specific guidance and generic
// "using local image" wording. Detection inspects both the
// jsonstream.Error code (when the stream carried a structured error)
// and the error string (when ImagePull surfaced a plain network/auth
// error before the stream started) — auth signals show up at both layers
// depending on how the registry rejects the request.
func warnPullError(ref, prefix string, err error) {
	if isAuthError(err) {
		ui.Warning(prefix + " (auth): " + err.Error())
		ui.Warning("registry rejected request — run `docker login " + registryOf(ref) + "` to refresh credentials. Using local image if present.")
		return
	}
	ui.Warning(prefix + ", using local image if present: " + err.Error())
}

// isAuthError reports whether err denotes an authentication/authorization
// failure surfaced from the registry. Two signal sources: a structured
// jsonstream.Error with HTTP 401/403, and the freeform error string
// for cases that bypass the stream (e.g. transport-layer rejection from
// ImagePull itself). String matching is intentionally broad — registries
// use varied phrasing ("unauthorized", "denied", "authentication
// required") and a false positive only changes the wording, not behavior.
func isAuthError(err error) bool {
	if jerr, ok := errors.AsType[*jsonstream.Error](err); ok {
		if jerr.Code == 401 || jerr.Code == 403 {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{"unauthorized", "authentication required", "denied", "forbidden", "401", "403"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// registryOf extracts the registry host from an image ref for the
// `docker login` hint. Refs without an explicit registry default to
// docker.io (Docker Hub's documented anonymous host). Best-effort: a
// malformed ref falls through to docker.io rather than printing an
// empty hostname in the user-facing remediation line.
func registryOf(ref string) string {
	if host, _ := build.SplitRegistryHost(ref); host != "" {
		return host
	}
	return "docker.io"
}

// cache is the TTL marker store under one session's resolved state dir. The
// dir travelled beside every ref through six signatures before it was a type;
// naming it puts the "which cache" question in one place and leaves the
// functions asking only about the ref.
//
// The zero value is the honest representation of a session that resolved no
// state mount: there is nowhere to keep markers, so nothing is fresh and
// nothing records. See markerPath.
type cache struct{ dir string }

// markerPath returns the marker path for ref in this cache. The ref is hashed
// because tags can contain characters that are awkward in filenames (digests,
// registry paths with ":" / "/").
//
// A cache with no dir is an error, not a fallback to the default location: it
// means the session resolved no state mount (the user disabled it), and the
// callers read that as "no cache" — one registry round-trip per invocation
// instead of one per TTL. Guessing a path the session does not use would put
// the cache somewhere nothing else looks, and the container could not see it.
func (c cache) markerPath(ref string) (string, error) {
	if c.dir == "" {
		return "", errors.New("no toolbox state dir resolved for this session")
	}
	sum := sha256.Sum256([]byte(ref))
	return filepath.Join(c.dir, "pull-cache", hex.EncodeToString(sum[:])), nil
}

// fresh reports whether a successful pull of ref happened within the
// last pullTTL. Any error (no state dir, missing marker, stat failure)
// returns false so the caller falls through to a real pull — never
// silently skip on uncertainty.
func (c cache) fresh(ref string) bool {
	path, err := c.markerPath(ref)
	if err != nil {
		return false
	}
	return fsx.MarkerFresh(path, pullTTL)
}

// stamp writes a fresh marker after a successful pull. Persist failures
// (mkdir/write rejection from ENOSPC / EROFS / permissions) don't break the
// user — the next invocation just pulls again — but a permanently un-writable
// cache silently turns every shell into a full registry round-trip, which adds
// latency and consumes GHCR rate budget. One warning per failure surfaces the
// root cause without spamming: once the underlying issue is fixed the warning
// stops on its own.
//
// A session with no state dir is not such a failure and is silent: there is
// nowhere to keep a cache because the user disabled the state mount, and
// "fix the underlying issue" names nothing they did wrong. fresh answers the
// same input the same way, and imageprefetch.Start returns early on it.
// Marker contents are intentionally empty — modtime is the timestamp.
func (c cache) stamp(ref string) {
	if c.dir == "" {
		return
	}
	path, err := c.markerPath(ref)
	if err != nil {
		ui.Warning("pull cache: cannot resolve marker path: " + err.Error())
		return
	}
	if err := fsx.TouchMarker(path); err != nil {
		ui.Warning("pull cache: " + err.Error())
	}
}
