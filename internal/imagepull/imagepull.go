// Package imagepull owns the "refresh canonical image, best-effort, with
// a TTL cache" concern that used to live inline in container.Shell. The
// cache marker file lives at ~/.toolbox/state/pull-cache/<sha256-of-ref>
// so it survives across CLI runs; only successful pulls record, so a
// network blip doesn't poison the next invocation into staleness.
//
// The single seam is RefreshIfStale(ctx, cli, ref): cache-hit fast-path,
// pull on miss, record on success. Everything is best-effort — callers
// proceed with the local image regardless. UI surfacing (per-layer
// progress, success/warning lines) stays inside this package so the
// pull concern owns its own observability end-to-end.
package imagepull

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"golang.org/x/term"

	"github.com/filippolmt/toolbox/internal/ui"
)

// TTL bounds how long we trust a previous successful manifest check
// before re-asking the registry. One hour is short enough that a freshly
// pushed image lands on developer machines within the same work block,
// and long enough that rapid `toolbox shell` cycles (open → exit → open)
// don't each pay a round-trip to GHCR. Override is intentional fs-only:
// delete ~/.toolbox/state/pull-cache/* to force a fresh pull on next
// invocation.
const TTL = 1 * time.Hour

// RefreshIfStale refreshes the registry image at ref, best-effort, unless
// a recent successful pull is still within TTL. Errors are logged as
// warnings and swallowed: the caller proceeds with the local image.
func RefreshIfStale(ctx context.Context, cli client.APIClient, ref string) {
	if cached(ref) {
		return
	}
	if pull(ctx, cli, ref) {
		record(ref)
	}
}

// pull attempts to pull the image from its remote registry. The pull
// stream is rendered with per-layer progress bars on a TTY, or as plain
// status lines otherwise — the caller gets real-time feedback instead of
// a silent hang while layers download. Returns true on a clean pull so
// the caller can record it in the cache; returns false on any failure
// path so a poisoned cache never silently masks broken connectivity.
func pull(ctx context.Context, cli client.APIClient, ref string) bool {
	ui.Info("Checking for image updates: " + ref + "...")
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		ui.Warning("image pull failed, using local image if present: " + err.Error())
		return false
	}
	defer rc.Close()

	// Pull progress is diagnostic; keep stdout clean for program output.
	fd := os.Stderr.Fd()
	isTerm := term.IsTerminal(int(fd))
	if err := jsonmessage.DisplayJSONMessagesStream(rc, os.Stderr, fd, isTerm, nil); err != nil {
		ui.Warning("image pull stream error, using local image if present: " + err.Error())
		return false
	}
	ui.Success("Image up to date: " + ref)
	return true
}

// markerPath returns the cache marker path for a given image ref. The
// ref is hashed because tags can contain characters that are awkward in
// filenames (digests, registry paths with ":" / "/"). os.UserHomeDir
// errors are surfaced so the caller treats them as "no cache" rather
// than writing to an unexpected location.
func markerPath(ref string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(ref))
	return filepath.Join(home, ".toolbox", "state", "pull-cache", hex.EncodeToString(sum[:])), nil
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
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < TTL
}

// record stamps a fresh marker after a successful pull. Best-effort: any
// error (no home dir, mkdir/write failure) leaves the cache empty, so
// the next invocation just pulls again. Marker contents are intentionally
// empty — modtime is the timestamp.
func record(ref string) {
	path, err := markerPath(ref)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, nil, 0o644)
}
