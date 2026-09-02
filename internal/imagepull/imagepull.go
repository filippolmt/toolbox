// Package imagepull owns the "refresh canonical image, best-effort, with
// a TTL cache" concern that used to live inline in container.Shell. The
// cache marker file lives at ~/.toolbox/toolbox/state/pull-cache/<sha256-of-ref>
// so it survives across CLI runs; only successful pulls record, so a
// network blip doesn't poison the next invocation into staleness.
//
// Two seams, both best-effort (callers proceed with the local image
// regardless): RefreshIfStale(ctx, cli, ref) is the cache-aware default
// (cache-hit fast-path, pull on miss, record on success), and ForcePull
// pulls unconditionally for the "always" policy. UI surfacing (per-layer
// progress, success/warning lines) stays inside this package so the
// pull concern owns its own observability end-to-end.
package imagepull

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

// TTL bounds how long we trust a previous successful manifest check
// before re-asking the registry. One hour is short enough that a freshly
// pushed image lands on developer machines within the same work block,
// and long enough that rapid `toolbox shell` cycles (open → exit → open)
// don't each pay a round-trip to GHCR. Override is intentional fs-only:
// delete ~/.toolbox/toolbox/state/pull-cache/* to force a fresh pull on next
// invocation.
const TTL = 1 * time.Hour

// RefreshIfStale refreshes the registry image at ref, best-effort, unless
// a recent successful pull is still within TTL. Errors are logged as
// warnings and swallowed: the caller proceeds with the local image.
//
// Reports whether a registry round trip actually succeeded now. Only that
// answer lets the background update prefetch skip its own probe: a cache hit
// did no work at all, and a failed pull leaves the local store possibly
// behind the registry, which is the one thing the prefetch exists to notice.
func RefreshIfStale(ctx context.Context, cli client.APIClient, ref string) bool {
	if cached(ref) {
		return false
	}
	return pullAndRecord(ctx, cli, ref)
}

// ForcePull pulls ref unconditionally, ignoring the TTL cache. Backs the
// "always" pull policy: the user has asked for a registry round-trip on every
// shell, so a recent cache hit must not short-circuit it. Best-effort like
// RefreshIfStale — failures are warned and the caller falls back to the local
// image.
func ForcePull(ctx context.Context, cli client.APIClient, ref string) bool {
	return pullAndRecord(ctx, cli, ref)
}

// pullAndRecord pulls ref and stamps the cache marker on success — the shared
// tail of RefreshIfStale (after the cache check) and ForcePull.
func pullAndRecord(ctx context.Context, cli client.APIClient, ref string) bool {
	if !pull(ctx, cli, ref) {
		return false
	}
	record(ref)
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
func pull(ctx context.Context, cli client.APIClient, ref string) bool {
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

// markerPath returns the cache marker path for a given image ref. The
// ref is hashed because tags can contain characters that are awkward in
// filenames (digests, registry paths with ":" / "/"). os.UserHomeDir
// errors are surfaced so the caller treats them as "no cache" rather
// than writing to an unexpected location.
func markerPath(ref string) (string, error) {
	home, err := fsx.Home()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(ref))
	return filepath.Join(home, ".toolbox", "toolbox", "state", "pull-cache", hex.EncodeToString(sum[:])), nil
}

// cached reports whether a successful pull of ref happened within the
// last TTL. Any error (no home dir, missing marker, stat failure)
// returns false so the caller falls through to a real pull — never
// silently skip on uncertainty.
func cached(ref string) bool {
	path, err := markerPath(ref)
	if err != nil {
		return false
	}
	return fsx.MarkerFresh(path, TTL)
}

// record stamps a fresh marker after a successful pull. Persist failures
// (no home dir, mkdir/write rejection from ENOSPC / EROFS / permissions)
// don't break the user — the next invocation just pulls again — but a
// permanently un-writable cache silently turns every shell into a full
// registry round-trip, which adds latency and consumes GHCR rate budget.
// One warning per failure surfaces the root cause without spamming: once
// the underlying issue is fixed the warning stops on its own.
// Marker contents are intentionally empty — modtime is the timestamp.
func record(ref string) {
	path, err := markerPath(ref)
	if err != nil {
		ui.Warning("pull cache: cannot resolve marker path: " + err.Error())
		return
	}
	if err := fsx.TouchMarker(path); err != nil {
		ui.Warning("pull cache: " + err.Error())
	}
}
